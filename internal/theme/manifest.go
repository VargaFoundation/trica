package theme

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// SchemaVersion is the only manifest version this package reads.
const SchemaVersion = 1

// Pair is a colour in both modes: a theme restates a colour for light and for
// dark together, never for one, so that no mode keeps a colour that was tuned
// for the other.
type Pair struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

// Face is a web font shipped in the archive.
type Face struct {
	Family string `json:"family"`
	File   string `json:"file"`
	Weight string `json:"weight,omitempty"`
	Style  string `json:"style,omitempty"`
}

// Fonts sets the type stacks and the title weight.
type Fonts struct {
	Sans        string `json:"sans,omitempty"`
	Mono        string `json:"mono,omitempty"`
	Title       string `json:"title,omitempty"`
	TitleWeight int    `json:"title_weight,omitempty"`
	Faces       []Face `json:"faces,omitempty"`
}

// Radius sets the two corner radii.
type Radius struct {
	Control string `json:"control,omitempty"`
	Surface string `json:"surface,omitempty"`
}

// Logo is the brand mark of the sidebar, one file per mode.
type Logo struct {
	Light string `json:"light,omitempty"`
	Dark  string `json:"dark,omitempty"`
	Width string `json:"width,omitempty"`
}

// Manifest is theme.json.
type Manifest struct {
	Schema      int             `json:"schema"`
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Version     string          `json:"version"`
	Description string          `json:"description,omitempty"`
	Extends     string          `json:"extends,omitempty"`
	Colors      map[string]Pair `json:"colors,omitempty"`
	Fonts       *Fonts          `json:"fonts,omitempty"`
	Radius      *Radius         `json:"radius,omitempty"`
	Logo        *Logo           `json:"logo,omitempty"`
	// SignalBar is the width of the bar drawn on the active sidebar entry in
	// the signal colour; zero (the default) draws none.
	SignalBar  string `json:"signal_bar,omitempty"`
	Favicon    string `json:"favicon,omitempty"`
	Stylesheet string `json:"stylesheet,omitempty"`
}

var (
	reName     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	reVersion  = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`)
	reColour   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	reStack    = regexp.MustCompile(`^[A-Za-z0-9 ,"'_-]{1,200}$`)
	reFamily   = regexp.MustCompile(`^[A-Za-z0-9 _-]{1,64}$`)
	reWeight   = regexp.MustCompile(`^(normal|bold|[1-9]00)( [1-9]00)?$`)
	reLength   = regexp.MustCompile(`^(\d{1,2}(?:\.\d+)?)(px|rem)$`)
	reFontFace = regexp.MustCompile(`^(normal|italic|oblique)$`)
)

// Limits of the manifest's values, in CSS pixels (a rem is 16 px).
const (
	maxRadiusPx  = 32
	maxLogoPx    = 160
	maxSignalPx  = 8
	titleMaxLen  = 64
	descMaxLen   = 280
	maxFontFaces = 8
)

// parseManifest decodes theme.json strictly: an unknown key is refused, so a
// typo in a token name never silently does nothing.
func parseManifest(raw []byte) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("theme.json: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("theme.json: data after the end of the object")
	}
	return &m, nil
}

// lengthPx parses a CSS length of the manifest ("0px", "1.5rem") into pixels.
func lengthPx(s string) (float64, bool) {
	m := reLength.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	if m[2] == "rem" {
		n *= 16
	}
	return n, true
}

func checkLength(c *checker, what, v string, maxPx float64) {
	px, ok := lengthPx(v)
	switch {
	case !ok:
		c.add("%s %q is not a length such as 4px or 0.25rem", what, v)
	case px > maxPx:
		c.add("%s %q is over %gpx", what, v, maxPx)
	}
}

