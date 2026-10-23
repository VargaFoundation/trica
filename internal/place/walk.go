package place

// Graph is the in-memory view of the inventory the rule needs: what Sites
// reads from PostgreSQL, for a caller that already holds the graph (the
// topology map, ADR 0051). Relations are the open ones.
type Graph struct {
	// Type returns the type of an object, "" when it is unknown.
	Type func(id string) string
	// Parent returns the parent of an object, "" when it has none.
	Parent func(id string) string
	// Targets returns the ids an object points to with an open relation of
	// the given type (MountedIn or LocatedIn).
	Targets func(id, relType string) []string
}

// Site returns the site an object is in (itself for a site), "" when it has
// none or two different ones. It is the rule of Sites, step for step: the
// parent, every open mounted_in, and the open located_in only when the object
// has no open mounted_in.
func (g Graph) Site(id string) string {
	if g.Type(id) == "" {
		return ""
	}
	type item struct {
		id    string
		depth int
	}
	seen := map[string]bool{id: true}
	queue := []item{{id, 0}}
	sites := map[string]bool{}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if g.Type(it.id) == "site" {
			sites[it.id] = true
			continue
		}
		if it.depth >= MaxDepth {
			continue
		}
		var next []string
		if p := g.Parent(it.id); p != "" {
			next = append(next, p)
		}
		mounted := g.Targets(it.id, MountedIn)
		next = append(next, mounted...)
		if len(mounted) == 0 {
			next = append(next, g.Targets(it.id, LocatedIn)...)
		}
		for _, t := range next {
			if !seen[t] && g.Type(t) != "" {
				seen[t] = true
				queue = append(queue, item{t, it.depth + 1})
			}
		}
	}
	if len(sites) != 1 {
		return ""
	}
	for s := range sites {
		return s
	}
	return ""
}
