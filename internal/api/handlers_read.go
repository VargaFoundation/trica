package api

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/tql"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// ------------------------------------------------------------------ schema

func (s *Server) hSchema(c *call) error {
	reg := s.registry()
	doc := SchemaDoc{Types: []TypeDoc{}, Relations: []RelationDoc{}}
	for _, n := range reg.TypeNames() {
		doc.Types = append(doc.Types, typeDoc(reg.Types[n]))
	}
	for _, n := range sdk.SortedKeys(reg.Relations) {
		doc.Relations = append(doc.Relations, relationDoc(reg.Relations[n]))
	}
	return c.json(200, doc)
}

func (s *Server) hSchemaType(c *call) error {
	name := c.r.PathValue("type")
	t, ok := s.registry().Types[name]
	if !ok {
		return notFound("no object type %q", name)
	}
	return c.json(200, typeDoc(t))
}

// ----------------------------------------------------------------- objects

// searchQuery builds the query to run: the caller's TQL, the sort parameter,
// and a restriction to the types the caller may read. The restriction is added
// to the syntax tree, never spliced into text.
func (c *call) searchQuery(q, sortParam, typeParam string, requireQ bool) (*tql.Query, error) {
	q = strings.TrimSpace(q)
	if requireQ && q == "" {
		return nil, badRequest("q is required")
	}
	parsed, err := tql.Parse(q)
	if err != nil {
		return nil, err
	}
	for _, f := range parsed.Fields() {
		if Sensitive(f) {
			return nil, newErr(403, "sensitive-attribute", "the attribute %q is not exposed and cannot be queried", f)
		}
	}
	if hasSensitiveHas(parsed.Where) {
		return nil, newErr(403, "sensitive-attribute", "a queried attribute is not exposed")
	}
	if sortParam != "" {
		if len(parsed.Sort) > 0 {
			return nil, badRequest("the sort parameter and a sort: directive in q cannot be combined")
		}
		for _, part := range strings.Split(sortParam, ",") {
			desc := strings.HasPrefix(part, "-")
			f := strings.TrimPrefix(part, "-")
			if !sortFieldRe.MatchString(f) {
				return nil, badRequest("sort: %q is not a field name", part)
			}
			if Sensitive(f) {
				return nil, newErr(403, "sensitive-attribute", "the attribute %q is not exposed and cannot be queried", f)
			}
			parsed.Sort = append(parsed.Sort, tql.SortKey{Field: f, Desc: desc})
		}
	}

	var types []string
	switch {
	case typeParam != "":
		if _, ok := c.s.registry().Types[typeParam]; !ok {
			return nil, badRequest("unknown object type %q", typeParam)
		}
		if !c.s.auth.Allow(c.p, ActionRead, typeParam, "") {
			return nil, forbidden("reading %s objects across all sites is not granted", typeParam)
		}
		types = []string{typeParam}
	default:
		allowed, all := c.readableTypes()
		if len(allowed) == 0 {
			return nil, forbidden("no object type is readable across all sites; read objects by id")
		}
		if !all {
			types = allowed
		}
	}
	if types != nil {
		pred := tql.Pred{Field: "type", Op: tql.OpEq, Values: types}
		if parsed.Where == nil {
			parsed.Where = pred
		} else {
			parsed.Where = tql.And{Terms: []tql.Node{pred, parsed.Where}}
		}
	}
	return parsed, nil
}

func hasSensitiveHas(n tql.Node) bool {
	switch x := n.(type) {
	case tql.And:
		for _, t := range x.Terms {
			if hasSensitiveHas(t) {
				return true
			}
		}
	case tql.Or:
		for _, t := range x.Terms {
			if hasSensitiveHas(t) {
				return true
			}
		}
	case tql.Not:
		return hasSensitiveHas(x.Term)
	case tql.Pred:
		if x.Field == "has" {
			for _, v := range x.Values {
				if Sensitive(v) {
					return true
				}
			}
		}
	}
	return false
}

