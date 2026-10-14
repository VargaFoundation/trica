package mcp

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/tql"
)

// PageArgs are the pagination arguments of every list tool.
type PageArgs struct {
	Limit  int `json:"limit,omitempty" jsonschema:"page size, 1 to 1000 (default 50)"`
	Offset int `json:"offset,omitempty" jsonschema:"number of items to skip (default 0); continue with the summary's next_offset"`
}

func (p PageArgs) bounds() (limit, offset int, err error) { return api.PageBounds(p.Limit, p.Offset) }

// Summary is what every list answer says first: how many items match, of
// which types, and whether this page is all of them.
type Summary struct {
	// Total is the number of items that match (a lower bound when
	// TotalIsLowerBound).
	Total  int            `json:"total"`
	ByType map[string]int `json:"by_type,omitempty"`
	// Returned is the number of items in this page.
	Returned int `json:"returned"`
	// Truncated: more items match than this page holds; read on from NextOffset.
	Truncated  bool `json:"truncated"`
	NextOffset *int `json:"next_offset,omitempty"`
	// TotalIsLowerBound: the server stopped reading at its scan bound.
	TotalIsLowerBound bool `json:"total_is_lower_bound,omitempty"`
}

func newSummary(total, offset, returned int, byType map[string]int) Summary {
	s := Summary{Total: total, ByType: byType, Returned: returned, Truncated: offset+returned < total}
	if s.Truncated {
		next := offset + returned
		s.NextOffset = &next
	}
	return s
}

// Answer is the envelope of a list: the summary first, then the page.
type Answer struct {
	Summary any `json:"summary"`
	Limit   int `json:"limit"`
	Offset  int `json:"offset"`
	Items   any `json:"items"`
}

// ObjectItem is an object as list tools show it.
type ObjectItem struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Display    string         `json:"display"`
	Status     string         `json:"status"`
	ParentID   *string        `json:"parent_id,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// objectItems projects objects for a list. fields, when not empty, keeps only
// those attributes; sensitive attributes are never returned either way.
func objectItems(objs []*store.Object, fields []string) []ObjectItem {
	out := make([]ObjectItem, 0, len(objs))
	for _, o := range objs {
		attrs := api.RedactMap(o.Attrs)
		if len(fields) > 0 {
			kept := make(map[string]any, len(fields))
			for _, f := range fields {
				if v, ok := attrs[f]; ok {
					kept[f] = v
				}
			}
			attrs = kept
		}
		out = append(out, ObjectItem{ID: o.ID, Type: o.Type, Display: o.Display, Status: o.Status, ParentID: o.ParentID, Attributes: attrs})
	}
	return out
}

// runQuery runs a query the guard has already restricted and answers with the summary first.
func (t *tc) runQuery(q *tql.Query, label string, pa PageArgs, fields []string) (*Answer, *search.Result, error) {
	limit, offset, err := pa.bounds()
	if err != nil {
		return nil, nil, err
	}
	res, err := t.s.searcher.SearchQuery(t.ctx, q, label, search.Page{Limit: limit, Offset: offset})
	if err != nil {
		return nil, nil, err
	}
	sum := struct {
		Query string `json:"query"`
		Summary
		Fuzzy  bool  `json:"fuzzy,omitempty"`
		TookMS int64 `json:"took_ms"`
	}{Query: res.Query, Summary: newSummary(res.Total, res.Offset, len(res.Items), res.ByType), Fuzzy: res.Fuzzy, TookMS: res.Took.Milliseconds()}
	return &Answer{Summary: sum, Limit: res.Limit, Offset: res.Offset, Items: objectItems(res.Items, fields)}, res, nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolve finds the object a tool argument designates: an id, or a name,
// address or serial. "device/port" designates the port named "port" whose
// parent is named "device". The search is the caller's own (readable types
// only, exact matches only); a name that matches several objects is refused
// with the candidates, never guessed.
func (t *tc) resolve(ref string) (*store.Object, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, api.BadRequest("an object is required: an id, a name, an address or a serial")
	}
	if uuidRe.MatchString(ref) {
		o, _, err := t.g.LoadObject(strings.ToLower(ref))
		return o, err
	}
	var where tql.Node = tql.Text{Value: ref}
	if parent, leaf, ok := strings.Cut(ref, "/"); ok && parent != "" && leaf != "" {
		where = tql.And{Terms: []tql.Node{tql.Text{Value: leaf}, tql.Pred{Field: "parent", Op: tql.OpEq, Values: []string{parent}}}}
	}
	q := &tql.Query{Where: where}
	if err := t.g.Restrict(q, ""); err != nil {
		return nil, err
	}
	res, err := t.s.searcher.SearchQuery(t.ctx, q, ref, search.Page{Limit: 10})
	if err != nil {
		return nil, err
	}
	switch {
	case res.Total == 0 || res.Fuzzy:
		return nil, api.NotFound("no object matches %q (use search to find it)", ref)
	case len(res.Items) == 1:
		return res.Items[0], nil
	}
	leaf := ref
	if _, l, ok := strings.Cut(ref, "/"); ok {
		leaf = l
	}
	var exact []*store.Object
	for _, o := range res.Items {
		if strings.EqualFold(o.Display, leaf) {
			exact = append(exact, o)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	cands := make([]string, 0, len(res.Items))
	for _, o := range res.Items {
		cands = append(cands, fmt.Sprintf("%s %q (%s)", o.Type, o.Display, o.ID))
	}
	return nil, api.BadRequest("%q is ambiguous (%d objects match, first %d: %s); pass an id or narrow it with parent/name", ref, res.Total, len(cands), strings.Join(cands, "; "))
}

// resolveAll resolves several arguments.
func (t *tc) resolveAll(refs []string) ([]*store.Object, error) {
	out := make([]*store.Object, 0, len(refs))
	for _, r := range refs {
		o, err := t.resolve(r)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

var relativeRe = regexp.MustCompile(`^-?([0-9]{1,5})([mhdw])$`)

// parseWhen reads a time argument: RFC 3339, a date (2026-03-03), or a time
// relative to now ("30m", "24h", "-7d", "2w").
func parseWhen(v string, now time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	if m := relativeRe.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return now.Add(-time.Duration(n) * unit).UTC(), nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, api.BadRequest("a time must be RFC 3339 (2026-03-03T10:00:00Z), a date (2026-03-03) or relative to now (30m, 24h, 7d, 2w): got %q", v)
}
