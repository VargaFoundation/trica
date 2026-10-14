package mcp

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
)

const (
	descGet = `Read one object: its attributes (now, or as they were at a date), parent, site and status. ` +
		`The object is an id, a name ("sw-a1"), an address ("192.0.2.10"), a serial, or "device/port" for a port ("sw-a1/Ethernet2"). ` +
		`An ambiguous name is refused with the candidates. For provenance, relations and recent changes in one call use object_summary.`
	descSummary = `Everything about one object in one call: attributes, where each value comes from (source, confidence, first and last seen, other sources' claims), ` +
		`current relations (what it is cabled to, mounted in, connected to), its latest changes and its open discrepancies. Start here once search has found the object.`
	descHistory = `History of one object: how its attributes changed and which relations it had, with the periods during which each held. ` +
		`"What was plugged into sw-a1/Ethernet2 last month" is object_history{object:"sw-a1/Ethernet2", from:"2026-03-01", to:"2026-04-01", kind:"relations"}. ` +
		`Give attribute to follow one attribute only. Newest first, paginated.`
	descChanges = `What changed in the inventory since a date (RFC 3339, a date, or relative: 24h, 7d), and what is out of line with what was declared: ` +
		`attribute and relation changes with their source, plus the discrepancies opened in the period (observed state differing from the declared one). ` +
		`"What changed since yesterday that was not planned" is changes_since{since:"24h"}: the discrepancies are the unplanned ones.`
	descSchema = `The object types, their attributes and the relations of the inventory. Without type: a compact list (what each type is, its attribute names, how types relate). ` +
		`With type: that type in full (kinds, units, allowed values, identity keys). Use it to learn which attribute to filter or rank on.`
)

// ObjectInput designates one object.
type ObjectInput struct {
	Object string `json:"object" jsonschema:"an id, a name (sw-a1), an address (192.0.2.10), a serial, or device/port (sw-a1/Ethernet2)"`
	At     string `json:"at,omitempty" jsonschema:"read the object as it was then: RFC 3339, a date, or relative (7d)"`
}

func (s *Server) toolGet(t *tc, in ObjectInput) (any, error) {
	t.target = in.Object
	o, err := t.resolve(in.Object)
	if err != nil {
		return nil, err
	}
	t.target = o.ID
	var at *time.Time
	if in.At != "" {
		v, err := parseWhen(in.At, t.s.now())
		if err != nil {
			return nil, err
		}
		at = &v
	}
	return t.g.ObjectAt(o.ID, at)
}

// SummaryInput is the input of object_summary.
type SummaryInput struct {
	Object        string `json:"object" jsonschema:"an id, a name (sw-a1), an address (192.0.2.10), a serial, or device/port (sw-a1/Ethernet2)"`
	TimelineLimit int    `json:"timeline_limit,omitempty" jsonschema:"how many recent changes to include, 1 to 100 (default 10)"`
	RelationLimit int    `json:"relation_limit,omitempty" jsonschema:"how many relations to include, 1 to 200 (default 50)"`
}

// ObjectSummary is the one-call picture of an object.
type ObjectSummary struct {
	Summary       SummaryHead             `json:"summary"`
	Object        *api.ObjectView         `json:"object"`
	Provenance    []api.ProvenanceEntry   `json:"provenance"`
	Relations     []api.RelationView      `json:"relations"`
	Timeline      []store.Change          `json:"timeline"`
	Discrepancies []reconcile.Discrepancy `json:"discrepancies"`
}

// SummaryHead is what object_summary says first.
type SummaryHead struct {
	Object            string `json:"object"`
	Type              string `json:"type"`
	Status            string `json:"status"`
	Attributes        int    `json:"attributes"`
	Relations         int    `json:"relations"`
	RelationsReturned int    `json:"relations_returned"`
	Truncated         bool   `json:"truncated"`
	RecentChanges     int    `json:"recent_changes"`
	OpenDiscrepancies int    `json:"open_discrepancies"`
}

func clamp(v, def, hi int) (int, error) {
	if v == 0 {
		return def, nil
	}
	if v < 1 || v > hi {
		return 0, api.BadRequest("the limit must be an integer between 1 and %d", hi)
	}
	return v, nil
}

