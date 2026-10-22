package theme

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
)

// defaults.json is a snapshot of the colours of web/src/styles/tokens.css, the
// theme every other one starts from. TestDefaultsMatchTokensCSS fails when it
// is stale; TRICA_UPDATE_THEME_FILES=1 go test ./internal/theme rewrites it (and the golden stylesheets of testdata/).
//
//go:embed defaults.json
var defaultsJSON []byte

// contrast.json lists the foreground/background pairs the stylesheet uses and
// their minimum WCAG ratio. web/src/styles/tokens.test.ts reads the same file.
//
//go:embed contrast.json
var contrastJSON []byte

// Mode names the two colour schemes.
const (
	Dark  = "dark"
	Light = "light"
)

// Palette maps a colour token (the CSS custom property without its --trica-
// prefix) to a #rrggbb value.
type Palette map[string]string

// Defaults is the palette of tokens.css in both modes.
type defaults struct {
	Dark  Palette `json:"dark"`
	Light Palette `json:"light"`
}

type pair struct {
	FG, BG string
	Min    float64
}

var (
	baseline defaults
	pairs    []pair
)

func init() {
	if err := json.Unmarshal(defaultsJSON, &baseline); err != nil {
		panic(fmt.Sprintf("theme: defaults.json: %v", err))
	}
	var raw [][3]any
	if err := json.Unmarshal(contrastJSON, &raw); err != nil {
		panic(fmt.Sprintf("theme: contrast.json: %v", err))
	}
	for _, r := range raw {
		fg, ok1 := r[0].(string)
		bg, ok2 := r[1].(string)
		min, ok3 := r[2].(float64)
		if !ok1 || !ok2 || !ok3 {
			panic("theme: contrast.json: a pair is [foreground, background, minimum]")
		}
		pairs = append(pairs, pair{fg, bg, min})
	}
}

// ColourTokens returns the names a theme may set as a colour, sorted.
func ColourTokens() []string {
	out := make([]string, 0, len(baseline.Dark))
	for k := range baseline.Dark {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func isColourToken(name string) bool {
	_, ok := baseline.Dark[name]
	return ok
}

// Violation is a pair of the palette that does not read.
type Violation struct {
	Mode   string
	FG, BG string
	Ratio  float64
	Min    float64
}

func (v Violation) String() string {
	return fmt.Sprintf("contrast in %s: %s on %s is %.2f:1, needs %.1f:1", v.Mode, v.FG, v.BG, v.Ratio, v.Min)
}

// CheckContrast checks the palette of a mode against every pair of
// contrast.json and returns every pair that fails, in the order of the file.
// A token the palette lacks is reported too.
func CheckContrast(mode string, p Palette) []Violation {
	var out []Violation
	for _, pr := range pairs {
		fg, ok1 := p[pr.FG]
		bg, ok2 := p[pr.BG]
		if !ok1 || !ok2 {
			out = append(out, Violation{Mode: mode, FG: pr.FG, BG: pr.BG, Min: pr.Min})
			continue
		}
		if r := Contrast(fg, bg); r < pr.Min {
			out = append(out, Violation{Mode: mode, FG: pr.FG, BG: pr.BG, Ratio: r, Min: pr.Min})
		}
	}
	return out
}

// Contrast is the WCAG 2 contrast ratio of two #rrggbb colours.
func Contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) float64 {
	var c [3]float64
	for i := range c {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		x := float64(v) / 255
		if x <= 0.03928 {
			c[i] = x / 12.92
		} else {
			c[i] = math.Pow((x+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

// effective returns the palette of a mode once the layers are applied in
// order over the defaults.
func effective(mode string, layers ...map[string]Pair) Palette {
	base := baseline.Dark
	if mode == Light {
		base = baseline.Light
	}
	out := make(Palette, len(base))
	for k, v := range base {
		out[k] = v
	}
	for _, l := range layers {
		for k, p := range l {
			if mode == Light {
				out[k] = p.Light
			} else {
				out[k] = p.Dark
			}
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
