// Package tql parses the trica query language, the structured search of the
// search bar, the API and the MCP tools (ADR 0012). It only builds a syntax
// tree; package search compiles it to SQL.
//
//	type:device role:switch os_version<4.20 -status:absent
//	type:disk wear_pct>70 rel.mounted_in:"rack 2" sort:-wear_pct limit:10
//	(vendor:arista OR vendor:mikrotik) NOT has:serial
//	address<<192.0.2.0/24
//	sw-a1
//
// Terms are joined by AND unless OR stands between them; NOT or a leading
// "-" negates; parentheses group. A term that is not field-op-value is free
// text. sort: and limit: are directives and only appear at the top level.
package tql

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Comparison operators.
const (
	OpEq       = ":"  // equals (case-insensitive, * wildcards, list membership)
	OpEqStrict = "="  // same as ":"
	OpNe       = "!=" // differs, or is missing
	OpLt       = "<"
	OpLe       = "<="
	OpGt       = ">"
	OpGe       = ">="
	OpContains = "~"  // contains the value
	OpWithin   = "<<" // address or prefix within a prefix
)

// Node is a node of the syntax tree.
type Node interface{ node() }

// And holds terms that must all match.
type And struct{ Terms []Node }

// Or holds terms of which one must match.
type Or struct{ Terms []Node }

// Not negates a term.
type Not struct{ Term Node }

// Text is free text, matched against names and identifiers.
type Text struct{ Value string }

// Pred is field op value. Values has several entries for "a,b,c" (any of).
type Pred struct {
	Field  string
	Op     string
	Values []string
}

func (And) node()  {}
func (Or) node()   {}
func (Not) node()  {}
func (Text) node() {}
func (Pred) node() {}

// SortKey orders results by a field, descending when Desc.
type SortKey struct {
	Field string
	Desc  bool
}

// Query is a parsed query. Where is nil when the query has no condition.
type Query struct {
	Where Node
	Sort  []SortKey
	Limit int
}

// MaxLimit bounds limit:.
const MaxLimit = 1000

// Error is a syntax error at a byte offset of the query.
type Error struct {
	Pos int
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("tql: %s at offset %d", e.Msg, e.Pos) }

// Parse parses a query.
func Parse(s string) (*Query, error) {
	toks, err := lex(s)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, q: &Query{}}
	if len(toks) > 0 {
		w, err := p.or(true)
		if err != nil {
			return nil, err
		}
		if p.pos < len(toks) {
			return nil, &Error{toks[p.pos].pos, fmt.Sprintf("unexpected %q", toks[p.pos].raw)}
		}
		p.q.Where = w
	}
	return p.q, nil
}

// Fields returns every predicate field the query uses.
func (q *Query) Fields() []string {
	var out []string
	var walk func(Node)
	walk = func(n Node) {
		switch x := n.(type) {
		case And:
			for _, t := range x.Terms {
				walk(t)
			}
		case Or:
			for _, t := range x.Terms {
				walk(t)
			}
		case Not:
			walk(x.Term)
		case Pred:
			out = append(out, x.Field)
		}
	}
	if q.Where != nil {
		walk(q.Where)
	}
	return out
}

// Texts returns the free-text terms of the query, negated ones excluded.
func (q *Query) Texts() []string {
	var out []string
	var walk func(Node)
	walk = func(n Node) {
		switch x := n.(type) {
		case And:
			for _, t := range x.Terms {
				walk(t)
			}
		case Or:
			for _, t := range x.Terms {
				walk(t)
			}
		case Text:
			out = append(out, x.Value)
		}
	}
	if q.Where != nil {
		walk(q.Where)
	}
	return out
}

// ------------------------------------------------------------------ lexer

type tokKind int

const (
	tLParen tokKind = iota
	tRParen
	tAnd
	tOr
	tNot
	tTerm
)

type token struct {
	kind tokKind
	pos  int
	raw  string
	// For tTerm: a predicate when field != "", free text otherwise.
	field, op, value string
	quoted           bool
}

var predRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)?)(<<|<=|>=|!=|:|=|<|>|~)(.*)$`)

func lex(s string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{kind: tLParen, pos: i, raw: "("})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen, pos: i, raw: ")"})
			i++
		case c == '-' && i+1 < len(s) && !isSpace(s[i+1]) && s[i+1] != ')' && (i == 0 || isSpace(s[i-1]) || s[i-1] == '('):
			toks = append(toks, token{kind: tNot, pos: i, raw: "-"})
			i++
		case c == '"':
			v, n, err := quoted(s, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tTerm, pos: i, raw: s[i:n], value: v, quoted: true})
			i = n
		default:
			start := i
			for i < len(s) && !isSpace(s[i]) && s[i] != '(' && s[i] != ')' && s[i] != '"' {
				i++
			}
			word := s[start:i]
			switch word {
			case "AND":
				toks = append(toks, token{kind: tAnd, pos: start, raw: word})
				continue
			case "OR":
				toks = append(toks, token{kind: tOr, pos: start, raw: word})
				continue
			case "NOT":
				toks = append(toks, token{kind: tNot, pos: start, raw: word})
				continue
			}
			t := token{kind: tTerm, pos: start, raw: word, value: word}
			if m := predRE.FindStringSubmatch(word); m != nil && !isAddress(word) && !strings.HasPrefix(m[3], "//") {
				t.field, t.op, t.value = strings.ToLower(m[1]), m[2], m[3]
				if t.value == "" && i < len(s) && s[i] == '"' {
					v, n, err := quoted(s, i)
					if err != nil {
						return nil, err
					}
					t.value, t.quoted, t.raw = v, true, s[start:n]
					i = n
				}
				if t.value == "" {
					return nil, &Error{start, fmt.Sprintf("%q has no value", word)}
				}
			}
			toks = append(toks, t)
		}
	}
	return toks, nil
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// isAddress keeps MACs and IPv6 addresses, full of colons, as free text.
func isAddress(w string) bool {
	if _, err := net.ParseMAC(w); err == nil {
		return true
	}
	if a, err := netip.ParseAddr(w); err == nil && a.Is6() {
		return true
	}
	if p, err := netip.ParsePrefix(w); err == nil && p.Addr().Is6() {
		return true
	}
	return false
}

// quoted reads a double-quoted string starting at s[i]; \" and \\ escape.
func quoted(s string, i int) (string, int, error) {
	var b strings.Builder
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if j+1 < len(s) {
				j++
				b.WriteByte(s[j])
			}
		case '"':
			return b.String(), j + 1, nil
		default:
			b.WriteByte(s[j])
		}
	}
	return "", 0, &Error{i, "unterminated quote"}
}

// ----------------------------------------------------------------- parser

type parser struct {
	toks []token
	pos  int
	q    *Query
}

func (p *parser) peek() *token {
	if p.pos < len(p.toks) {
		return &p.toks[p.pos]
	}
	return nil
}

// or parses and-groups separated by OR. top is true outside parentheses,
// NOT and OR, where directives are allowed.
func (p *parser) or(top bool) (Node, error) {
	start := p.pos
	first, err := p.and(top)
	if err != nil {
		return nil, err
	}
	terms := []Node{first}
	for t := p.peek(); t != nil && t.kind == tOr; t = p.peek() {
		p.pos++
		next, err := p.and(false)
		if err != nil {
			return nil, err
		}
		terms = append(terms, next)
	}
	if len(terms) == 1 {
		return first, nil
	}
	for _, t := range terms {
		if t == nil {
			return nil, &Error{p.toks[start].pos, "OR needs a condition on each side"}
		}
	}
	return Or{Terms: terms}, nil
}

// and parses terms up to OR, ")" or the end. It may return nil when the
// group held only directives.
func (p *parser) and(top bool) (Node, error) {
	var terms []Node
	for {
		t := p.peek()
		if t == nil || t.kind == tOr || t.kind == tRParen {
			break
		}
		if t.kind == tAnd {
			if len(terms) == 0 {
				return nil, &Error{t.pos, "AND needs a condition before it"}
			}
			p.pos++
			if n := p.peek(); n == nil || n.kind == tOr || n.kind == tRParen || n.kind == tAnd {
				return nil, &Error{t.pos, "AND needs a condition after it"}
			}
			continue
		}
		n, err := p.unary(top && p.orFree())
		if err != nil {
			return nil, err
		}
		if n != nil {
			terms = append(terms, n)
		}
	}
	switch len(terms) {
	case 0:
		if t := p.peek(); t != nil && t.kind == tOr {
			return nil, &Error{t.pos, "OR needs a condition on each side"}
		}
		return nil, nil
	case 1:
		return terms[0], nil
	}
	return And{Terms: terms}, nil
}

// orFree reports whether no OR appears at this nesting level, so that a
// directive found here applies to the whole query.
func (p *parser) orFree() bool {
	depth := 0
	for _, t := range p.toks {
		switch t.kind {
		case tLParen:
			depth++
		case tRParen:
			depth--
		case tOr:
			if depth == 0 {
				return false
			}
		}
	}
	return true
}

func (p *parser) unary(top bool) (Node, error) {
	t := p.peek()
	switch t.kind {
	case tNot:
		p.pos++
		if n := p.peek(); n == nil || n.kind == tRParen || n.kind == tOr || n.kind == tAnd {
			return nil, &Error{t.pos, "NOT needs a condition after it"}
		}
		inner, err := p.unary(false)
		if err != nil {
			return nil, err
		}
		return Not{Term: inner}, nil
	case tLParen:
		p.pos++
		if n := p.peek(); n != nil && n.kind == tRParen {
			return nil, &Error{t.pos, "empty parentheses"}
		}
		inner, err := p.or(false)
		if err != nil {
			return nil, err
		}
		if c := p.peek(); c == nil || c.kind != tRParen {
			return nil, &Error{t.pos, "unclosed parenthesis"}
		}
		p.pos++
		return inner, nil
	case tRParen:
		return nil, &Error{t.pos, "unexpected )"}
	}
	p.pos++
	if t.field == "" {
		return Text{Value: t.value}, nil
	}
	switch t.field {
	case "sort", "limit":
		if !top {
			return nil, &Error{t.pos, t.field + ": only applies to the whole query, outside parentheses, NOT and OR"}
		}
		return nil, p.directive(t)
	}
	values := []string{t.value}
	if !t.quoted && (t.op == OpEq || t.op == OpEqStrict || t.op == OpNe) && strings.Contains(t.value, ",") {
		values = nil
		for _, v := range strings.Split(t.value, ",") {
			if v != "" {
				values = append(values, v)
			}
		}
	}
	op := t.op
	if op == OpEqStrict {
		op = OpEq
	}
	return Pred{Field: t.field, Op: op, Values: values}, nil
}

func (p *parser) directive(t *token) error {
	if t.op != OpEq && t.op != OpEqStrict {
		return &Error{t.pos, t.field + " takes ':'"}
	}
	switch t.field {
	case "limit":
		n, err := strconv.Atoi(t.value)
		if err != nil || n < 1 || n > MaxLimit {
			return &Error{t.pos, fmt.Sprintf("limit must be between 1 and %d", MaxLimit)}
		}
		p.q.Limit = n
	case "sort":
		for _, f := range strings.Split(t.value, ",") {
			k := SortKey{Field: strings.ToLower(f)}
			if strings.HasPrefix(k.Field, "-") {
				k.Field, k.Desc = k.Field[1:], true
			}
			if !fieldRE.MatchString(k.Field) {
				return &Error{t.pos, fmt.Sprintf("cannot sort on %q", f)}
			}
			p.q.Sort = append(p.q.Sort, k)
		}
	}
	return nil
}

var fieldRE = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