func (s *Server) toolSummary(t *tc, in SummaryInput) (any, error) {
	t.target = in.Object
	tl, err := clamp(in.TimelineLimit, 10, 100)
	if err != nil {
		return nil, err
	}
	rl, err := clamp(in.RelationLimit, 50, 200)
	if err != nil {
		return nil, err
	}
	o, err := t.resolve(in.Object)
	if err != nil {
		return nil, err
	}
	t.target = o.ID
	view, err := t.g.ObjectAt(o.ID, nil)
	if err != nil {
		return nil, err
	}
	prov, err := t.g.Provenance(o.ID)
	if err != nil {
		return nil, err
	}
	rels, err := s.st.RelationsOf(t.ctx, o.ID, nil)
	if err != nil {
		return nil, err
	}
	relViews, err := t.g.RelationViews(o.ID, rels)
	if err != nil {
		return nil, err
	}
	total := len(relViews)
	if len(relViews) > rl {
		relViews = relViews[:rl]
	}
	changes, err := s.st.Changes(t.ctx, time.Unix(0, 0).UTC(), t.s.now().UTC(), o.ID, tl)
	if err != nil {
		return nil, err
	}
	changes, err = t.g.FilterChanges(changes)
	if err != nil {
		return nil, err
	}
	disc, err := t.g.Discrepancies("open", "", o.ID, 10, 0)
	if err != nil {
		return nil, err
	}
	return &ObjectSummary{
		Summary: SummaryHead{Object: view.Display, Type: view.Type, Status: view.Status, Attributes: len(view.Attributes),
			Relations: total, RelationsReturned: len(relViews), Truncated: total > len(relViews),
			RecentChanges: len(changes), OpenDiscrepancies: disc.Total},
		Object: view, Provenance: prov.Attributes, Relations: relViews, Timeline: changes, Discrepancies: disc.Items,
	}, nil
}

// HistoryInput is the input of object_history.
type HistoryInput struct {
	Object    string `json:"object" jsonschema:"an id, a name, or device/port (sw-a1/Ethernet2)"`
	Attribute string `json:"attribute,omitempty" jsonschema:"follow this attribute only (implies kind values)"`
	Kind      string `json:"kind,omitempty" jsonschema:"all (default), values (attribute history) or relations (what it was connected to)"`
	From      string `json:"from,omitempty" jsonschema:"start of the window: RFC 3339, a date, or relative (30d); default: the beginning"`
	To        string `json:"to,omitempty" jsonschema:"end of the window (default: now)"`
	PageArgs
}

// HistoryEntry is one period during which an attribute had a value or a relation held.
type HistoryEntry struct {
	Kind       string         `json:"kind"` // value or relation
	Name       string         `json:"name"` // attribute key or relation type
	Value      any            `json:"value,omitempty"`
	Direction  string         `json:"direction,omitempty"`
	Other      *api.ObjectRef `json:"other,omitempty"`
	Source     string         `json:"source"`
	Confidence string         `json:"confidence"`
	ValidFrom  time.Time      `json:"valid_from"`
	ValidTo    *time.Time     `json:"valid_to,omitempty"`
}

// overlaps tells whether the validity [start, end) (end nil: still open) meets
// the window [from, to]: a value that became valid at the very end of the
// window is in it (the default end is now, and what happened now counts).
func overlaps(start time.Time, end *time.Time, from, to time.Time) bool {
	return !start.After(to) && (end == nil || end.After(from))
}

