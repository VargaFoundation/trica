package theme

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// An SVG of a theme is drawn on the page as a CSS background or an icon, where
// a browser runs no script in it, and it is also served on its own under a
// sandboxing policy. It is nevertheless held to a strict profile: a theme
// archive is content from outside, and an SVG is the file type most often
// used to smuggle script into it.

var forbiddenElements = map[string]bool{
	"script": true, "foreignobject": true, "iframe": true, "embed": true, "object": true,
	"set": true, "animate": true, "handler": true, "listener": true,
}

var reComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// checkInlineStyle refuses the parts of a style (an attribute or an element's
// text) that load something or run something: only a reference to the same
// document, url(#id), is allowed.
func checkInlineStyle(style string) error {
	s := strings.ToLower(reComment.ReplaceAllString(style, " "))
	switch {
	case strings.Contains(s, `\`):
		return errors.New("style holds a backslash escape")
	case strings.Contains(s, "@import"), strings.Contains(s, "expression("), strings.Contains(s, "javascript:"),
		strings.Contains(s, "-moz-binding"):
		return errors.New("style holds a construct that loads or runs something")
	}
	for rest := s; ; {
		i := strings.Index(rest, "url(")
		if i < 0 {
			return nil
		}
		rest = rest[i+len("url("):]
		arg := strings.TrimLeft(rest, " \t\r\n\"'")
		if !strings.HasPrefix(arg, "#") {
			return errors.New("style holds a url() that is not a reference to the same document (#id)")
		}
	}
}

// checkSVG validates an SVG file.
func checkSVG(b []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = true
	dec.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		if l := strings.ToLower(label); l == "utf-8" || l == "utf8" {
			return in, nil
		}
		return nil, fmt.Errorf("encoding %q is not supported (use UTF-8)", label)
	}
	root := ""
	var inStyle bool
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("not well-formed XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return errors.New("a DOCTYPE or entity declaration is not allowed")
		case xml.ProcInst:
			if t.Target != "xml" {
				return fmt.Errorf("the processing instruction <?%s?> is not allowed", t.Target)
			}
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if root == "" {
				root = name
			}
			if forbiddenElements[name] {
				return fmt.Errorf("the element <%s> is not allowed", t.Name.Local)
			}
			inStyle = name == "style"
			for _, a := range t.Attr {
				an := strings.ToLower(a.Name.Local)
				av := strings.TrimSpace(a.Value)
				switch {
				case strings.HasPrefix(an, "on"):
					return fmt.Errorf("the event attribute %q is not allowed", a.Name.Local)
				case an == "href" && !strings.HasPrefix(av, "#"):
					return fmt.Errorf("href %q is not a reference to the same document (#id)", av)
				case an == "style":
					if err := checkInlineStyle(av); err != nil {
						return err
					}
				case strings.Contains(strings.ToLower(av), "javascript:"):
					return fmt.Errorf("attribute %q holds a javascript: URL", a.Name.Local)
				}
			}
		case xml.EndElement:
			inStyle = false
		case xml.CharData:
			if inStyle {
				if err := checkInlineStyle(string(t)); err != nil {
					return err
				}
			}
		}
	}
	if root != "svg" {
		return errors.New("the root element is not <svg>")
	}
	return nil
}
