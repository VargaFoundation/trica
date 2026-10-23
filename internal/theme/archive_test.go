package theme_test

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/theme"
)

const okManifest = `{"schema":1,"name":"acme","version":"1.0.0"}`

var (
	pngBytes  = "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 16)
	okSVG     = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"><rect width="4" height="4" fill="#123456"/></svg>`
	woff2Head = "wOF2" + strings.Repeat("x", 16)
)

// zipOf builds an archive; a name ending in "/" is a directory entry.
func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	// Stable order, so that a failing case is reproducible.
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, files[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func with(extra map[string]string) map[string]string {
	out := map[string]string{"theme.json": okManifest}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func manifestWith(t *testing.T, mutate func(m map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(okManifest), &m); err != nil {
		t.Fatal(err)
	}
	mutate(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func refusal(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("accepted, want a refusal")
	}
	var r *theme.RefusedError
	if !errors.As(err, &r) {
		t.Fatalf("error is %T (%v), want *RefusedError", err, err)
	}
	return err.Error()
}

func TestReadArchiveAccepts(t *testing.T) {
	th, err := theme.ReadArchive(zipOf(t, map[string]string{
		"theme.json": manifestWith(t, func(m map[string]any) {
			m["fonts"] = map[string]any{"faces": []any{map[string]any{"family": "Acme", "file": "fonts/a.woff2", "weight": "100 900"}}}
			m["logo"] = map[string]any{"light": "logo.svg", "dark": "logo-dark.png", "width": "6rem"}
			m["favicon"] = "icon.svg"
			m["stylesheet"] = "extra.css"
		}),
		"fonts/a.woff2": woff2Head,
		"logo.svg":      okSVG,
		"logo-dark.png": pngBytes,
		"icon.svg":      okSVG,
		"extra.css":     `.brand-mark{background-image:url(logo.svg)} /* c */ a{color:red}`,
		"LICENSE":       "MIT",
		"README.md":     "hello",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if th.Name != "acme" || len(th.SHA) != 64 {
		t.Fatalf("got %+v", th)
	}
	css := string(th.CSS())
	for _, want := range []string{
		`:root:root{`, `:root:root[data-theme="light"]{`, `@media (prefers-color-scheme: light){
:root:root:not(`,
		`@font-face{font-family:"Acme";src:url("/theme/a/` + th.SHA + `/fonts/a.woff2")`,
		`--trica-logo:url("/theme/a/` + th.SHA + `/logo-dark.png")`,
		`url("/theme/a/` + th.SHA + `/logo.svg")`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("css lacks %q:\n%s", want, css)
		}
	}
	if _, ok := th.Favicon(); !ok {
		t.Error("favicon not served")
	}
	if _, ok := th.Asset("README.md"); ok {
		t.Error("an unreferenced file is served")
	}
	if _, ok := th.Asset("extra.css"); ok {
		t.Error("the stylesheet is served as a file (it is inlined)")
	}
}

func TestReadArchiveIdentityIsTheArchiveHash(t *testing.T) {
	a := zipOf(t, with(nil))
	b := zipOf(t, with(map[string]string{"README.md": "x"}))
	ta, err := theme.ReadArchive(a)
	if err != nil {
		t.Fatal(err)
	}
	ta2, _ := theme.ReadArchive(a)
	tb, _ := theme.ReadArchive(b)
	if ta.SHA != ta2.SHA || ta.SHA == tb.SHA {
		t.Fatalf("sha: %s %s %s", ta.SHA, ta2.SHA, tb.SHA)
	}
}

func TestReadArchiveRefuses(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"parent traversal", with(map[string]string{"../x.txt": "x"}), "path holds a . or .. segment"},
		{"nested traversal", with(map[string]string{"a/../../x.txt": "x"}), "path holds a . or .. segment"},
		{"absolute path", with(map[string]string{"/x.txt": "x"}), "path is absolute"},
		{"backslash", with(map[string]string{`a\b.txt`: "x"}), "backslash"},
		{"odd characters", with(map[string]string{"a b.txt": "x"}), "outside letters"},
		{"too deep", with(map[string]string{"a/b/c/d.txt": "x"}), "deeper than"},
		{"case duplicate", with(map[string]string{"A.txt": "x", "a.txt": "y"}), "differ only by case"},
		{"script", with(map[string]string{"x.js": "alert(1)"}), "not allowed in a theme"},
		{"html", with(map[string]string{"x.html": "<p>"}), "not allowed in a theme"},
		{"no manifest", map[string]string{"x.txt": "x"}, "theme.json is missing"},
		{"not json", map[string]string{"theme.json": "{"}, "theme.json:"},
		{"unknown manifest key", map[string]string{"theme.json": `{"schema":1,"name":"a","version":"1","colours":{}}`}, "unknown field"},
		{"two documents", map[string]string{"theme.json": okManifest + okManifest}, "after the end"},
		{"wrong schema", map[string]string{"theme.json": `{"schema":2,"name":"a","version":"1"}`}, "schema is 2"},
		{"bad name", map[string]string{"theme.json": `{"schema":1,"name":"A B","version":"1"}`}, "name"},
		{"reserved name", map[string]string{"theme.json": `{"schema":1,"name":"flat","version":"1"}`}, "reserved"},
		{"unknown extends", map[string]string{"theme.json": `{"schema":1,"name":"a","version":"1","extends":"acme"}`}, "not a built-in theme"},
		{"unknown token", map[string]string{"theme.json": `{"schema":1,"name":"a","version":"1","colors":{"nope":{"light":"#ffffff","dark":"#000000"}}}`}, "not a colour token"},
		{"short hex", map[string]string{"theme.json": `{"schema":1,"name":"a","version":"1","colors":{"ink":{"light":"#fff","dark":"#000000"}}}`}, "#rrggbb"},
		{"rgb()", map[string]string{"theme.json": `{"schema":1,"name":"a","version":"1","colors":{"ink":{"light":"rgb(0,0,0)","dark":"#000000"}}}`}, "#rrggbb"},
		{"one mode only", map[string]string{"theme.json": `{"schema":1,"name":"a","version":"1","colors":{"ink":{"light":"#000000"}}}`}, "light and a dark"},
		{"css in a font stack", map[string]string{"theme.json": `{"schema":1,"name":"a","version":"1","fonts":{"sans":"a;}body{display:none"}}`}, "font-family list"},
		{"huge radius", map[string]string{"theme.json": `{"schema":1,"name":"a","version":"1","radius":{"control":"99px"}}`}, "over 32px"},
		{"fake woff2", with(map[string]string{"f.woff2": "<html>", "theme.json": manifestWith(t, func(m map[string]any) {
			m["fonts"] = map[string]any{"faces": []any{map[string]any{"family": "A", "file": "f.woff2"}}}
		})}), "bad signature"},
		{"png with jpeg magic", with(map[string]string{"l.png": "\xff\xd8\xff\xe0xxxx"}), "bad signature"},
		{"missing logo file", map[string]string{"theme.json": manifestWith(t, func(m map[string]any) { m["logo"] = map[string]any{"light": "nope.svg"} })}, "not in the archive"},
		{"logo of the wrong type", with(map[string]string{"l.ico": "\x00\x00\x01\x00xx", "theme.json": manifestWith(t, func(m map[string]any) { m["logo"] = map[string]any{"light": "l.ico"} })}), "must be one of"},
		{"svg script", with(map[string]string{"l.svg": `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`}), "script"},
		{"svg onload", with(map[string]string{"l.svg": `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`}), "onload"},
		{"svg foreignObject", with(map[string]string{"l.svg": `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject/></svg>`}), "foreignObject"},
		{"svg external href", with(map[string]string{"l.svg": `<svg xmlns="http://www.w3.org/2000/svg" xmlns:x="http://www.w3.org/1999/xlink"><use href="http://evil/x.svg#a"/></svg>`}), "href"},
		{"svg javascript url", with(map[string]string{"l.svg": `<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:alert(1)"/></svg>`}), "href"},
		{"svg doctype", with(map[string]string{"l.svg": `<!DOCTYPE svg [<!ENTITY a "b">]><svg xmlns="http://www.w3.org/2000/svg"/>`}), "DOCTYPE"},
		{"svg css import", with(map[string]string{"l.svg": `<svg xmlns="http://www.w3.org/2000/svg"><style>@import url(http://evil/x.css);</style></svg>`}), "loads or runs"},
		{"svg not svg", with(map[string]string{"l.svg": `<html/>`}), "svg"},
		{"svg not xml", with(map[string]string{"l.svg": `<svg`}), "svg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := theme.ReadArchive(zipOf(t, c.files))
			if got := refusal(t, err); !strings.Contains(got, c.want) {
				t.Errorf("refused with %q, want it to mention %q", got, c.want)
			}
		})
	}
}

func TestReadArchiveRefusesTheStylesheet(t *testing.T) {
	cases := []struct{ name, css, want string }{
		{"import", `@import url(x.css);`, "@import"},
		{"import upper case", `@IMPORT "x.css";`, "@import"},
		{"namespace", `@namespace x "http://x";`, "@namespace"},
		{"remote url", `a{background:url(http://evil/x.png)}`, "no scheme"},
		{"remote quoted url", `a{background:url("https://evil/x.png")}`, "no scheme"},
		{"protocol relative", `a{background:url(//evil/x.png)}`, "no absolute path"},
		{"data url", `a{background:url(data:image/png;base64,AAAA)}`, "no scheme"},
		{"absolute path", `a{background:url(/api/x)}`, "no absolute path"},
		{"missing file", `a{background:url(nope.png)}`, "no such file"},
		{"parent", `a{background:url(../x.png)}`, "leaves the archive"},
		{"query", `a{background:url(logo.svg?x)}`, "no query"},
		{"escaped url", `a{background:u\72l(http://evil/x)}`, "backslash"},
		{"escaped string", `a{background:url("l\6fgo.svg")}`, "escape"},
		{"expression", `a{width:expression(alert(1))}`, "expression"},
		{"moz-binding", `a{-moz-binding:url(x)}`, "-moz-binding"},
		{"behavior", `a{behavior:url(x.htc)}`, "behavior"},
		{"javascript", `a{background:javascript:alert(1)}`, "javascript"},
		{"image-set string", `a{background:image-set("http://evil/x.png" 1x)}`, "no scheme"},
		{"image() string", `a{background:image("http://evil/x.png")}`, "no scheme"},
		{"-webkit-image-set string", `a{background:-webkit-image-set("//evil/x.png" 1x)}`, "no absolute path"},
		{"stylesheet as url", `a{background:url(other.css)}`, "cannot be referenced"},
		{"unterminated comment", `a{}/* x`, "unterminated comment"},
		{"unterminated string", "a{content:\"x\n}", "unterminated string"},
		{"empty url", `a{background:url()}`, "empty url"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := with(map[string]string{
				"theme.json": manifestWith(t, func(m map[string]any) { m["stylesheet"] = "s.css" }),
				"s.css":      c.css, "logo.svg": okSVG, "other.css": "a{}",
			})
			_, err := theme.ReadArchive(zipOf(t, files))
			if got := refusal(t, err); !strings.Contains(got, c.want) {
				t.Errorf("refused with %q, want it to mention %q", got, c.want)
			}
		})
	}
}

func TestReadArchiveRewritesTheStylesheetsReferences(t *testing.T) {
	files := with(map[string]string{
		"theme.json":  manifestWith(t, func(m map[string]any) { m["stylesheet"] = "css/s.css" }),
		"css/s.css":   `a{background:url( "../img/x.png" )} b{background-image:image-set("../img/x.png" 1x)} c{content:"url(http://x)"}`,
		"img/x.png":   pngBytes,
		"img/not.png": pngBytes,
	})
	th, err := theme.ReadArchive(zipOf(t, files))
	if err != nil {
		t.Fatal(err)
	}
	css := string(th.CSS())
	at := `"/theme/a/` + th.SHA + `/img/x.png"`
	if strings.Count(css, at) != 2 || !strings.Contains(css, `content:"url(http://x)"`) {
		t.Errorf("css:\n%s", css)
	}
	if got := th.Files(); fmt.Sprint(got) != "[img/x.png]" {
		t.Errorf("served files %v, want only the one the stylesheet names", got)
	}
}

func TestReadArchiveStripsOneTopDirectoryAndIgnoresMacOSJunk(t *testing.T) {
	th, err := theme.ReadArchive(zipOf(t, map[string]string{
		"acme/":                  "",
		"acme/theme.json":        okManifest,
		"acme/.DS_Store":         "junk",
		"__MACOSX/acme/._x":      "junk",
		"__MACOSX/._acme":        "junk",
		"acme/sub/.DS_Store":     "junk",
		"acme/README.md":         "x",
		"acme/images/logo.svg":   okSVG,
		"acme/images/.DS_Store":  "junk",
		"acme/images/Thumbs.txt": "x",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if th.Name != "acme" {
		t.Fatalf("got %+v", th)
	}
}

func TestReadArchiveLimits(t *testing.T) {
	t.Run("too many entries", func(t *testing.T) {
		files := with(nil)
		for i := 0; i < theme.MaxEntries; i++ {
			files[fmt.Sprintf("f%d.txt", i)] = "x"
		}
		if got := refusal(t, readErr(theme.ReadArchive(zipOf(t, files)))); !strings.Contains(got, "more than 64 entries") {
			t.Error(got)
		}
	})
	t.Run("oversized archive", func(t *testing.T) {
		if got := refusal(t, readErr(theme.ReadArchive(make([]byte, theme.MaxArchiveBytes+1)))); !strings.Contains(got, "limit") {
			t.Error(got)
		}
	})
	t.Run("a file over its limit, however small the archive", func(t *testing.T) {
		big := zipOf(t, with(map[string]string{"README.md": strings.Repeat("a", theme.MaxFileBytes+1)}))
		if len(big) > 100<<10 {
			t.Fatalf("test archive is %d bytes, it should compress well", len(big))
		}
		if got := refusal(t, readErr(theme.ReadArchive(big))); !strings.Contains(got, "limit for a file") {
			t.Error(got)
		}
	})
	t.Run("a decompression bomb", func(t *testing.T) {
		files := with(nil)
		for i := 0; i < 8; i++ {
			files[fmt.Sprintf("b%d.md", i)] = strings.Repeat("a", theme.MaxFileBytes)
		}
		b := zipOf(t, files)
		if len(b) > theme.MaxArchiveBytes {
			t.Fatalf("archive is %d bytes", len(b))
		}
		if got := refusal(t, readErr(theme.ReadArchive(b))); !strings.Contains(got, "decompressed") {
			t.Error(got)
		}
	})
	t.Run("a stylesheet over its limit", func(t *testing.T) {
		files := with(map[string]string{
			"s.css":      strings.Repeat("a{}", theme.MaxStylesheet/3+10),
			"theme.json": manifestWith(t, func(m map[string]any) { m["stylesheet"] = "s.css" }),
		})
		if got := refusal(t, readErr(theme.ReadArchive(zipOf(t, files)))); !strings.Contains(got, "s.css is over 128 KiB") {
			t.Error(got)
		}
	})
	t.Run("a manifest over its limit", func(t *testing.T) {
		files := map[string]string{"theme.json": `{"schema":1,"name":"a","version":"1","description":"` + strings.Repeat("x", theme.MaxManifestBytes) + `"}`}
		if got := refusal(t, readErr(theme.ReadArchive(zipOf(t, files)))); !strings.Contains(got, "theme.json is over 64 KiB") {
			t.Error(got)
		}
	})
}

// readErr drops the theme: refusal() wants the error only.
func readErr(_ *theme.Theme, err error) error { return err }

// rawZip writes entries with full control of the header.
func rawZip(t *testing.T, mutate func(zw *zip.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mutate(zw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadArchiveRefusesHostileEntries(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		b := rawZip(t, func(zw *zip.Writer) {
			w, _ := zw.Create("theme.json")
			_, _ = io.WriteString(w, okManifest)
			h := &zip.FileHeader{Name: "link.txt", Method: zip.Store}
			h.SetMode(os.ModeSymlink | 0o777)
			w, _ = zw.CreateHeader(h)
			_, _ = io.WriteString(w, "/etc/passwd")
		})
		if got := refusal(t, readErr(theme.ReadArchive(b))); !strings.Contains(got, "symbolic link") {
			t.Error(got)
		}
	})
	t.Run("encrypted flag", func(t *testing.T) {
		b := rawZip(t, func(zw *zip.Writer) {
			w, _ := zw.CreateHeader(&zip.FileHeader{Name: "theme.json", Method: zip.Store, Flags: 0x1})
			_, _ = io.WriteString(w, okManifest)
		})
		if got := refusal(t, readErr(theme.ReadArchive(b))); !strings.Contains(got, "encrypted") {
			t.Error(got)
		}
	})
	t.Run("unusual compression method", func(t *testing.T) {
		zw := func(z *zip.Writer) {
			z.RegisterCompressor(99, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, 5) })
			w, _ := z.CreateHeader(&zip.FileHeader{Name: "theme.json", Method: 99})
			_, _ = io.WriteString(w, okManifest)
		}
		if got := refusal(t, readErr(theme.ReadArchive(rawZip(t, zw)))); !strings.Contains(got, "compression method 99") {
			t.Error(got)
		}
	})
	t.Run("duplicate entry", func(t *testing.T) {
		b := rawZip(t, func(zw *zip.Writer) {
			for i := 0; i < 2; i++ {
				w, _ := zw.Create("theme.json")
				_, _ = io.WriteString(w, okManifest)
			}
		})
		if got := refusal(t, readErr(theme.ReadArchive(b))); !strings.Contains(got, "appears twice") {
			t.Error(got)
		}
	})
	t.Run("declared size is not trusted", func(t *testing.T) {
		// The header claims 10 bytes, the data is 2 MiB of zeros.
		b := rawZip(t, func(zw *zip.Writer) {
			w, _ := zw.Create("theme.json")
			_, _ = io.WriteString(w, okManifest)
			w, _ = zw.Create("big.md")
			_, _ = w.Write(make([]byte, 2*theme.MaxFileBytes))
		})
		if got := refusal(t, readErr(theme.ReadArchive(b))); !strings.Contains(got, "limit for a file") {
			t.Error(got)
		}
	})
	t.Run("not a zip", func(t *testing.T) {
		if got := refusal(t, readErr(theme.ReadArchive([]byte("hello")))); !strings.Contains(got, "not a zip") {
			t.Error(got)
		}
	})
}

func TestContrastRefusesAnUnreadableTheme(t *testing.T) {
	bad := manifestWith(t, func(m map[string]any) {
		m["colors"] = map[string]any{"ink": map[string]string{"light": "#eeeeee", "dark": "#222222"}}
	})
	_, err := theme.ReadArchive(zipOf(t, map[string]string{"theme.json": bad}))
	got := refusal(t, err)
	for _, want := range []string{"contrast in light: ink on surface", "contrast in dark: ink on surface"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal %q lacks %q", got, want)
		}
	}
	// What is not restated is the console's own and passes.
	if _, err := theme.ReadArchive(zipOf(t, with(nil))); err != nil {
		t.Fatal(err)
	}
}

func TestExtendsLaysTheThemeOverABuiltin(t *testing.T) {
	m := manifestWith(t, func(m map[string]any) {
		m["extends"] = "flat"
		m["colors"] = map[string]any{"accent": map[string]string{"light": "#0000aa", "dark": "#4040ff"}}
	})
	th, err := theme.ReadArchive(zipOf(t, map[string]string{"theme.json": m}))
	if err != nil {
		t.Fatal(err)
	}
	css := string(th.CSS())
	for _, want := range []string{"--trica-radius-control:0px", "--trica-accent:#4040ff", "--trica-accent:#0000aa", "--trica-signal:#53ff64"} {
		if !strings.Contains(css, want) {
			t.Errorf("css lacks %q", want)
		}
	}
	if strings.Contains(css, "--trica-accent:#161616") {
		t.Error("the base's accent was not replaced")
	}
}

func TestReadDirFollowsKubernetesLinksButNotEscapes(t *testing.T) {
	// A ConfigMap mount: theme.json -> ..data/theme.json, ..data -> ..2024_01/.
	k8s := t.TempDir()
	rev := filepath.Join(k8s, "..2024_01")
	must(t, os.Mkdir(rev, 0o755))
	must(t, os.WriteFile(filepath.Join(rev, "theme.json"), []byte(okManifest), 0o600))
	must(t, os.Symlink("..2024_01", filepath.Join(k8s, "..data")))
	must(t, os.Symlink(filepath.Join("..data", "theme.json"), filepath.Join(k8s, "theme.json")))
	th, err := theme.ReadDir(k8s)
	if err != nil {
		t.Fatal(err)
	}
	if th.Name != "acme" {
		t.Fatalf("got %+v", th)
	}

	// A link that leaves the directory is refused, not followed.
	secret := filepath.Join(t.TempDir(), "secret.txt")
	must(t, os.WriteFile(secret, []byte("s3cret"), 0o600))
	evil := t.TempDir()
	must(t, os.WriteFile(filepath.Join(evil, "theme.json"), []byte(okManifest), 0o600))
	must(t, os.Symlink(secret, filepath.Join(evil, "README.md")))
	if _, err := theme.ReadDir(evil); err == nil {
		t.Fatal("a link out of the directory was followed")
	}
	must(t, os.Remove(filepath.Join(evil, "README.md")))
	must(t, os.Symlink("../../../../etc", filepath.Join(evil, "etc")))
	if _, err := theme.ReadDir(evil); err == nil {
		t.Fatal("a link to a directory outside was followed")
	}
}

func TestReadDirIdentityAndLayout(t *testing.T) {
	mk := func(readme string) string {
		d := t.TempDir()
		must(t, os.WriteFile(filepath.Join(d, "theme.json"), []byte(okManifest), 0o600))
		must(t, os.WriteFile(filepath.Join(d, ".hidden"), []byte("ignored"), 0o600))
		must(t, os.Mkdir(filepath.Join(d, "img"), 0o755))
		must(t, os.WriteFile(filepath.Join(d, "img", "logo.svg"), []byte(okSVG), 0o600))
		must(t, os.WriteFile(filepath.Join(d, "README.md"), []byte(readme), 0o600))
		return d
	}
	a, err := theme.ReadDir(mk("one"))
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := theme.ReadDir(mk("one"))
	b, _ := theme.ReadDir(mk("two"))
	if a.SHA != a2.SHA || a.SHA == b.SHA {
		t.Errorf("identity: %s %s %s", a.SHA, a2.SHA, b.SHA)
	}
	if _, err := theme.ReadDir(t.TempDir()); err == nil {
		t.Error("an empty directory was accepted")
	}
}

func TestReadPath(t *testing.T) {
	d := t.TempDir()
	zf := filepath.Join(d, "t.zip")
	must(t, os.WriteFile(zf, zipOf(t, with(nil)), 0o600))
	if th, err := theme.ReadPath(zf); err != nil || th.Name != "acme" {
		t.Fatalf("zip: %v %v", th, err)
	}
	big := filepath.Join(d, "big.zip")
	must(t, os.WriteFile(big, make([]byte, theme.MaxArchiveBytes+1), 0o600))
	refusal(t, readErr(theme.ReadPath(big)))
	if _, err := theme.ReadPath(filepath.Join(d, "none")); err == nil {
		t.Error("a missing path was accepted")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func FuzzReadArchive(f *testing.F) {
	f.Add(zipOf(&testing.T{}, nil))
	f.Add([]byte("PK\x03\x04"))
	f.Fuzz(func(t *testing.T, b []byte) {
		// Whatever the bytes, it returns: a theme or an error, never a panic.
		if th, err := theme.ReadArchive(b); err == nil && th == nil {
			t.Fatal("nil theme without an error")
		}
	})
}
