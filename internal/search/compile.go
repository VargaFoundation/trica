// Package search answers the search bar, the API and the MCP search tools:
// free text over names and identifiers, tolerant to typos and partial input,
// and structured queries in TQL (ADR 0012), compiled to parameterised SQL.
package search

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/tql"
)

// Columns of objects a query can name directly; anything else is an attribute.
var timeColumns = map[string]bool{"first_seen": true, "last_seen": true, "created_at": true}

var attrRE = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// compiled is the SQL form of a query against "objects o".
type compiled struct {
	where string
	order string
	// args are the parameters of where then order; the first nWhere are
	// all that where uses.
	args   []any
	nWhere int
	limit  int
}

type compiler struct {
	args []any
	now  time.Time
	// fuzzy lets free text match by trigram similarity, for typos. The
	// searcher only turns it on when the exact pass finds nothing: similar
	// names are everywhere in an inventory (sw-a1, sw-a2), and a typo-tolerant
	// first pass would bury the exact hit among its neighbours.
	fuzzy bool
}

func (c *compiler) arg(v any) string {
	c.args = append(c.args, v)
	return fmt.Sprintf("$%d", len(c.args))
}

// compile turns a parsed query into SQL. Objects that are absent are left
// out unless the query says something about status.
func compile(q *tql.Query, now time.Time, fuzzy bool) (*compiled, error) {
	c := &compiler{now: now, fuzzy: fuzzy}
	where := "TRUE"
	if q.Where != nil {
		w, err := c.node(q.Where)
		if err != nil {
			return nil, err
		}
		where = w
	}
	mentionsStatus := false
	for _, f := range q.Fields() {
		if f == "status" {
			mentionsStatus = true
		}
	}
	if !mentionsStatus {
		where = "o.status = 'active' AND (" + where + ")"
	}
	nWhere := len(c.args)
	order, err := c.order(q)
	if err != nil {
		return nil, err
	}
	return &compiled{where: where, order: order, args: c.args, nWhere: nWhere, limit: q.Limit}, nil
}

func (c *compiler) node(n tql.Node) (string, error) {
	switch x := n.(type) {
	case tql.And:
		return c.join(x.Terms, " AND ")
	case tql.Or:
		return c.join(x.Terms, " OR ")
	case tql.Not:
		s, err := c.node(x.Term)
		if err != nil {
			return "", err
		}
		return "(" + s + ") IS NOT TRUE", nil
	case tql.Text:
		return c.text(x.Value), nil
	case tql.Pred:
		return c.pred(x)
	}
	return "", fmt.Errorf("search: unknown node %T", n)
}

func (c *compiler) join(terms []tql.Node, sep string) (string, error) {
	parts := make([]string, 0, len(terms))
	for _, t := range terms {
		s, err := c.node(t)
		if err != nil {
			return "", err
		}
		parts = append(parts, "("+s+")")
	}
	return strings.Join(parts, sep), nil
}

// text matches free text on the indexed search text and the display name,
// as a substring or, in the fuzzy pass, by trigram word similarity. A MAC in
// any format is also tried in its canonical form; an id matches its object.
func (c *compiler) text(v string) string {
	var parts []string
	for _, a := range forms(v) {
		like := c.arg("%" + escapeLike(a) + "%")
		parts = append(parts, fmt.Sprintf("o.search_text ILIKE %[1]s OR o.display ILIKE %[1]s", like))
	}
	p := c.arg(v)
	parts = append(parts, fmt.Sprintf("o.id::text = %s", p))
	if c.fuzzy {
		parts = append(parts, fmt.Sprintf("%[1]s <%% o.search_text OR similarity(%[2]s, %[3]s) >= 0.5",
			p, compact("o.display"), compact(p)))
	}
	return strings.Join(parts, " OR ")
}

