package kubemetrics

import (
	"fmt"
	"regexp"
	"strings"
)

// matcher is one label matcher of a selector: name, operator and value.
type matcher struct {
	name, op, value string
	re              *regexp.Regexp // for =~ and !~, anchored as PromQL does
}

// matcherRe is one matcher: a label name, an operator and a double-quoted
// value. Nothing else is accepted, so that a selector can only narrow a query
// and never change it (no function, no offset, no second metric).
var matcherRe = regexp.MustCompile(`^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(=~|!~|!=|=)\s*"((?:[^"\\]|\\.)*)"\s*`)

// parseMatchers parses a comma-separated list of label matchers, with or
// without the surrounding braces. It returns the canonical text (no braces)
// and the matchers.
func parseMatchers(s string) (string, []matcher, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		if !strings.HasSuffix(s, "}") {
			return "", nil, fmt.Errorf("selector %q: unbalanced braces", s)
		}
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if s == "" {
		return "", nil, nil
	}
	var out []matcher
	rest := s
	for {
		loc := matcherRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			return "", nil, fmt.Errorf("selector %q: expected label=\"value\" (operators =, !=, =~, !~), near %q", s, rest)
		}
		m := matcher{name: rest[loc[2]:loc[3]], op: rest[loc[4]:loc[5]], value: rest[loc[6]:loc[7]]}
		if m.name == "__name__" {
			return "", nil, fmt.Errorf("selector %q: __name__ cannot be matched: the collector chooses the metrics", s)
		}
		if m.op == "=~" || m.op == "!~" {
			re, err := regexp.Compile("^(?:" + unescape(m.value) + ")$")
			if err != nil {
				return "", nil, fmt.Errorf("selector %q: label %s: %w", s, m.name, err)
			}
			m.re = re
		}
		out = append(out, m)
		rest = rest[loc[1]:]
		if strings.TrimSpace(rest) == "" {
			break
		}
		if !strings.HasPrefix(rest, ",") {
			return "", nil, fmt.Errorf("selector %q: expected a comma near %q", s, rest)
		}
		rest = rest[1:]
	}
	return s, out, nil
}

// unescape undoes the backslash escapes of a PromQL double-quoted string.
func unescape(v string) string {
	r := strings.NewReplacer(`\\`, `\`, `\"`, `"`)
	return r.Replace(v)
}

// matches reports whether a series' labels satisfy every matcher (used by the
// fake, which stands for the server's evaluation of the selector).
func matches(ms []matcher, labels map[string]string) bool {
	for _, m := range ms {
		v := labels[m.name]
		var ok bool
		switch m.op {
		case "=":
			ok = v == unescape(m.value)
		case "!=":
			ok = v != unescape(m.value)
		case "=~":
			ok = m.re.MatchString(v)
		case "!~":
			ok = !m.re.MatchString(v)
		}
		if !ok {
			return false
		}
	}
	return true
}