func (s *Server) toolHistory(t *tc, in HistoryInput) (any, error) {
	t.target = in.Object
	limit, offset, err := in.bounds()
	if err != nil {
		return nil, err
	}
	kind := strings.ToLower(in.Kind)
	switch kind {
	case "", "all":
		kind = "all"
		if in.Attribute != "" {
			kind = "values"
		}
	case "values", "relations":
		if kind == "relations" && in.Attribute != "" {
			return nil, api.BadRequest("attribute applies to values, not to relations")
		}
	default:
		return nil, api.BadRequest("kind must be all, values or relations")
	}
	if in.Attribute != "" {
		if !fieldRe.MatchString(in.Attribute) {
			return nil, api.BadRequest("attribute %q is not an attribute name", in.Attribute)
		}
		if api.Sensitive(in.Attribute) {
			return nil, api.Forbidden("the attribute %q is not exposed", in.Attribute)
		}
	}
	now := t.s.now().UTC()
	from, to := time.Unix(0, 0).UTC(), now
	if in.From != "" {
		if from, err = parseWhen(in.From, now); err != nil {
			return nil, err
		}
	}
	if in.To != "" {
		if to, err = parseWhen(in.To, now); err != nil {
			return nil, err
		}
	}
	if to.Before(from) {
		return nil, api.BadRequest("to must not be before from")
	}
	o, err := t.resolve(in.Object)
	if err != nil {
		return nil, err
	}
	t.target = o.ID
	if _, _, err := t.g.LoadObject(o.ID); err != nil {
		return nil, err
	}
	var entries []HistoryEntry
	truncated := false
	if kind != "relations" {
		vals, err := s.st.History(t.ctx, o.ID, in.Attribute, api.MaxScan)
		if err != nil {
			return nil, err
		}
		truncated = len(vals) >= api.MaxScan
		for _, v := range api.RedactValues(vals) {
			if overlaps(v.ValidFrom, v.ValidTo, from, to) {
				entries = append(entries, HistoryEntry{Kind: "value", Name: v.Key, Value: v.Value, Source: v.Source,
					Confidence: v.Confidence, ValidFrom: v.ValidFrom, ValidTo: v.ValidTo})
			}
		}
	}
	if kind != "values" {
		rels, err := s.st.RelationHistory(t.ctx, o.ID, api.MaxScan)
		if err != nil {
			return nil, err
		}
		truncated = truncated || len(rels) >= api.MaxScan
		inWindow := rels[:0:0]
		for _, r := range rels {
			if overlaps(r.ValidFrom, r.ValidTo, from, to) {
				inWindow = append(inWindow, r)
			}
		}
		views, err := t.g.RelationViews(o.ID, inWindow)
		if err != nil {
			return nil, err
		}
		for _, v := range views {
			other := v.Other
			entries = append(entries, HistoryEntry{Kind: "relation", Name: v.Type, Direction: v.Direction, Other: &other,
				Source: v.Source, Confidence: v.Confidence, ValidFrom: v.ValidFrom, ValidTo: v.ValidTo})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ValidFrom.After(entries[j].ValidFrom) })
	byKind := map[string]int{}
	for _, e := range entries {
		byKind[e.Kind]++
	}
	page := api.PageOf(entries, limit, offset, truncated)
	sum := struct {
		Object string `json:"object"`
		From   string `json:"from"`
		To     string `json:"to"`
		Summary
		ByKind map[string]int `json:"by_kind"`
	}{Object: o.Display, From: from.Format(time.RFC3339), To: to.Format(time.RFC3339),
		Summary: newSummary(page.Total, offset, len(page.Items), nil), ByKind: byKind}
	sum.TotalIsLowerBound = truncated
	return &Answer{Summary: sum, Limit: limit, Offset: offset, Items: page.Items}, nil
}

// ChangesInput is the input of changes_since.
type ChangesInput struct {
	Since  string   `json:"since" jsonschema:"start of the period: RFC 3339, a date, or relative (24h, 7d)"`
	Until  string   `json:"until,omitempty" jsonschema:"end of the period (default: now)"`
	Object string   `json:"object,omitempty" jsonschema:"only this object (id, name, address)"`
	Types  []string `json:"types,omitempty" jsonschema:"only changes on these object types"`
	Only   string   `json:"only,omitempty" jsonschema:"both (default), changes or discrepancies"`
	PageArgs
}

// ChangesAnswer is the answer of changes_since: a summary, then the changes
// and the discrepancies opened in the period, each paginated.
type ChangesAnswer struct {
	Summary       ChangesSummary `json:"summary"`
	Limit         int            `json:"limit"`
	Offset        int            `json:"offset"`
	Changes       any            `json:"changes"`
	Discrepancies any            `json:"discrepancies"`
}

// ChangesSummary says first what the period holds.
type ChangesSummary struct {
	Since   string  `json:"since"`
	Until   string  `json:"until"`
	Changes Summary `json:"changes"`
	// BySource counts the changes per source.
	BySource      map[string]int `json:"by_source,omitempty"`
	Discrepancies Summary        `json:"discrepancies"`
	// DiscrepanciesByKind counts the discrepancies opened in the period per kind.
	DiscrepanciesByKind map[string]int `json:"discrepancies_by_kind,omitempty"`
}