func (c *compiler) pred(p tql.Pred) (string, error) {
	switch {
	case p.Field == "type" || p.Field == "status" || p.Field == "id":
		return c.column(p)
	case p.Field == "display":
		return c.compare(p, "o.display", "to_jsonb(o.display)")
	case p.Field == "parent":
		return c.parent(p)
	case p.Field == "has":
		return c.has(p)
	case p.Field == "source":
		return c.source(p)
	case strings.HasPrefix(p.Field, "rel."):
		return c.relation(p)
	case timeColumns[p.Field]:
		return c.timeColumn(p)
	}
	if !attrRE.MatchString(p.Field) {
		return "", fmt.Errorf("search: unknown field %q", p.Field)
	}
	k := c.arg(p.Field) + "::text"
	return c.compare(p, "o.attrs->>"+k, "o.attrs->"+k)
}

// column handles type, status and id: exact values, any of a list.
func (c *compiler) column(p tql.Pred) (string, error) {
	if p.Op != tql.OpEq && p.Op != tql.OpNe {
		return "", fmt.Errorf("search: %s only takes ':' or '!='", p.Field)
	}
	col := "o." + p.Field
	if p.Field == "id" {
		col = "o.id::text"
	}
	vals := make([]string, len(p.Values))
	for i, v := range p.Values {
		vals[i] = strings.ToLower(v)
	}
	s := fmt.Sprintf("%s = ANY(%s::text[])", col, c.arg(vals))
	if p.Op == tql.OpNe {
		s = "NOT (" + s + ")"
	}
	return s, nil
}

// compare applies an operator to a text expression and its JSON form.
func (c *compiler) compare(p tql.Pred, text, js string) (string, error) {
	switch p.Op {
	case tql.OpEq, tql.OpNe:
		var alts []string
		for _, v := range p.Values {
			for _, f := range forms(v) {
				alts = append(alts, c.match(text, js, likePattern(f, false)))
			}
		}
		s := strings.Join(alts, " OR ")
		if p.Op == tql.OpNe {
			s = "(" + s + ") IS NOT TRUE"
		}
		return s, nil
	case tql.OpContains:
		var alts []string
		for _, f := range forms(p.Values[0]) {
			alts = append(alts, c.match(text, js, likePattern(f, true)))
		}
		return strings.Join(alts, " OR "), nil
	case tql.OpWithin:
		if _, _, err := net.ParseCIDR(p.Values[0]); err != nil {
			if net.ParseIP(p.Values[0]) == nil {
				return "", fmt.Errorf("search: %s<<%s: not an address or a prefix", p.Field, p.Values[0])
			}
		}
		return fmt.Sprintf("trica_inet(%s) <<= %s::inet", text, c.arg(p.Values[0])), nil
	case tql.OpLt, tql.OpLe, tql.OpGt, tql.OpGe:
		return c.order3(p, text, js)
	}
	return "", fmt.Errorf("search: unknown operator %q", p.Op)
}

// match is a case-insensitive LIKE on a value, or on any element of a list.
func (c *compiler) match(text, js, pattern string) string {
	a := c.arg(pattern)
	return fmt.Sprintf("CASE jsonb_typeof(%[1]s) WHEN 'array' THEN EXISTS (SELECT 1 FROM jsonb_array_elements_text(%[1]s) e WHERE lower(e) LIKE %[2]s) ELSE lower(%[3]s) LIKE %[2]s END",
		js, a, text)
}

