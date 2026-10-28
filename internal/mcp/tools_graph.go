package mcp

import (
	"errors"
	"slices"
	"sort"

	"github.com/VargaFoundation/trica/internal/api"
)

const (
	descImpact = `What goes down or degrades if objects fail: "if I power off sw-core-01, which applications fall" is impact_analysis{objects:["sw-core-01"], affected_types:["application"]}. ` +
		`Follows the dependency graph (cabling, power, hosting, runs-on). Each affected object says whether it is down or degraded, why, and from which failed object: a workload that lost some pods, or all of them while its cluster has a node left to restart them, is degraded; an application is down when all its instances are, degraded when one is. ` +
		`The answer starts with the counts per type, then lists the affected objects you may read, down first.`
	descPath = `How two objects are connected: the chain of objects and relations from one to the other (a server to a firewall, an application to its database host). ` +
		`Follows relations in either direction (restrict with relation_types, e.g. cabled_to). Answers found:false when there is no path. ` +
		`It is the connectivity declared or discovered in the inventory, not the traffic that actually flows.`
	maxFailed = 50
)

// ImpactInput is the input of impact_analysis.
type ImpactInput struct {
	Objects       []string `json:"objects" jsonschema:"the objects that fail: ids, names or addresses (at most 50)"`
	Anchors       []string `json:"anchors,omitempty" jsonschema:"objects connectivity is measured from (default: the graph's own, e.g. the sites' uplinks)"`
	AffectedTypes []string `json:"affected_types,omitempty" jsonschema:"only list affected objects of these types, e.g. application"`
	PageArgs
}

// ImpactSummary says first what a failure reaches.
type ImpactSummary struct {
	Failed       []api.ObjectRef `json:"failed"`
	Affected     int             `json:"affected"`
	Down         int             `json:"down"`
	Degraded     int             `json:"degraded"`
	DownByType   map[string]int  `json:"down_by_type,omitempty"`
	DegradedType map[string]int  `json:"degraded_by_type,omitempty"`
	Listed       Summary         `json:"listed"`
}

func (s *Server) toolImpact(t *tc, in ImpactInput) (any, error) {
	limit, offset, err := in.bounds()
	if err != nil {
		return nil, err
	}
	if len(in.Objects) == 0 || len(in.Objects) > maxFailed {
		return nil, api.BadRequest("give between 1 and %d objects that fail", maxFailed)
	}
	if len(in.Anchors) > maxFailed {
		return nil, api.BadRequest("give at most %d anchors", maxFailed)
	}
	failed, err := t.resolveAll(in.Objects)
	if err != nil {
		return nil, err
	}
	anchors, err := t.resolveAll(in.Anchors)
	if err != nil {
		return nil, err
	}
	failedIDs, anchorIDs := make([]string, 0, len(failed)), make([]string, 0, len(anchors))
	refs := make([]api.ObjectRef, 0, len(failed))
	for _, o := range failed {
		failedIDs = append(failedIDs, o.ID)
		refs = append(refs, api.ObjectRef{ID: o.ID, Type: o.Type, Display: o.Display})
	}
	for _, o := range anchors {
		anchorIDs = append(anchorIDs, o.ID)
	}
	t.target = failedIDs[0]
	t.details["failed"] = failedIDs
	res, err := s.graph.Impact(t.ctx, failedIDs, anchorIDs)
	if err != nil {
		if errors.Is(err, ErrUnknownObject) {
			return nil, api.NotFound("an object is not in the dependency graph yet (the graph is rebuilt periodically from the inventory)")
		}
		return nil, err
	}
	// The graph knows every object; the caller reads some of them only.
	ids := make([]string, 0, len(res.Affected))
	for _, a := range res.Affected {
		ids = append(ids, a.ID)
	}
	visible, err := visibleIDs(t, ids)
	if err != nil {
		return nil, err
	}
	sum := ImpactSummary{Failed: refs, DownByType: map[string]int{}, DegradedType: map[string]int{}}
	var items []Affected
	byType := map[string]int{}
	for _, a := range res.Affected {
		if _, ok := visible[a.ID]; !ok {
			continue
		}
		sum.Affected++
		if a.State == StateDown {
			sum.Down++
			sum.DownByType[a.Type]++
		} else {
			sum.Degraded++
			sum.DegradedType[a.Type]++
		}
		if len(in.AffectedTypes) > 0 && !slices.Contains(in.AffectedTypes, a.Type) {
			continue
		}
		items = append(items, a)
		byType[a.Type]++
	}
	sort.SliceStable(items, func(i, j int) bool {
		if (items[i].State == StateDown) != (items[j].State == StateDown) {
			return items[i].State == StateDown
		}
		if items[i].Depth != items[j].Depth {
			return items[i].Depth < items[j].Depth
		}
		return items[i].Display < items[j].Display
	})
	page := api.PageOf(items, limit, offset, false)
	sum.Listed = newSummary(page.Total, offset, len(page.Items), byType)
	return &Answer{Summary: sum, Limit: limit, Offset: offset, Items: page.Items}, nil
}

// visibleIDs keeps the ids of the objects the caller may read, reading them in batches.
func visibleIDs(t *tc, ids []string) (map[string]api.ObjectRef, error) {
	out := make(map[string]api.ObjectRef, len(ids))
	for len(ids) > 0 {
		n := min(len(ids), api.MaxLimit)
		refs, err := t.g.VisibleRefs(ids[:n])
		if err != nil {
			return nil, err
		}
		for id, r := range refs {
			out[id] = r
		}
		ids = ids[n:]
	}
	return out, nil
}

// PathInput is the input of network_path.
type PathInput struct {
	From          string   `json:"from" jsonschema:"where the path starts: an id, a name or an address"`
	To            string   `json:"to" jsonschema:"where it ends"`
	RelationTypes []string `json:"relation_types,omitempty" jsonschema:"follow these relation types only, e.g. cabled_to (default: all)"`
}

// PathSummary says first whether the objects are connected.
type PathSummary struct {
	Found bool   `json:"found"`
	Hops  int    `json:"hops"`
	From  string `json:"from"`
	To    string `json:"to"`
}

func (s *Server) toolPath(t *tc, in PathInput) (any, error) {
	t.target = in.From + " -> " + in.To
	from, err := t.resolve(in.From)
	if err != nil {
		return nil, err
	}
	to, err := t.resolve(in.To)
	if err != nil {
		return nil, err
	}
	rels := in.RelationTypes
	for _, r := range rels {
		if !fieldRe.MatchString(r) {
			return nil, api.BadRequest("relation type %q is not a relation name", r)
		}
	}
	hops, err := s.graph.Path(t.ctx, from.ID, to.ID, rels)
	if err != nil {
		if errors.Is(err, ErrUnknownObject) {
			return nil, api.NotFound("an object is not in the dependency graph yet (the graph is rebuilt periodically from the inventory)")
		}
		return nil, err
	}
	// A path through objects the caller may not read is not shown at all: a
	// partial path would leak that something is there.
	ids := make([]string, 0, len(hops))
	for _, h := range hops {
		if h.Node != nil {
			ids = append(ids, h.Node.ID)
		}
	}
	visible, err := visibleIDs(t, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, ok := visible[id]; !ok {
			return nil, api.Forbidden("the path crosses objects you are not allowed to read")
		}
	}
	if hops == nil {
		hops = []Hop{}
	}
	return struct {
		Summary PathSummary `json:"summary"`
		Path    []Hop       `json:"path"`
	}{PathSummary{Found: len(hops) > 0, Hops: max(0, len(hops)-1), From: from.Display, To: to.Display}, hops}, nil
}
