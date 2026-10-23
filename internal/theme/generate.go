package theme

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
)

// Asset is a file of a theme, as served.
type Asset struct {
	ContentType string
	Body        []byte
}

// Theme is a validated theme: its stylesheet is generated once, here, and only
// the files the stylesheet and the manifest name can be served.
type Theme struct {
	Name        string
	Title       string
	Version     string
	Description string
	// SHA is the identity of the theme: the sha256 of the archive, or of the
	// directory listing (see ReadDir). Built-in themes carry "builtin:<name>".
	SHA string
	// Source is "builtin" or "path"; the service sets it.
	Source string

	css     []byte
	etag    string
	assets  map[string]Asset // by path in the archive
	favicon string           // path of the favicon in assets, "" for none
}

// CSS is the generated stylesheet, served at /theme/theme.css.
func (t *Theme) CSS() []byte { return t.css }

// ETag of the stylesheet, quoted.
func (t *Theme) ETag() string { return t.etag }

// Favicon returns the theme's icon, when it ships one.
func (t *Theme) Favicon() (Asset, bool) {
	a, ok := t.assets[t.favicon]
	return a, t.favicon != "" && ok
}

// Asset returns a file the stylesheet or the manifest references.
func (t *Theme) Asset(file string) (Asset, bool) {
	a, ok := t.assets[file]
	return a, ok
}

// Files lists the files that can be served, sorted.
func (t *Theme) Files() []string { return sortedKeys(t.assets) }

// build validates a set of files (already normalised, names checked) and
// generates the theme. builtin is true for the embedded manifests.
func build(files map[string][]byte, sha string, builtin bool) (*Theme, error) {
	var c checker
	for _, name := range sortedKeys(files) {
		checkFile(name, files[name], &c)
	}
	raw, ok := files[ManifestName]
	if !ok {
		c.add("%s is missing at the root of the theme", ManifestName)
		return nil, c.err()
	}
	m, err := parseManifest(raw)
	if err != nil {
		c.add("%v", err)
		return nil, c.err()
	}
	m.validate(&c, builtin)

	// What it extends, merged under it.
	merged := *m
	merged.Colors = map[string]Pair{}
	if m.Extends != "" && isBuiltinName(m.Extends) {
		if base, err := builtinManifest(m.Extends); err == nil {
			merged = mergeManifests(base, m)
		}
	} else {
		for k, v := range m.Colors {
			merged.Colors[k] = v
		}
	}

	for _, r := range m.refs() {
		if err := checkName(r.file); err != nil {
			c.add("theme.json: %s %q: %v", r.what, r.file, err)
			continue
		}
		if _, ok := files[r.file]; !ok {
			c.add("theme.json: %s names %q, which is not in the archive", r.what, r.file)
			continue
		}
		if !hasExt(r.file, r.exts) {
			c.add("theme.json: %s %q must be one of %s", r.what, r.file, strings.Join(r.exts, ", "))
		}
	}

	used := map[string]bool{}
	for _, r := range m.refs() {
		if r.what != "stylesheet" {
			used[r.file] = true
		}
	}
	var userCSS string
	if m.Stylesheet != "" {
		if body, ok := files[m.Stylesheet]; ok && checkName(m.Stylesheet) == nil {
			res := fileResolver(path.Dir(m.Stylesheet), files, sha, used)
			if userCSS, err = rewriteCSS(string(body), res); err != nil {
				c.add("%s: %v", m.Stylesheet, err)
			}
		}
	}

	// The contrast is read on valid colours only: a malformed one is already
	// reported, and has no luminance.
	if len(c.v) == 0 {
		for _, mode := range []string{Dark, Light} {
			for _, v := range CheckContrast(mode, effective(mode, merged.Colors)) {
				c.add("%s", v)
			}
		}
	}
	if err := c.err(); err != nil {
		return nil, err
	}

	t := &Theme{Name: m.Name, Title: m.Title, Version: m.Version, Description: m.Description, SHA: sha, assets: map[string]Asset{}}
	if t.Title == "" {
		t.Title = m.Name
	}
	for f := range used {
		if _, ok := files[f]; ok {
			t.assets[f] = Asset{ContentType: contentType(f), Body: files[f]}
		}
	}
	if m.Favicon != "" {
		t.favicon = m.Favicon
	}
	t.css = generateCSS(t, &merged, userCSS)
	sum := sha256.Sum256(t.css)
	t.etag = `"` + hex.EncodeToString(sum[:8]) + `"`
	return t, nil
}

func builtinManifest(name string) (*Manifest, error) {
	raw, err := builtinFS.ReadFile("builtin/" + name + "/theme.json")
	if err != nil {
		return nil, err
	}
	return parseManifest(raw)
}