func (c *call) runSearch(q *tql.Query, label string, pp pageParams) error {
	res, err := c.s.searcher.SearchQuery(c.ctx(), q, label, search.Page{Limit: pp.Limit, Offset: pp.Offset})
	if err != nil {
		return err
	}
	return c.json(200, SearchPage{
		Summary: SearchSummary{Query: res.Query, Total: res.Total, ByType: res.ByType, Fuzzy: res.Fuzzy, TookMS: res.Took.Milliseconds()},
		Total:   res.Total, Limit: res.Limit, Offset: res.Offset, Items: objectViews(res.Items),
	})
}

func (s *Server) hObjects(c *call) error {
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	v := c.r.URL.Query()
	q, err := c.searchQuery(v.Get("q"), v.Get("sort"), v.Get("type"), false)
	if err != nil {
		return err
	}
	return c.runSearch(q, v.Get("q"), pp)
}

func (s *Server) hSearch(c *call) error {
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	v := c.r.URL.Query()
	q, err := c.searchQuery(v.Get("q"), "", "", true)
	if err != nil {
		return err
	}
	return c.runSearch(q, v.Get("q"), pp)
}

func (s *Server) hObject(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	at, err := optTime(c, "at")
	if err != nil {
		return err
	}
	o, site, err := c.loadObject(id)
	if err != nil {
		return err
	}
	view := objectView(o)
	view.SiteID = site
	if at == nil {
		return c.json(200, view)
	}
	state, err := s.st.AsOf(c.ctx(), id, *at)
	if err != nil {
		return err
	}
	if len(state) == 0 {
		return notFound("object %s had no state at %s", id, at.Format(time.RFC3339))
	}
	return c.json(200, s.asOfView(view, o.Type, state, *at))
}

// asOfView replaces the current state of view by the one at time at. System
// keys (_status, _parent) become status and parent; the display name is
// derived from the attributes of that date like the engine derives it.
func (s *Server) asOfView(view ObjectView, typ string, state map[string]any, at time.Time) ObjectView {
	attrs := map[string]any{}
	raw := map[string]json.RawMessage{}
	for k, v := range state {
		switch {
		case k == "_status":
			if st, ok := v.(string); ok {
				view.Status = st
			}
		case k == "_parent":
			if p, ok := v.(string); ok && p != "" {
				view.ParentID = &p
			} else {
				view.ParentID = nil
			}
		case strings.HasPrefix(k, "_"):
		case Sensitive(k):
		default:
			attrs[k] = v
			raw[k], _ = json.Marshal(v)
		}
	}
	view.Attributes = attrs
	view.Display, _ = reconcile.DisplayAndSearch(s.registry().Types[typ], raw)
	view.FirstSeen, view.LastSeen = nil, nil
	view.AsOf = &at
	return view
}

func (s *Server) hProvenance(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	if _, _, err := c.loadObject(id); err != nil {
		return err
	}
	vals, err := s.st.Provenance(c.ctx(), id)
	if err != nil {
		return err
	}
	claims, err := s.st.Claims(c.ctx(), id)
	if err != nil {
		return err
	}
	byKey := map[string][]store.Claim{}
	for _, cl := range redactClaims(claims) {
		byKey[cl.Key] = append(byKey[cl.Key], cl)
	}
	out := Provenance{ObjectID: id, Attributes: []ProvenanceEntry{}}
	for _, v := range redactValues(vals) {
		e := ProvenanceEntry{Key: v.Key, Value: v.Value, Source: v.Source, Confidence: v.Confidence, Author: v.Author,
			ValidFrom: v.ValidFrom, LastSeen: v.LastSeen}
		for _, cl := range byKey[v.Key] {
			if cl.Source == v.Source {
				first := cl.FirstSeen
				e.FirstSeen = &first
				if e.LastSeen == nil {
					last := cl.LastSeen
					e.LastSeen = &last
				}
			} else {
				e.OtherClaims = append(e.OtherClaims, cl)
			}
		}
		out.Attributes = append(out.Attributes, e)
	}
	return c.json(200, out)
}