// validate checks the manifest alone (the files are checked by the caller).
// builtin is true for the embedded themes, which may use the reserved names.
func (m *Manifest) validate(c *checker, builtin bool) {
	if m.Schema != SchemaVersion {
		c.add("theme.json: schema is %d, this server reads schema %d", m.Schema, SchemaVersion)
	}
	if !reName.MatchString(m.Name) {
		c.add("theme.json: name %q must match %s", m.Name, reName)
	} else if !builtin && isBuiltinName(m.Name) {
		c.add("theme.json: name %q is reserved for a built-in theme", m.Name)
	}
	if len([]rune(m.Title)) > titleMaxLen {
		c.add("theme.json: title is over %d characters", titleMaxLen)
	}
	if !reVersion.MatchString(m.Version) {
		c.add("theme.json: version %q must match %s", m.Version, reVersion)
	}
	if len([]rune(m.Description)) > descMaxLen {
		c.add("theme.json: description is over %d characters", descMaxLen)
	}
	if m.Extends != "" {
		switch {
		case builtin:
			c.add("theme.json: a built-in theme cannot extend another")
		case !isBuiltinName(m.Extends):
			c.add("theme.json: extends %q is not a built-in theme (%s)", m.Extends, strings.Join(BuiltinNames(), ", "))
		}
	}
	for _, k := range sortedKeys(m.Colors) {
		p := m.Colors[k]
		if !isColourToken(k) {
			c.add("theme.json: colors.%s is not a colour token of the console", k)
			continue
		}
		if !reColour.MatchString(p.Light) || !reColour.MatchString(p.Dark) {
			c.add("theme.json: colors.%s needs a light and a dark value, each #rrggbb", k)
		}
	}
	if f := m.Fonts; f != nil {
		for _, s := range []struct{ name, v string }{{"sans", f.Sans}, {"mono", f.Mono}, {"title", f.Title}} {
			if s.v != "" && !reStack.MatchString(s.v) {
				c.add("theme.json: fonts.%s is not a font-family list (letters, digits, spaces, commas, quotes, - and _; 200 characters at most)", s.name)
			}
		}
		if w := f.TitleWeight; w != 0 && (w < 100 || w > 900 || w%100 != 0) {
			c.add("theme.json: fonts.title_weight %d must be 100, 200, ... 900", w)
		}
		if len(f.Faces) > maxFontFaces {
			c.add("theme.json: fonts.faces has more than %d entries", maxFontFaces)
		}
		for i, fc := range f.Faces {
			if !reFamily.MatchString(fc.Family) {
				c.add("theme.json: fonts.faces[%d].family %q is not a font name", i, fc.Family)
			}
			if fc.Weight != "" && !reWeight.MatchString(fc.Weight) {
				c.add("theme.json: fonts.faces[%d].weight %q is not normal, bold, 100..900 or a range such as \"100 900\"", i, fc.Weight)
			}
			if fc.Style != "" && !reFontFace.MatchString(fc.Style) {
				c.add("theme.json: fonts.faces[%d].style %q must be normal, italic or oblique", i, fc.Style)
			}
		}
	}
	if r := m.Radius; r != nil {
		if r.Control != "" {
			checkLength(c, "theme.json: radius.control", r.Control, maxRadiusPx)
		}
		if r.Surface != "" {
			checkLength(c, "theme.json: radius.surface", r.Surface, maxRadiusPx)
		}
	}
	if l := m.Logo; l != nil {
		if l.Light == "" && l.Dark == "" {
			c.add("theme.json: logo needs a light or a dark file")
		}
		if l.Width != "" {
			checkLength(c, "theme.json: logo.width", l.Width, maxLogoPx)
		}
	}
	if m.SignalBar != "" {
		checkLength(c, "theme.json: signal_bar", m.SignalBar, maxSignalPx)
	}
}

// refPaths are the files a manifest names, with the extensions each may have.
type fileRef struct {
	what string
	file string
	exts []string
}

func (m *Manifest) refs() []fileRef {
	var out []fileRef
	if f := m.Fonts; f != nil {
		for i, fc := range f.Faces {
			out = append(out, fileRef{fmt.Sprintf("fonts.faces[%d].file", i), fc.File, []string{".woff2"}})
		}
	}
	if l := m.Logo; l != nil {
		img := []string{".svg", ".png", ".webp", ".jpg", ".jpeg"}
		if l.Light != "" {
			out = append(out, fileRef{"logo.light", l.Light, img})
		}
		if l.Dark != "" {
			out = append(out, fileRef{"logo.dark", l.Dark, img})
		}
	}
	if m.Favicon != "" {
		out = append(out, fileRef{"favicon", m.Favicon, []string{".svg", ".png", ".ico"}})
	}
	if m.Stylesheet != "" {
		out = append(out, fileRef{"stylesheet", m.Stylesheet, []string{".css"}})
	}
	return out
}

func hasExt(name string, exts []string) bool {
	e := strings.ToLower(path.Ext(name))
	for _, x := range exts {
		if e == x {
			return true
		}
	}
	return false
}
