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
	// ReasonRacks: the device is mounted in racks of two or more sites. It has
	// no site.
	ReasonRacks = "racks"
)

// Placement is one open located_in of a device and the site it leads to.
type Placement struct {
	PlaceID   string   `json:"place_id"`
	PlaceType string   `json:"place_type"`
	SiteID    string   `json:"site_id,omitempty"` // "" when the place is in no site
	Sources   []string `json:"sources"`           // rel_claims sources, sorted
}

// Mount is one open mounted_in of a device and the site its rack is in.
type Mount struct {
	RackID  string   `json:"rack_id"`
	SiteID  string   `json:"site_id,omitempty"` // "" when the rack is in no site
	Sources []string `json:"sources"`           // rel_claims sources, sorted
}

// Conflict says why the placements of a device disagree on the site.
type Conflict struct {
	Reason     string      `json:"reason"`
	Sites      []string    `json:"sites"`           // distinct sites involved, sorted (the rack's included)
	Places     []Placement `json:"places"`          // sorted by PlaceID, never null
	Racks      []Mount     `json:"racks,omitempty"` // only for ReasonRacks, sorted by RackID
	RackID     string      `json:"rack_id,omitempty"`
	RackSiteID string      `json:"rack_site_id,omitempty"`
}

// Conflicts returns, for the devices among ids whose open placements disagree
// on the site, why. A device whose places agree, or that has none, is absent.
// A device mounted in racks of two different sites is one too (reason racks),
// even when it has no place: Sites fails closed on it.
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
	rows, err = q.Query(ctx, fmt.Sprintf(`
		SELECT r.from_id::text, r.to_id::text,
			coalesce(array_agg(c.source ORDER BY c.source) FILTER (WHERE c.source IS NOT NULL), '{}')
		FROM relations r
		LEFT JOIN rel_claims c ON c.type = r.type AND c.from_id = r.from_id AND c.to_id = r.to_id
		WHERE r.type = '%s' AND upper_inf(r.valid) AND r.from_id = ANY($1::uuid[])
		GROUP BY r.from_id, r.to_id`, MountedIn), ids)
	if err != nil {
		return nil, err
	}
	mounts := map[string][]Mount{}
	for rows.Next() {
		var from string
		var m Mount
		if err := rows.Scan(&from, &m.RackID, &m.Sources); err != nil {
			rows.Close()
			return nil, err
		}
		mounts[from] = append(mounts[from], m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var ends []string
	devs := map[string]bool{}
	for dev, ps := range places {
		devs[dev] = true
		for _, p := range ps {
			ends = append(ends, p.PlaceID)
		}
	}
	for dev, ms := range mounts {
		devs[dev] = true
		for _, m := range ms {
			ends = append(ends, m.RackID)
		}
	}
	sites, err := Sites(ctx, q, ends)
	if err != nil {
		return nil, err
	}
	for dev := range devs {
		ps := places[dev]
		if ps == nil {
			ps = []Placement{}
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i].PlaceID < ps[j].PlaceID })
		set := map[string]bool{}
		for i := range ps {
			ps[i].SiteID = sites[ps[i].PlaceID]
			if ps[i].SiteID != "" {
				set[ps[i].SiteID] = true
			}
		}
		if ms := mounts[dev]; len(ms) > 0 {
			sort.Slice(ms, func(i, j int) bool { return ms[i].RackID < ms[j].RackID })
			rackSites := map[string]bool{}
			rackID, rackSite := "", ""
			for i := range ms {
				ms[i].SiteID = sites[ms[i].RackID]
				if ms[i].SiteID == "" {
					continue
				}
				rackSites[ms[i].SiteID] = true
				if rackID == "" {
					rackID, rackSite = ms[i].RackID, ms[i].SiteID
				}
			}
			switch {
			case len(rackSites) >= 2:
				out[dev] = Conflict{Reason: ReasonRacks, Sites: sortedKeys(rackSites), Places: ps, Racks: ms}
			case len(rackSites) == 1 && len(set) > 0 && (len(set) != 1 || !set[rackSite]):
				set[rackSite] = true
				out[dev] = Conflict{Reason: ReasonRack, Sites: sortedKeys(set), Places: ps, RackID: rackID, RackSiteID: rackSite}
			}
			continue
		}
		if len(set) >= 2 {
			out[dev] = Conflict{Reason: ReasonSites, Sites: sortedKeys(set), Places: ps}
		}
	}
	return out, nil
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