// mergeManifests lays m over base: what m states wins, the rest comes from base.
func mergeManifests(base, m *Manifest) Manifest {
	out := *m
	out.Colors = map[string]Pair{}
	for k, v := range base.Colors {
		out.Colors[k] = v
	}
	for k, v := range m.Colors {
		out.Colors[k] = v
	}
	f := Fonts{}
	if base.Fonts != nil {
		f = *base.Fonts
	}
	if m.Fonts != nil {
		mf := *m.Fonts
		if mf.Sans != "" {
			f.Sans = mf.Sans
		}
		if mf.Mono != "" {
			f.Mono = mf.Mono
		}
		if mf.Title != "" {
			f.Title = mf.Title
		}
		if mf.TitleWeight != 0 {
			f.TitleWeight = mf.TitleWeight
		}
		f.Faces = append(append([]Face(nil), f.Faces...), mf.Faces...)
	}
	out.Fonts = &f
	r := Radius{}
	if base.Radius != nil {
		r = *base.Radius
	}
	if m.Radius != nil {
		if m.Radius.Control != "" {
			r.Control = m.Radius.Control
		}
		if m.Radius.Surface != "" {
			r.Surface = m.Radius.Surface
		}
	}
	out.Radius = &r
	if out.SignalBar == "" {
		out.SignalBar = base.SignalBar
	}
	if out.Logo == nil {
		out.Logo = base.Logo
	}
	return out
}

// generateCSS writes the stylesheet. Each block repeats :root so that it
// outranks the same block of tokens.css by one class, whatever the order of
// the two <link>s: a test pins the specificities.
func generateCSS(t *Theme, m *Manifest, userCSS string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "/* trica theme %s %s (%s) */\n", m.Name, m.Version, strings.TrimPrefix(t.SHA, "builtin:"))
	if f := m.Fonts; f != nil {
		for _, fc := range f.Faces {
			fmt.Fprintf(&b, "@font-face{font-family:%q;src:url(%q) format(\"woff2\");", fc.Family, servedPath(t.SHA, fc.File))
			if fc.Weight != "" {
				fmt.Fprintf(&b, "font-weight:%s;", fc.Weight)
			}
			if fc.Style != "" {
				fmt.Fprintf(&b, "font-style:%s;", fc.Style)
			}
			b.WriteString("font-display:swap}\n")
		}
	}
	colours := func(mode string) string {
		var s strings.Builder
		for _, k := range sortedKeys(m.Colors) {
			v := m.Colors[k].Dark
			if mode == Light {
				v = m.Colors[k].Light
			}
			fmt.Fprintf(&s, "--trica-%s:%s;", k, v)
		}
		return s.String()
	}
	logo := func(mode string) string {
		l := m.Logo
		if l == nil {
			return ""
		}
		file := l.Dark
		if mode == Light {
			file = l.Light
		}
		if file == "" { // one file serves both modes
			file = l.Light + l.Dark
		}
		return fmt.Sprintf("--trica-logo:url(%q);--trica-brand-icon:none;--trica-brand-ground:transparent;", servedPath(t.SHA, file))
	}
	var plain strings.Builder
	if f := m.Fonts; f != nil {
		for _, kv := range [][2]string{{"font-sans", f.Sans}, {"font-mono", f.Mono}, {"font-title", f.Title}} {
			if kv[1] != "" {
				fmt.Fprintf(&plain, "--trica-%s:%s;", kv[0], kv[1])
			}
		}
		if f.TitleWeight != 0 {
			fmt.Fprintf(&plain, "--trica-font-title-weight:%d;", f.TitleWeight)
		}
	}
	if r := m.Radius; r != nil {
		if r.Control != "" {
			fmt.Fprintf(&plain, "--trica-radius-control:%s;", r.Control)
		}
		if r.Surface != "" {
			fmt.Fprintf(&plain, "--trica-radius-surface:%s;", r.Surface)
		}
	}
	if m.SignalBar != "" {
		fmt.Fprintf(&plain, "--trica-signal-width:%s;", m.SignalBar)
	}
	if m.Logo != nil && m.Logo.Width != "" {
		fmt.Fprintf(&plain, "--trica-logo-width:%s;", m.Logo.Width)
	}
	fmt.Fprintf(&b, ":root:root{%s%s%s}\n", colours(Dark), plain.String(), logo(Dark))
	fmt.Fprintf(&b, ":root:root[data-theme=\"light\"]{%s%s}\n", colours(Light), logo(Light))
	fmt.Fprintf(&b, "@media (prefers-color-scheme: light){\n:root:root:not([data-theme=\"light\"]):not([data-theme=\"dark\"]){%s%s}\n}\n", colours(Light), logo(Light))
	if userCSS != "" {
		b.WriteString(userCSS)
		if !strings.HasSuffix(userCSS, "\n") {
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}