var versionRE = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+[A-Za-z0-9.-]*$`)

// order3 compares numbers as numbers, versions component by component and
// anything else (times included, stored as RFC 3339 UTC) as text.
func (c *compiler) order3(p tql.Pred, text, js string) (string, error) {
	v := p.Values[0]
	if t, ok := c.timeValue(v); ok {
		v = t.UTC().Format(time.RFC3339)
	}
	op := p.Op
	var alts []string
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		alts = append(alts, fmt.Sprintf("WHEN 'number' THEN (%s)::numeric %s %s::numeric", text, op, c.arg(v)))
	}
	if versionRE.MatchString(v) {
		alts = append(alts, fmt.Sprintf("WHEN 'string' THEN CASE WHEN %[1]s ~ '[0-9]' THEN trica_version_key(%[1]s) %[2]s trica_version_key(%[3]s) END", text, op, c.arg(v)))
	} else {
		alts = append(alts, fmt.Sprintf("WHEN 'string' THEN %s %s %s", text, op, c.arg(v)))
	}
	return fmt.Sprintf("CASE jsonb_typeof(%s) %s END", js, strings.Join(alts, " ")), nil
}

// timeValue reads an RFC 3339 time, a date, or a time relative to now:
// "-7d", "-24h", "-90m".
func (c *compiler) timeValue(v string) (time.Time, bool) {
	if strings.HasPrefix(v, "-") {
		s := v[1:]
		if strings.HasSuffix(s, "d") {
			if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil {
				return c.now.AddDate(0, 0, -n), true
			}
		}
		if d, err := time.ParseDuration(s); err == nil {
			return c.now.Add(-d), true
		}
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (c *compiler) timeColumn(p tql.Pred) (string, error) {
	t, ok := c.timeValue(p.Values[0])
	if !ok {
		return "", fmt.Errorf("search: %s: %q is not a time (2026-03-03, RFC 3339, or -7d, -24h)", p.Field, p.Values[0])
	}
	op := p.Op
	switch op {
	case tql.OpLt, tql.OpLe, tql.OpGt, tql.OpGe:
	default:
		return "", fmt.Errorf("search: %s only takes <, <=, > or >=", p.Field)
	}
	return fmt.Sprintf("o.%s %s %s", p.Field, op, c.arg(t)), nil
}

// parent matches the direct parent by id or display name.
func (c *compiler) parent(p tql.Pred) (string, error) {
	if p.Op != tql.OpEq {
		return "", fmt.Errorf("search: parent only takes ':'")
	}
	var alts []string
	for _, v := range p.Values {
		alts = append(alts, fmt.Sprintf("p.id::text = %s OR lower(p.display) LIKE %s", c.arg(v), c.arg(likePattern(v, false))))
	}
	return "EXISTS (SELECT 1 FROM objects p WHERE p.id = o.parent_id AND (" + strings.Join(alts, " OR ") + "))", nil
}

func (c *compiler) has(p tql.Pred) (string, error) {
	if p.Op != tql.OpEq {
		return "", fmt.Errorf("search: has only takes ':'")
	}
	for _, v := range p.Values {
		if !attrRE.MatchString(strings.ToLower(v)) {
			return "", fmt.Errorf("search: has:%s: not an attribute name", v)
		}
	}
	vals := make([]string, len(p.Values))
	for i, v := range p.Values {
		vals[i] = strings.ToLower(v)
	}
	return fmt.Sprintf("o.attrs ?| %s::text[]", c.arg(vals)), nil
}

// source matches objects some source currently makes a claim on: source:arista:*.
func (c *compiler) source(p tql.Pred) (string, error) {
	if p.Op != tql.OpEq && p.Op != tql.OpNe {
		return "", fmt.Errorf("search: source only takes ':' or '!='")
	}
	var alts []string
	for _, v := range p.Values {
		alts = append(alts, "cl.source LIKE "+c.arg(strings.ReplaceAll(escapeLike(v), "*", "%")))
	}
	s := "EXISTS (SELECT 1 FROM claims cl WHERE cl.object_id = o.id AND (" + strings.Join(alts, " OR ") + "))"
	if p.Op == tql.OpNe {
		s = "NOT " + s
	}
	return s, nil
}

// relation matches objects which, themselves or through a parent, have a
// current relation of the type with an object designated by id or display
// name ("*" for any). rel.mounted_in:"rack 2" finds the disks of the servers
// mounted in rack 2.
func (c *compiler) relation(p tql.Pred) (string, error) {
	typ := strings.TrimPrefix(p.Field, "rel.")
	if !attrRE.MatchString(typ) {
		return "", fmt.Errorf("search: %s: not a relation type", p.Field)
	}
	if p.Op != tql.OpEq && p.Op != tql.OpNe {
		return "", fmt.Errorf("search: %s only takes ':' or '!='", p.Field)
	}
	var alts []string
	for _, v := range p.Values {
		if v == "*" {
			alts = append(alts, "TRUE")
			continue
		}
		alts = append(alts, fmt.Sprintf("t.id::text = %s OR lower(t.display) LIKE %s", c.arg(v), c.arg(likePattern(v, false))))
	}
	// The set is built once, from the targets outwards: the objects at the
	// other end of a matching relation, then their descendants. A correlated
	// walk up from every row costs seconds on a few thousand servers.
	typArg, cond := c.arg(typ), strings.Join(alts, " OR ")
	s := fmt.Sprintf(`o.id IN (
		WITH RECURSIVE hit(id, depth) AS (
			SELECT r.from_id, 0 FROM relations r JOIN objects t ON t.id = r.to_id
			WHERE upper_inf(r.valid) AND r.type = %[1]s AND (%[2]s)
			UNION
			SELECT r.to_id, 0 FROM relations r JOIN objects t ON t.id = r.from_id
			WHERE upper_inf(r.valid) AND r.type = %[1]s AND (%[2]s)
			UNION
			SELECT c.id, hit.depth + 1 FROM objects c JOIN hit ON c.parent_id = hit.id
			WHERE hit.depth < 8
		)
		SELECT id FROM hit)`, typArg, cond)
	if p.Op == tql.OpNe {
		s = "NOT " + s
	}
	return s, nil
}

// order builds ORDER BY: the sort: keys, then relevance when the query has
// free text, then type and name for stable pages.
func (c *compiler) order(q *tql.Query) (string, error) {
	var keys []string
	for _, k := range q.Sort {
		dir := "ASC"
		if k.Desc {
			dir = "DESC"
		}
		switch {
		case k.Field == "id" || k.Field == "type" || k.Field == "display" || k.Field == "status" || timeColumns[k.Field]:
			keys = append(keys, fmt.Sprintf("o.%s %s NULLS LAST", k.Field, dir))
		case attrRE.MatchString(k.Field):
			a := c.arg(k.Field) + "::text"
			keys = append(keys,
				fmt.Sprintf("CASE WHEN jsonb_typeof(o.attrs->%[1]s) = 'number' THEN (o.attrs->>%[1]s)::numeric END %[2]s NULLS LAST", a, dir),
				fmt.Sprintf("o.attrs->>%s %s NULLS LAST", a, dir))
		default:
			return "", fmt.Errorf("search: cannot sort on %q", k.Field)
		}
	}
	if texts := q.Texts(); len(texts) > 0 {
		all := c.arg(strings.Join(texts, " "))
		keys = append(keys, fmt.Sprintf("(lower(o.display) = lower(%s)) DESC", all))
		if c.fuzzy {
			keys = append(keys, fmt.Sprintf("similarity(%s, %s) DESC", compact("o.display"), compact(all)))
		}
		keys = append(keys, fmt.Sprintf("word_similarity(%s, o.search_text) DESC", all))
	}
	keys = append(keys, "o.type", "o.display", "o.id")
	return strings.Join(keys, ", "), nil
}

// compact lowercases a name and drops its separators, so that a typo in
// the punctuation of a name (swa-1 for sw-a1) still compares close: trigrams
// split words on punctuation and would otherwise see "swa" and "1".
func compact(expr string) string {
	return fmt.Sprintf("regexp_replace(lower(%s), '[^[:alnum:]]+', '', 'g')", expr)
}

// forms returns v and, when it reads as a MAC in another format, the
// canonical form the store uses. Both are tried: a 12-digit hex serial number
// also reads as a MAC.
func forms(v string) []string {
	if m, err := schema.NormalizeMAC(v); err == nil && m != strings.ToLower(v) {
		return []string{v, m}
	}
	return []string{v}
}

// likePattern lowercases v for a case-insensitive LIKE, turning "*" into a
// wildcard; contains wraps it in wildcards.
func likePattern(v string, contains bool) string {
	p := strings.ReplaceAll(escapeLike(strings.ToLower(v)), "*", "%")
	if contains {
		p = "%" + p + "%"
	}
	return p
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
