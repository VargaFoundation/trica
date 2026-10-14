package api

import (
	"context"
	"errors"
	"sort"

	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// siteIDs returns, for each object id, the id of the site it is in (itself for
// a site). An object is in the site of its parent, or, for a device, of the
// rack it is currently mounted in (devices have no parent in the schema:
// their place is the mounted_in relation). Objects with no site, or with two
// different sites, are absent from the map: site-restricted grants then fail
// closed.
func (s *Server) siteIDs(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.st.Pool.Query(ctx, `
		WITH RECURSIVE up AS (
			SELECT id AS origin, id, type, 0 AS depth FROM objects WHERE id = ANY($1::uuid[])
			UNION ALL
			SELECT up.origin, n.id, n.type, up.depth + 1
			FROM up
			JOIN LATERAL (
				SELECT p.id, p.type FROM objects o JOIN objects p ON p.id = o.parent_id WHERE o.id = up.id
				UNION
				SELECT t.id, t.type FROM relations r JOIN objects t ON t.id = r.to_id
				WHERE r.type = 'mounted_in' AND r.from_id = up.id AND upper_inf(r.valid)
			) n ON true
			WHERE up.type <> 'site' AND up.depth < 32
		)
		SELECT DISTINCT origin::text, id::text FROM up WHERE type = 'site'`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ambiguous := map[string]bool{}
	for rows.Next() {
		var origin, site string
		if err := rows.Scan(&origin, &site); err != nil {
			return nil, err
		}
		if prev, ok := out[origin]; ok && prev != site {
			ambiguous[origin] = true
		}
		out[origin] = site
	}
	for o := range ambiguous {
		delete(out, o)
	}
	return out, rows.Err()
}

// loadObject returns object id after checking the caller may read it: 404 when
// it does not exist, 403 when it exists but the caller's grants do not cover
// its type and site.
func (c *call) loadObject(id string) (*store.Object, string, error) {
	o, err := c.s.st.Object(c.ctx(), id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", notFound("no object %s", id)
	}
	if err != nil {
		return nil, "", err
	}
	sites, err := c.s.siteIDs(c.ctx(), []string{id})
	if err != nil {
		return nil, "", err
	}
	site := sites[id]
	if !c.s.auth.Allow(c.p, ActionRead, o.Type, site) {
		return nil, "", forbidden("reading %s objects here is not granted", o.Type)
	}
	return o, site, nil
}

// idParam reads and validates the {id} path parameter.
func (c *call) idParam() (string, error) { return parseUUID(c.r.PathValue("id"), "id") }

// readableTypes lists the object types the caller may read in every site
// (Allow with an empty site), and whether that is all of them. List endpoints
// restrict their query to these types, so that counts and pages stay exact.
// A caller whose grants are limited to some sites reads no list: it reads
// objects one by one, where the site is known.
func (c *call) readableTypes() (types []string, all bool) {
	reg := c.s.registry()
	for _, t := range reg.TypeNames() {
		if c.s.auth.Allow(c.p, ActionRead, t, "") {
			types = append(types, t)
		}
	}
	return types, len(types) == len(reg.Types)
}

// batchEndpoints returns every endpoint a batch's relations and unrelations
// point at, parents included.
func batchEndpoints(b *sdk.Batch) []sdk.Endpoint {
	var out []sdk.Endpoint
	var add func(e sdk.Endpoint)
	add = func(e sdk.Endpoint) {
		out = append(out, e)
		if e.Parent != nil {
			add(*e.Parent)
		}
	}
	for _, r := range b.Relations {
		if r != nil {
			add(r.From)
			add(r.To)
		}
	}
	for _, r := range b.Unrelations {
		if r != nil {
			add(r.From)
			add(r.To)
		}
	}
	return out
}

// batchTypes returns the distinct object types of a batch, sorted: those of
// its objects and those the relations and unrelations point at. An endpoint
// designated by ref has the type of the batch object it names (already in the
// set); one designated by identity carries its own type.
func batchTypes(b *sdk.Batch) []string {
	set := map[string]bool{}
	for _, o := range b.Objects {
		set[o.Type] = true
	}
	for _, e := range batchEndpoints(b) {
		if e.Ref == "" && e.Type != "" {
			set[e.Type] = true
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// allowTypes reports whether the caller holds action on every type, in every site.
func (c *call) allowTypes(action string, types []string) (string, bool) {
	for _, t := range types {
		if !c.s.auth.Allow(c.p, action, t, "") {
			return t, false
		}
	}
	return "", true
}
