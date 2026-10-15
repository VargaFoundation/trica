package api

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
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

func (s *Server) hSchema(c *call) error { return c.json(200, s.schemaDoc()) }

func (s *Server) schemaDoc() SchemaDoc {
	reg := s.registry()
	doc := SchemaDoc{Types: []TypeDoc{}, Relations: []RelationDoc{}}
	for _, n := range reg.TypeNames() {
		doc.Types = append(doc.Types, typeDoc(reg.Types[n]))
	}
	for _, n := range sdk.SortedKeys(reg.Relations) {
		doc.Relations = append(doc.Relations, relationDoc(reg.Relations[n]))
	}
	return doc
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
	if err := checkSensitive(parsed); err != nil {
		return nil, err
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

	if err := c.restrictTypes(parsed, typeParam); err != nil {
		return nil, err
	}
	return parsed, nil
}

// checkSensitive refuses a query that names a sensitive attribute, in a
// condition, a has: predicate or a sort key.
func checkSensitive(parsed *tql.Query) error {
	for _, f := range parsed.Fields() {
		if Sensitive(f) {
			return newErr(403, "sensitive-attribute", "the attribute %q is not exposed and cannot be queried", f)
		}
	}
	if hasSensitiveHas(parsed.Where) {
		return newErr(403, "sensitive-attribute", "a queried attribute is not exposed")
	}
	for _, k := range parsed.Sort {
		if Sensitive(k.Field) {
			return newErr(403, "sensitive-attribute", "the attribute %q is not exposed and cannot be queried", k.Field)
		}
	}
	return nil
}

// restrictTypes narrows parsed, in its syntax tree, to typeParam when set and
// to the types the caller may read.
func (c *call) restrictTypes(parsed *tql.Query, typeParam string) error {
	var types []string
	switch {
	case typeParam != "":
		if _, ok := c.s.registry().Types[typeParam]; !ok {
			return badRequest("unknown object type %q", typeParam)
		}
		if !c.s.auth.Allow(c.p, ActionRead, typeParam, "") {
			return forbidden("reading %s objects across all sites is not granted", typeParam)
		}
		types = []string{typeParam}
	default:
		allowed, all := c.readableTypes()
		if len(allowed) == 0 {
			return forbidden("no object type is readable across all sites; read objects by id")
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
	return nil
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
	items := objectViews(res.Items)
	// The site of each item, in one query for the page, as GET /objects/{id}
	// gives it (#100). The caller reads these types across all sites, so the
	// site reveals nothing it could not read.
	ids := make([]string, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	sites, err := c.s.siteIDs(c.ctx(), ids)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].SiteID = sites[items[i].ID]
	}
	return c.json(200, SearchPage{
		Summary: SearchSummary{Query: res.Query, Total: res.Total, ByType: res.ByType, Fuzzy: res.Fuzzy, TookMS: res.Took.Milliseconds()},
		Total:   res.Total, Limit: res.Limit, Offset: res.Offset, Items: items,
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
	view, err := c.objectAt(id, at)
	if err != nil {
		return err
	}
	return c.json(200, view)
}

// objectAt loads an object the caller may read, as it is now or at a date.
func (c *call) objectAt(id string, at *time.Time) (*ObjectView, error) {
	o, site, err := c.loadObject(id)
	if err != nil {
		return nil, err
	}
	view := objectView(o)
	view.SiteID = site
	if at == nil {
		return &view, nil
	}
	state, err := c.s.st.AsOf(c.ctx(), id, *at)
	if err != nil {
		return nil, err
	}
	if len(state) == 0 {
		return nil, notFound("object %s had no state at %s", id, at.Format(time.RFC3339))
	}
	v := c.s.asOfView(view, o.Type, state, *at)
	return &v, nil
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
	out, err := s.provenance(c.ctx(), id)
	if err != nil {
		return err
	}
	return c.json(200, out)
}

// provenance reads where each attribute of an object comes from, sensitive
// attributes removed. It does not check the caller's rights.
func (s *Server) provenance(ctx context.Context, id string) (*Provenance, error) {
	vals, err := s.st.Provenance(ctx, id)
	if err != nil {
		return nil, err
	}
	claims, err := s.st.Claims(ctx, id)
	if err != nil {
		return nil, err
	}
	byKey := map[string][]store.Claim{}
	for _, cl := range redactClaims(claims) {
		byKey[cl.Key] = append(byKey[cl.Key], cl)
	}
	out := &Provenance{ObjectID: id, Attributes: []ProvenanceEntry{}}
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
	return out, nil
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
	ends, err := c.visibleEnds(ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ObjectRef, len(ends))
	for id, e := range ends {
		out[id] = ObjectRef{ID: e.ID, Type: e.Type, Display: e.Display}
	}
	return out, nil
}

// visibleEnds is visibleRefs with the parent and the site of each object.
func (c *call) visibleEnds(ids []string) (map[string]RelationEnd, error) {
	objs, sites, err := c.visibleObjects(ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]RelationEnd, len(objs))
	for id, o := range objs {
		e := RelationEnd{ID: o.ID, Type: o.Type, Display: o.Display, SiteID: sites[id]}
		if o.ParentID != nil {
			e.ParentID = *o.ParentID
		}
		out[id] = e
	}
	return out, nil
}

// visibleObjects loads the objects with these ids and keeps the ones the
// caller may read, with the site of each.
func (c *call) visibleObjects(ids []string) (map[string]*store.Object, map[string]string, error) {
	out := map[string]*store.Object{}
	if len(ids) == 0 {
		return out, nil, nil
	}
	objs, err := c.s.st.Objects(c.ctx(), ids)
	if err != nil {
		return nil, nil, err
	}
	objIDs := make([]string, 0, len(objs))
	for _, o := range objs {
		objIDs = append(objIDs, o.ID)
	}
	sites, err := c.s.siteIDs(c.ctx(), objIDs)
	if err != nil {
		return nil, nil, err
	}
	for _, o := range objs {
		if c.s.auth.Allow(c.p, ActionRead, o.Type, sites[o.ID]) {
			out[o.ID] = o
		}
	}
	return out, sites, nil
}

// hRackContents lists the devices mounted in a rack with their position, face
// and model, in three queries whatever the number of devices (finding #42).
func (s *Server) hRackContents(c *call) error {
	id, err := c.idParam()
	if err != nil {
		return err
	}
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	rack, _, err := c.loadObject(id)
	if err != nil {
		return err
	}
	if rack.Type != "rack" {
		return notFound("%s is a %s, not a rack", id, rack.Type)
	}
	rels, err := s.st.RelationsOf(c.ctx(), id, nil)
	if err != nil {
		return err
	}
	var mounts []store.Relation
	var devices []string
	for _, r := range redactRelations(rels) {
		if r.Type == "mounted_in" && r.ToID == id {
			mounts = append(mounts, r)
			devices = append(devices, r.FromID)
		}
	}
	models, err := s.st.CurrentRelationsFrom(c.ctx(), "instance_of", devices)
	if err != nil {
		return err
	}
	modelOf := map[string]string{}
	ids := append([]string(nil), devices...)
	for _, r := range models {
		if _, seen := modelOf[r.FromID]; !seen {
			modelOf[r.FromID] = r.ToID
			ids = append(ids, r.ToID)
		}
	}
	objs, _, err := c.visibleObjects(ids)
	if err != nil {
		return err
	}
	items := []RackItem{}
	for _, m := range mounts {
		d, ok := objs[m.FromID]
		if !ok {
			continue
		}
		it := RackItem{Device: ObjectRef{ID: d.ID, Type: d.Type, Display: d.Display}, Position: number(m.Attrs["position"])}
		if f, _ := m.Attrs["face"].(string); f == "front" || f == "rear" {
			it.Face = f
		}
		if t, ok := objs[modelOf[d.ID]]; ok {
			attrs := redactMap(t.Attrs)
			it.Model = &ObjectRef{ID: t.ID, Type: t.Type, Display: t.Display}
			it.UHeight = number(attrs["u_height"])
			it.FullDepth = attrs["full_depth"] == true
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].Position, items[j].Position
		if (a == nil) != (b == nil) {
			return a != nil
		}
		if a != nil && *a != *b {
			return *a < *b
		}
		return items[i].Device.Display < items[j].Device.Display
	})
	return c.json(200, pageOf(items, pp, false))
}

// number reads a JSON number attribute; anything else is unknown.
func number(v any) *float64 {
	switch n := v.(type) {
	case float64:
		return &n
	case int:
		f := float64(n)
		return &f
	case int64:
		f := float64(n)
		return &f
	}
	return nil
}

// maxRelationTypes bounds the type list of a bulk relation read.
const maxRelationTypes = 8

// hRelations lists the current relations of some types across the inventory,
// with both ends named. A relation is listed only when the caller may read
// both of its ends, like the relations of one object.
func (s *Server) hRelations(c *call) error {
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	raw := c.r.URL.Query().Get("type")
	if raw == "" {
		return badRequest("type is required: one or more relation types, comma separated")
	}
	names := strings.Split(raw, ",")
	if len(names) > maxRelationTypes {
		return badRequest("type lists at most %d relation types", maxRelationTypes)
	}
	reg := s.registry()
	for i, n := range names {
		names[i] = strings.TrimSpace(n)
		if _, ok := reg.Relations[names[i]]; !ok {
			return badRequest("%q is not a relation type of the registry", names[i])
		}
	}
	rels, err := s.st.CurrentRelationsOfTypes(c.ctx(), names, maxScan)
	if err != nil {
		return err
	}
	rels = redactRelations(rels)
	ids := make([]string, 0, 2*len(rels))
	for _, r := range rels {
		ids = append(ids, r.FromID, r.ToID)
	}
	ends, err := c.visibleEnds(ids)
	if err != nil {
		return err
	}
	out := []RelationLink{}
	for _, r := range rels {
		from, okF := ends[r.FromID]
		to, okT := ends[r.ToID]
		if okF && okT {
			out = append(out, RelationLink{Relation: r, From: from, To: to})
		}
	}
	return c.json(200, pageOf(out, pp, len(rels) >= maxScan))
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
