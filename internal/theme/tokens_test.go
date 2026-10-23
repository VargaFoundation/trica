package theme_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/theme"
)

const tokensCSS = "../../web/src/styles/tokens.css"

var reHexToken = regexp.MustCompile(`--trica-([a-z0-9-]+):\s*(#[0-9a-fA-F]{6});`)

// colours of the first block opened by selector in tokens.css.
func colours(t *testing.T, css, selector string) map[string]string {
	t.Helper()
	i := strings.Index(css, "\n"+selector+" {")
	if i < 0 {
		t.Fatalf("tokens.css has no %s block", selector)
	}
	j := strings.Index(css[i:], "\n}")
	out := map[string]string{}
	for _, m := range reHexToken.FindAllStringSubmatch(css[i:i+j], -1) {
		out[m[1]] = strings.ToLower(m[2])
	}
	return out
}

func updating() bool { return os.Getenv("TRICA_UPDATE_THEME_FILES") == "1" }

func TestDefaultsMatchTokensCSS(t *testing.T) {
	raw, err := os.ReadFile(tokensCSS)
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	want := map[string]map[string]string{
		"dark":  colours(t, css, ":root"),
		"light": colours(t, css, `:root[data-theme="light"]`),
	}
	if updating() {
		b, _ := json.MarshalIndent(want, "", " ")
		if err := os.WriteFile("defaults.json", append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	have, err := os.ReadFile("defaults.json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]string
	if err := json.Unmarshal(have, &got); err != nil {
		t.Fatal(err)
	}
	for mode, w := range want {
		g := got[mode]
		if len(g) != len(w) {
			t.Errorf("%s: defaults.json has %d colours, tokens.css %d; run TRICA_UPDATE_THEME_FILES=1 go test ./internal/theme", mode, len(g), len(w))
		}
		for k, v := range w {
			if g[k] != v {
				t.Errorf("%s: --trica-%s is %s in tokens.css, %q in defaults.json; run TRICA_UPDATE_THEME_FILES=1 go test ./internal/theme", mode, k, v, g[k])
			}
		}
	}
	if len(theme.ColourTokens()) != len(want["dark"]) {
		t.Errorf("ColourTokens() has %d names, tokens.css %d", len(theme.ColourTokens()), len(want["dark"]))
	}
}

var reSelector = regexp.MustCompile(`(?m)^\s*(:root[^{;]*?)\s*\{`)

// specificity counts what ranks a selector here: one per :root and per
// attribute selector (the :not() arguments are attribute selectors).
func specificity(sel string) int { return strings.Count(sel, ":root") + strings.Count(sel, "[") }

func selectors(css string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range reSelector.FindAllStringSubmatch(css, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// A theme is linked after tokens.css, but an order is no guarantee (a cache, a
// proxy that rewrites the head): each of its selectors outranks the one it
// restates, so that it wins on weight, not on position.
func TestGeneratedSelectorsOutrankTokensCSS(t *testing.T) {
	raw, err := os.ReadFile(tokensCSS)
	if err != nil {
		t.Fatal(err)
	}
	base := selectors(string(raw))
	if len(base) != 3 {
		t.Fatalf("tokens.css selectors changed shape: %q", base)
	}
	th, err := theme.Builtin("flat")
	if err != nil {
		t.Fatal(err)
	}
	gen := selectors(string(th.CSS()))
	if len(gen) != 3 {
		t.Fatalf("generated selectors: %q", gen)
	}
	for i := range base {
		if specificity(gen[i]) <= specificity(base[i]) {
			t.Errorf("%s (%d) does not outrank %s (%d)", gen[i], specificity(gen[i]), base[i], specificity(base[i]))
		}
	}
}

func TestGeneratedCSSMatchesTheGoldenFiles(t *testing.T) {
	for _, name := range theme.BuiltinNames() {
		th, err := theme.Builtin(name)
		if err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("testdata", name+".css")
		if updating() {
			if err := os.WriteFile(golden, th.CSS(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != string(th.CSS()) {
			t.Errorf("%s: generated stylesheet differs from %s; run TRICA_UPDATE_THEME_FILES=1 go test ./internal/theme and read the diff", name, golden)
		}
	}
}

// Every built-in restates colours in both modes only, and the flat one is the
// whole palette: nothing falls back on a colour tuned for the other look.
func TestFlatRestatesEveryColourToken(t *testing.T) {
	th, _ := theme.Builtin("flat")
	css := string(th.CSS())
	for _, k := range theme.ColourTokens() {
		if strings.Count(css, "--trica-"+k+":") != 3 {
			t.Errorf("flat does not restate --trica-%s once per block", k)
		}
	}
}

func TestContrastCheckSeesAFailingPair(t *testing.T) {
	p := theme.Palette{"ink": "#777777", "surface": "#808080"}
	if v := theme.CheckContrast(theme.Light, p); len(v) == 0 || !strings.Contains(v[0].String(), "ink on surface") {
		t.Fatalf("violations: %v", v)
	}
	if r := theme.Contrast("#000000", "#ffffff"); r < 20.9 || r > 21.1 {
		t.Errorf("black on white is %.2f", r)
	}
}