func (s *Server) toolChanges(t *tc, in ChangesInput) (any, error) {
	t.target = in.Object
	limit, offset, err := in.bounds()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Since) == "" {
		return nil, api.BadRequest("since is required: RFC 3339, a date, or relative (24h, 7d)")
	}
	only := strings.ToLower(in.Only)
	if !slices.Contains([]string{"", "both", "changes", "discrepancies"}, only) {
		return nil, api.BadRequest("only must be both, changes or discrepancies")
	}
	now := t.s.now().UTC()
	since, err := parseWhen(in.Since, now)
	if err != nil {
		return nil, err
	}
	until := now
	if in.Until != "" {
		if until, err = parseWhen(in.Until, now); err != nil {
			return nil, err
		}
	}
	if until.Before(since) {
		return nil, api.BadRequest("until must not be before since")
	}
	object := ""
	if in.Object != "" {
		o, err := t.resolve(in.Object)
		if err != nil {
			return nil, err
		}
		object = o.ID
		t.target = o.ID
	}
	ans := &ChangesAnswer{Limit: limit, Offset: offset, Changes: []store.Change{}, Discrepancies: []reconcile.Discrepancy{},
		Summary: ChangesSummary{Since: since.Format(time.RFC3339), Until: until.Format(time.RFC3339)}}
	if only != "discrepancies" {
		changes, err := s.st.Changes(t.ctx, since, until, object, api.MaxScan)
		if err != nil {
			return nil, err
		}
		changes, err = t.g.FilterChanges(changes)
		if err != nil {
			return nil, err
		}
		if len(in.Types) > 0 {
			changes = slices.DeleteFunc(changes, func(c store.Change) bool { return !slices.Contains(in.Types, c.Type) })
		}
		byType, bySource := map[string]int{}, map[string]int{}
		for _, c := range changes {
			byType[c.Type]++
			bySource[c.Source]++
		}
		page := api.PageOf(changes, limit, offset, false)
		ans.Summary.Changes = newSummary(page.Total, offset, len(page.Items), byType)
		ans.Summary.Changes.TotalIsLowerBound = len(changes) >= api.MaxScan
		ans.Summary.BySource = bySource
		ans.Changes = page.Items
	}
	if only != "changes" {
		var opened []reconcile.Discrepancy
		complete := false
		for off := 0; off < api.MaxScan && !complete; off += api.MaxLimit {
			pg, err := t.g.Discrepancies("open", "", object, api.MaxLimit, off)
			if err != nil {
				return nil, err
			}
			for _, d := range pg.Items {
				if !d.OpenedAt.Before(since) && !d.OpenedAt.After(until) {
					opened = append(opened, d)
				}
			}
			complete = off+api.MaxLimit >= pg.Total && !pg.Truncated
		}
		byKind := map[string]int{}
		for _, d := range opened {
			byKind[d.Kind]++
		}
		page := api.PageOf(opened, limit, offset, false)
		ans.Summary.Discrepancies = newSummary(page.Total, offset, len(page.Items), nil)
		ans.Summary.Discrepancies.TotalIsLowerBound = !complete
		ans.Summary.DiscrepanciesByKind = byKind
		ans.Discrepancies = page.Items
	}
	return ans, nil
}

// SchemaInput is the input of describe_schema.
type SchemaInput struct {
	Type string `json:"type,omitempty" jsonschema:"one object type to describe in full (default: the compact list of all of them)"`
}

// TypeBrief is a type in the compact schema list.
type TypeBrief struct {
	Name        string   `json:"name"`
	Layer       string   `json:"layer"`
	Description string   `json:"description"`
	Parents     []string `json:"parents,omitempty"`
	Attributes  []string `json:"attributes"`
}

// RelationBrief is a relation type in the compact schema list.
type RelationBrief struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	From        []string `json:"from"`
	To          []string `json:"to"`
	Impact      string   `json:"impact,omitempty"`
}

func (s *Server) toolSchema(t *tc, in SchemaInput) (any, error) {
	t.target = in.Type
	doc := s.api.SchemaDoc()
	if in.Type != "" {
		for _, td := range doc.Types {
			if td.Name != in.Type {
				continue
			}
			var rels []api.RelationDoc
			for _, r := range doc.Relations {
				if slices.Contains(r.From, in.Type) || slices.Contains(r.To, in.Type) {
					rels = append(rels, r)
				}
			}
			return struct {
				Type      api.TypeDoc       `json:"type"`
				Relations []api.RelationDoc `json:"relations"`
			}{td, rels}, nil
		}
		return nil, api.NotFound("no object type %q (describe_schema without type lists them)", in.Type)
	}
	types := make([]TypeBrief, 0, len(doc.Types))
	for _, td := range doc.Types {
		names := make([]string, 0, len(td.Attributes))
		for _, a := range td.Attributes {
			names = append(names, a.Name)
		}
		types = append(types, TypeBrief{Name: td.Name, Layer: td.Layer, Description: td.Description, Parents: td.Parents, Attributes: names})
	}
	rels := make([]RelationBrief, 0, len(doc.Relations))
	for _, r := range doc.Relations {
		rels = append(rels, RelationBrief{Name: r.Name, Description: r.Description, From: r.From, To: r.To, Impact: r.Impact})
	}
	return struct {
		Summary   map[string]int  `json:"summary"`
		Types     []TypeBrief     `json:"types"`
		Relations []RelationBrief `json:"relations"`
	}{map[string]int{"types": len(types), "relations": len(rels)}, types, rels}, nil
}