func (s *Server) hHistory(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	key := c.r.URL.Query().Get("key")
	if key != "" && Sensitive(key) {
		return newErr(403, "sensitive-attribute", "the attribute %q is not exposed", key)
	}
	if _, _, err := c.loadObject(id); err != nil {
		return err
	}
	vals, err := s.st.History(c.ctx(), id, key, maxScan)
	if err != nil {
		return err
	}
	return c.json(200, pageOf(redactValues(vals), pp, len(vals) >= maxScan))
}

func (s *Server) hObjectRelations(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	at, err := optTime(c, "at")
	if err != nil {
		return err
	}
	if _, _, err := c.loadObject(id); err != nil {
		return err
	}
	rels, err := s.st.RelationsOf(c.ctx(), id, at)
	if err != nil {
		return err
	}
	views, err := c.relationViews(id, redactRelations(rels))
	if err != nil {
		return err
	}
	return c.json(200, pageOf(views, pp, false))
}

// relationViews names the other end of each relation and drops the relations
// whose other end the caller may not read.
func (c *call) relationViews(self string, rels []store.Relation) ([]RelationView, error) {
	ids := make([]string, 0, len(rels))
	for _, r := range rels {
		ids = append(ids, otherEnd(r, self))
	}
	refs, err := c.visibleRefs(ids)
	if err != nil {
		return nil, err
	}
	out := []RelationView{}
	for _, r := range rels {
		other := otherEnd(r, self)
		ref, ok := refs[other]
		if !ok {
			continue
		}
		dir := "out"
		if r.ToID == self {
			dir = "in"
		}
		out = append(out, RelationView{Relation: r, Direction: dir, Other: ref})
	}
	return out, nil
}

func otherEnd(r store.Relation, self string) string {
	if r.FromID == self {
		return r.ToID
	}
	return r.FromID
}

// visibleRefs loads the objects with these ids and keeps the ones the caller
// may read (type and site checked).
func (c *call) visibleRefs(ids []string) (map[string]ObjectRef, error) {
	out := map[string]ObjectRef{}
	if len(ids) == 0 {
		return out, nil
	}
	objs, err := c.s.st.Objects(c.ctx(), ids)
	if err != nil {
		return nil, err
	}
	objIDs := make([]string, 0, len(objs))
	for _, o := range objs {
		objIDs = append(objIDs, o.ID)
	}
	sites, err := c.s.siteIDs(c.ctx(), objIDs)
	if err != nil {
		return nil, err
	}
	for _, o := range objs {
		if c.s.auth.Allow(c.p, ActionRead, o.Type, sites[o.ID]) {
			out[o.ID] = ObjectRef{ID: o.ID, Type: o.Type, Display: o.Display}
		}
	}
	return out, nil
}

// changeParams reads from and to; from is required for the global feed.
func (c *call) changeParams(requireFrom bool) (from, to time.Time, err error) {
	f, err := optTime(c, "from")
	if err != nil {
		return from, to, err
	}
	switch {
	case f != nil:
		from = *f
	case requireFrom:
		return from, to, badRequest("from is required")
	default:
		from = time.Unix(0, 0).UTC()
	}
	t, err := optTime(c, "to")
	if err != nil {
		return from, to, err
	}
	if t != nil {
		to = *t
	} else {
		to = c.s.now().UTC()
	}
	if to.Before(from) {
		return from, to, badRequest("to must not be before from")
	}
	return from, to, nil
}

func (s *Server) hTimeline(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	from, to, err := c.changeParams(false)
	if err != nil {
		return err
	}
	if _, _, err := c.loadObject(id); err != nil {
		return err
	}
	changes, err := s.st.Changes(c.ctx(), from, to, id, maxScan)
	if err != nil {
		return err
	}
	return c.json(200, pageOf(redactChanges(changes), pp, len(changes) >= maxScan))
}

