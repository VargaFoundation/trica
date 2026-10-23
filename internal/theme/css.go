package theme

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// The stylesheet of a theme is free CSS: the manifest's tokens are the
// contract, this is the escape hatch (and it is not a stable API: class names
// change). It is held to a strict profile all the same. The scanner below
// strips comments, tracks strings, refuses what loads or runs something, and
// rewrites every reference to a file of the archive to its served address.

// resolver maps the argument of a url(), as written in the stylesheet, to the
// address it is served at, or an error when it names nothing of the archive.
type resolver func(ref string) (string, error)

func isIdentStart(c byte) bool {
	return c == '-' || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdent(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// stripComments replaces each comment by a space (as a browser reads it: u/**/rl( is not url( ) and keeps strings.
func stripComments(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '"' || c == '\'':
			j, err := skipString(s, i)
			if err != nil {
				return "", err
			}
			b.WriteString(s[i:j])
			i = j
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return "", errors.New("unterminated comment")
			}
			b.WriteByte(' ')
			i += 2 + end + 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), nil
}

// skipString returns the index after the string that starts at s[i].
func skipString(s string, i int) (int, error) {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '\n':
			return 0, errors.New("unterminated string")
		case q:
			return j + 1, nil
		}
	}
	return 0, errors.New("unterminated string")
}

// stringBody returns the text of the string literal s[i:j] (quotes removed),
// refusing an escape: a reference that hides its characters is refused.
func stringBody(lit string) (string, error) {
	body := lit[1 : len(lit)-1]
	if strings.Contains(body, `\`) {
		return "", errors.New("a file name with an escape is not allowed")
	}
	return body, nil
}

// rewriteCSS scans src and returns it with every file reference rewritten by
// resolve. It returns the first violation.
func rewriteCSS(src string, resolve resolver) (string, error) {
	s, err := stripComments(src)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	depth := 0
	var imageDepths []int // paren depths at which an image-set( or image( opened
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"' || c == '\'':
			j, err := skipString(s, i)
			if err != nil {
				return "", err
			}
			lit := s[i:j]
			if len(imageDepths) > 0 { // a bare string inside image-set( is an image
				body, err := stringBody(lit)
				if err != nil {
					return "", err
				}
				to, err := resolve(body)
				if err != nil {
					return "", err
				}
				out.WriteString(`"` + to + `"`)
			} else {
				out.WriteString(lit)
			}
			i = j
		case c == '\\':
			return "", errors.New("a backslash escape outside a string is not allowed")
		case c == '(':
			depth++
			out.WriteByte(c)
			i++
		case c == ')':
			if n := len(imageDepths); n > 0 && imageDepths[n-1] == depth {
				imageDepths = imageDepths[:n-1]
			}
			depth--
			out.WriteByte(c)
			i++
		case c == '@':
			j := i + 1
			for j < len(s) && isIdent(s[j]) {
				j++
			}
			switch strings.ToLower(s[i+1 : j]) {
			case "import", "namespace":
				return "", fmt.Errorf("@%s is not allowed", strings.ToLower(s[i+1:j]))
			}
			out.WriteString(s[i:j])
			i = j
		case isIdentStart(c):
			j := i
			for j < len(s) && isIdent(s[j]) {
				j++
			}
			id := strings.ToLower(s[i:j])
			next := j
			for next < len(s) && (s[next] == ' ' || s[next] == '\t' || s[next] == '\n' || s[next] == '\r') {
				next++
			}
			isCall := j < len(s) && s[j] == '('
			isColon := next < len(s) && s[next] == ':'
			switch {
			case id == "-moz-binding":
				return "", errors.New("-moz-binding is not allowed")
			case id == "behavior" && isColon:
				return "", errors.New("behavior is not allowed")
			case id == "javascript" && isColon:
				return "", errors.New("a javascript: URL is not allowed")
			case id == "expression" && isCall:
				return "", errors.New("expression() is not allowed")
			case id == "url" && isCall:
				arg, end, err := urlArgument(s, j+1)
				if err != nil {
					return "", err
				}
				to, err := resolve(arg)
				if err != nil {
					return "", err
				}
				out.WriteString(`url("` + to + `")`)
				i = end
				continue
			case isCall && (id == "image" || strings.HasSuffix(id, "image-set")):
				imageDepths = append(imageDepths, depth+1)
			}
			out.WriteString(s[i:j])
			i = j
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), nil
}

// urlArgument parses what follows "url(" (s[i] is the first byte after the
// parenthesis) and returns the file reference and the index after the ")".
func urlArgument(s string, i int) (arg string, end int, err error) {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	if i >= len(s) {
		return "", 0, errors.New("unterminated url(")
	}
	if s[i] == '"' || s[i] == '\'' {
		j, err := skipString(s, i)
		if err != nil {
			return "", 0, err
		}
		if arg, err = stringBody(s[i:j]); err != nil {
			return "", 0, err
		}
		i = j
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
		if i >= len(s) || s[i] != ')' {
			return "", 0, errors.New("malformed url()")
		}
		return arg, i + 1, nil
	}
	j := strings.IndexByte(s[i:], ')')
	if j < 0 {
		return "", 0, errors.New("unterminated url(")
	}
	arg = strings.TrimSpace(s[i : i+j])
	if strings.ContainsAny(arg, "\\\"'( \t\r\n") {
		return "", 0, errors.New("malformed url()")
	}
	return arg, i + j + 1, nil
}

// fileResolver resolves a reference relative to the directory of the
// stylesheet that holds it: it must name a file of the archive, of a type that
// is served (not a stylesheet, not text), without a scheme, query or fragment.
// Every file it resolves is recorded in used.
func fileResolver(dir string, files map[string][]byte, sha string, used map[string]bool) resolver {
	return func(ref string) (string, error) {
		switch {
		case ref == "":
			return "", errors.New("an empty url() is not allowed")
		case strings.ContainsAny(ref, ":?#") || strings.HasPrefix(ref, "/"):
			return "", fmt.Errorf("url(%s) must name a file of the archive (no scheme, no absolute path, no query, no fragment)", ref)
		}
		p := path.Join(dir, ref)
		if err := checkName(p); err != nil || strings.HasPrefix(p, "../") {
			return "", fmt.Errorf("url(%s) leaves the archive", ref)
		}
		if _, ok := files[p]; !ok {
			return "", fmt.Errorf("url(%s): no such file in the archive", ref)
		}
		if !hasExt(p, []string{".svg", ".png", ".jpg", ".jpeg", ".webp", ".ico", ".woff2"}) {
			return "", fmt.Errorf("url(%s): a file of this type cannot be referenced", ref)
		}
		used[p] = true
		return servedPath(sha, p), nil
	}
}

// servedPath is where a file of a theme is served.
func servedPath(sha, file string) string { return "/theme/a/" + sha + "/" + file }
