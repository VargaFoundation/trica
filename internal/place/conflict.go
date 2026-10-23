package place

import (
	"context"
	"fmt"
	"sort"
)

// Reasons of a Conflict.
const (
	// ReasonSites: the places of a device are in two or more sites and it is in
	// no rack. The device has no site.
	ReasonSites = "sites"
	// ReasonRack: the device is mounted in a rack of one site and a place says
	// another site. The rack wins, but a declaration is false.
	ReasonRack = "rack"
)

// Placement is one open located_in of a device and the site it leads to.
type Placement struct {
	PlaceID   string   `json:"place_id"`
	PlaceType string   `json:"place_type"`
	SiteID    string   `json:"site_id,omitempty"` // "" when the place is in no site
	Sources   []string `json:"sources"`           // rel_claims sources, sorted
}

// Conflict says why the placements of a device disagree on the site.
type Conflict struct {
	Reason     string      `json:"reason"`
	Sites      []string    `json:"sites"`  // distinct sites involved, sorted (the rack's included)
	Places     []Placement `json:"places"` // sorted by PlaceID
	RackID     string      `json:"rack_id,omitempty"`
	RackSiteID string      `json:"rack_site_id,omitempty"`
}

// Conflicts returns, for the devices among ids whose open placements disagree
// on the site, why. A device whose places agree, or that has none, is absent.
// Racks in two different sites are not a conflict here: that ambiguity is the
// one of mounted_in itself, and Sites already fails closed on it.
func Conflicts(ctx context.Context, q Querier, ids []string) (map[string]Conflict, error) {
	out := map[string]Conflict{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, fmt.Sprintf(`
		SELECT r.from_id::text, r.to_id::text, o.type,
			coalesce(array_agg(c.source ORDER BY c.source) FILTER (WHERE c.source IS NOT NULL), '{}')
		FROM relations r
		JOIN objects o ON o.id = r.to_id
		LEFT JOIN rel_claims c ON c.type = r.type AND c.from_id = r.from_id AND c.to_id = r.to_id
		WHERE r.type = '%s' AND upper_inf(r.valid) AND r.from_id = ANY($1::uuid[])
		GROUP BY r.from_id, r.to_id, o.type`, LocatedIn), ids)
	if err != nil {
		return nil, err
	}
	places := map[string][]Placement{}
	for rows.Next() {
		var from string
		var p Placement
		if err := rows.Scan(&from, &p.PlaceID, &p.PlaceType, &p.Sources); err != nil {
			rows.Close()
			return nil, err
		}
		places[from] = append(places[from], p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, fmt.Sprintf(`SELECT from_id::text, to_id::text FROM relations
		WHERE type = '%s' AND upper_inf(valid) AND from_id = ANY($1::uuid[])`, MountedIn), ids)
	if err != nil {
		return nil, err
	}
	racks := map[string][]string{}
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			rows.Close()
			return nil, err
		}
		racks[from] = append(racks[from], to)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var ends []string
	for _, ps := range places {
		for _, p := range ps {
			ends = append(ends, p.PlaceID)
		}
	}
	for _, rs := range racks {
		ends = append(ends, rs...)
	}
	sites, err := Sites(ctx, q, ends)
	if err != nil {
		return nil, err
	}
	for dev, ps := range places {
		sort.Slice(ps, func(i, j int) bool { return ps[i].PlaceID < ps[j].PlaceID })
		set := map[string]bool{}
		for i := range ps {
			ps[i].SiteID = sites[ps[i].PlaceID]
			if ps[i].SiteID != "" {
				set[ps[i].SiteID] = true
			}
		}
		if rs := racks[dev]; len(rs) > 0 {
			rackID, rackSite, ok := soleRackSite(rs, sites)
			if !ok || len(set) == 0 || (len(set) == 1 && set[rackSite]) {
				continue
			}
			set[rackSite] = true
			out[dev] = Conflict{Reason: ReasonRack, Sites: sortedKeys(set), Places: ps, RackID: rackID, RackSiteID: rackSite}
			continue
		}
		if len(set) >= 2 {
			out[dev] = Conflict{Reason: ReasonSites, Sites: sortedKeys(set), Places: ps}
		}
	}
	return out, nil
}

// soleRackSite returns the site of the racks when they all lead to one, and
// the first of them (by id).
func soleRackSite(racks []string, sites map[string]string) (rackID, site string, ok bool) {
	sort.Strings(racks)
	for _, rk := range racks {
		s := sites[rk]
		if s == "" {
			continue
		}
		if site != "" && s != site {
			return "", "", false
		}
		if site == "" {
			rackID, site = rk, s
		}
	}
	return rackID, site, site != ""
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PlacedUnder returns the devices with an open located_in or mounted_in to one
// of ids or to anything below them (parent_id), bounded by MaxDepth. It tells
// which devices a moved room, building or rack may have put in or out of a
// conflict.
func PlacedUnder(ctx context.Context, q Querier, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, fmt.Sprintf(`
		WITH RECURSIVE down(id, depth) AS (
			SELECT id, 0 FROM objects WHERE id = ANY($1::uuid[])
			UNION
			SELECT c.id, down.depth + 1 FROM down JOIN objects c ON c.parent_id = down.id WHERE down.depth < %d
		)
		SELECT DISTINCT r.from_id::text FROM relations r JOIN down ON down.id = r.to_id
		WHERE r.type IN ('%s', '%s') AND upper_inf(r.valid) ORDER BY 1`, MaxDepth, LocatedIn, MountedIn), ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