func (s *Server) hChanges(c *call) error {
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	from, to, err := c.changeParams(true)
	if err != nil {
		return err
	}
	object := ""
	if v := c.r.URL.Query().Get("object"); v != "" {
		if object, err = parseUUID(v, "object"); err != nil {
			return err
		}
	}
	changes, err := s.st.Changes(c.ctx(), from, to, object, maxScan)
	if err != nil {
		return err
	}
	changes = redactChanges(changes)
	changes, err = c.filterChanges(changes)
	if err != nil {
		return err
	}
	return c.json(200, pageOf(changes, pp, false))
}

// filterChanges keeps the changes on objects the caller may read. A relation
// change keeps its place in the feed but loses other_id when the caller may not
// read the other end: the id of an unreadable object is not disclosed.
func (c *call) filterChanges(in []store.Change) ([]store.Change, error) {
	seen := map[string]bool{}
	var ids, others []string
	for _, ch := range in {
		if !seen[ch.ObjectID] {
			seen[ch.ObjectID] = true
			ids = append(ids, ch.ObjectID)
		}
	}
	sites, err := c.s.siteIDs(c.ctx(), ids)
	if err != nil {
		return nil, err
	}
	kept := []store.Change{}
	seenOther := map[string]bool{}
	for _, ch := range in {
		if c.s.auth.Allow(c.p, ActionRead, ch.Type, sites[ch.ObjectID]) {
			kept = append(kept, ch)
			if ch.OtherID != "" && !seenOther[ch.OtherID] {
				seenOther[ch.OtherID] = true
				others = append(others, ch.OtherID)
			}
		}
	}
	if len(others) == 0 {
		return kept, nil
	}
	objs, err := c.s.st.Objects(c.ctx(), others)
	if err != nil {
		return nil, err
	}
	types := map[string]string{}
	for _, o := range objs {
		types[o.ID] = o.Type
	}
	osites, err := c.s.siteIDs(c.ctx(), others)
	if err != nil {
		return nil, err
	}
	for i := range kept {
		if id := kept[i].OtherID; id != "" && !c.s.auth.Allow(c.p, ActionRead, types[id], osites[id]) {
			kept[i].OtherID = ""
		}
	}
	return kept, nil
}

func (s *Server) hWhoHad(c *call) error {
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	v := c.r.URL.Query()
	key, value := v.Get("key"), v.Get("value")
	if key == "" || value == "" {
		return badRequest("key and value are required")
	}
	if !nameRe.MatchString(key) {
		return badRequest("key must be an attribute name")
	}
	if Sensitive(key) {
		return newErr(403, "sensitive-attribute", "the attribute %q is not exposed", key)
	}
	at, err := optTime(c, "at")
	if err != nil {
		return err
	}
	var vals []store.Value
	var ids []string
	for _, cand := range s.valueForms(key, value) {
		vs, is, err := s.st.WhoHad(c.ctx(), key, cand, at)
		if err != nil {
			return err
		}
		vals, ids = append(vals, vs...), append(ids, is...)
	}
	refs, err := c.visibleRefs(ids)
	if err != nil {
		return err
	}
	items := []WhoHadItem{}
	for i, val := range vals {
		if ref, ok := refs[ids[i]]; ok {
			items = append(items, WhoHadItem{Object: ref, Value: val})
		}
	}
	return c.json(200, pageOf(items, pp, len(vals) >= 200))
}

// valueForms returns the spellings of value under which it may be stored: the
// text as given and its canonical form for every address-like attribute
// called key ("AA-BB-CC-00-00-01" is stored "aa:bb:cc:00:00:01").
func (s *Server) valueForms(key, value string) []string {
	forms := []string{value}
	for _, t := range s.registry().Types {
		a, ok := t.Attributes[key]
		if !ok || (a.Kind != schema.IP && a.Kind != schema.MAC && a.Kind != schema.Prefix) {
			continue
		}
		if n, err := schema.Normalize(a, value); err == nil {
			if ns, ok := n.(string); ok && !contains(forms, ns) {
				forms = append(forms, ns)
			}
		}
	}
	return forms
}

var sortFieldRe = regexp.MustCompile(`^[a-z_][a-z0-9_.]*$`)
