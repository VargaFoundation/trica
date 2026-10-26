package schema

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// A display template (ADR 0065) builds the display name of an object from
// several attributes: "{name} · {model|cpu}". A placeholder is {attr} or
// {attr|formatter}; everything else is literal text.
//
// The literal text before a placeholder is its separator. A placeholder whose
// attribute is missing or empty renders nothing, and its separator with it. The
// next placeholder that renders takes the separator of the first one skipped
// before it ("{name} · {tech} {size}" without tech reads "x · 16 GB"). Nothing
// is written before the first value, except the text before the very first
// placeholder of the template, a prefix kept with that placeholder only. Text
// after the last placeholder is kept when something rendered.

// Formatter names accepted after the "|" of a placeholder.
var formatters = map[string]func(string) string{
	"upper": strings.ToUpper,
	"media": func(s string) string {
		if s == "unknown" {
			return ""
		}
		return strings.ToUpper(s)
	},
	"cpu":   tidyCPU,
	"bytes": func(s string) string { return humanBytes(s, 1000) },
	"ram":   func(s string) string { return humanBytes(s, 1024) },
	"watts": watts,
}

type tmplPart struct {
	lit       string // text before the placeholder
	attr      string
	formatter string
}

func parseTemplate(tmpl string) ([]tmplPart, string, error) {
	var parts []tmplPart
	rest := tmpl
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			break
		}
		end := strings.IndexByte(rest[open:], '}')
		if end < 0 {
			return nil, "", fmt.Errorf("display_template %q: unclosed {", tmpl)
		}
		body := rest[open+1 : open+end]
		attr, fm, _ := strings.Cut(body, "|")
		if attr == "" {
			return nil, "", fmt.Errorf("display_template %q: empty placeholder", tmpl)
		}
		if fm != "" {
			if _, ok := formatters[fm]; !ok {
				return nil, "", fmt.Errorf("display_template %q: unknown formatter %q", tmpl, fm)
			}
		}
		parts = append(parts, tmplPart{lit: rest[:open], attr: attr, formatter: fm})
		rest = rest[open+end+1:]
	}
	if strings.ContainsRune(rest, '}') {
		return nil, "", fmt.Errorf("display_template %q: unmatched }", tmpl)
	}
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("display_template %q: no placeholder", tmpl)
	}
	return parts, rest, nil
}

// validateTemplate checks that every placeholder names an attribute of t and a
// known formatter.
func validateTemplate(t *Type) error {
	if t.DisplayTemplate == "" {
		return nil
	}
	parts, _, err := parseTemplate(t.DisplayTemplate)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if _, ok := t.Attributes[p.attr]; !ok {
			return fmt.Errorf("display_template %q: %q is not an attribute", t.DisplayTemplate, p.attr)
		}
	}
	return nil
}

// RenderDisplay renders the display template of t with attr, which returns the
// text of an attribute ("" when it has no value). It returns "" when the type
// has no template, when the template renders empty, or when its first
// placeholder (the name, which tells two identical parts apart) has no value:
// the caller then falls back to the display attribute and the identity.
func (t *Type) RenderDisplay(attr func(string) string) string {
	if t == nil || t.DisplayTemplate == "" {
		return ""
	}
	parts, tail, err := parseTemplate(t.DisplayTemplate)
	if err != nil {
		return ""
	}
	var b strings.Builder
	sep, skipping := "", false
	for i, p := range parts {
		v := strings.TrimSpace(attr(p.attr))
		if v != "" {
			if f := formatters[p.formatter]; f != nil {
				v = f(v)
			}
		}
		if v == "" {
			if i == 0 {
				return ""
			}
			if !skipping {
				sep, skipping = p.lit, true
			}
			continue
		}
		if !skipping {
			sep = p.lit
		}
		skipping = false
		if b.Len() > 0 || i == 0 {
			b.WriteString(sep)
		}
		b.WriteString(v)
	}
	if b.Len() == 0 {
		return ""
	}
	b.WriteString(tail)
	return strings.TrimSpace(b.String())
}

var (
	trademarkRe = regexp.MustCompile(`(?i)\((r|tm)\)`)
	cpuWordRe   = regexp.MustCompile(`(?i)\bCPU\b`)
	spacesRe    = regexp.MustCompile(`\s+`)
)

// tidyCPU drops the registered and trademark signs and the word CPU from a
// processor model and collapses the spaces.
func tidyCPU(s string) string {
	s = trademarkRe.ReplaceAllString(s, "")
	s = cpuWordRe.ReplaceAllString(s, "")
	return strings.TrimSpace(spacesRe.ReplaceAllString(s, " "))
}

// humanBytes writes a size with GB-style units. base 1000 gives decimal units
// (a disk is sold that way), base 1024 gives binary units written as GB, TB (a
// DIMM is sold that way). A value that is not a positive number renders as "".
func humanBytes(s string, base float64) string {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return ""
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for v >= base*0.9995 && i < len(units)-1 {
		v /= base
		i++
	}
	return trimNumber(v) + " " + units[i]
}

func trimNumber(v float64) string {
	if v >= 100 {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0")
}

func watts(s string) string {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 {
		return ""
	}
	return trimNumber(v) + " W"
}
