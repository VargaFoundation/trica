package api

import (
	"context"
	"errors"
	"sort"

	"github.com/VargaFoundation/trica/internal/place"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// siteIDs returns, for each object id, the id of the site it is in (itself for
// a site): its parent's, or for a device the rack it is mounted in, or when it
// is in no rack the place it was declared in (located_in). The rule lives in
// internal/place (ADR 0058). Objects with no site, or with two different
// sites, are absent from the map: site-restricted grants then fail closed.
func (s *Server) siteIDs(ctx context.Context, ids []string) (map[string]string, error) {
	return place.Sites(ctx, s.st.Pool, ids)
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
