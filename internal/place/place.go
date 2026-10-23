// Package place holds the one rule that says which site an object is in
// (ADR 0058). The API's site-limited grants, the MCP guard and the site: query
// all read it here, and nothing else restates it.
//
// An object is in the site of its parent. A device has no parent in the schema:
// it is in the site of the rack it is mounted in (mounted_in), and, when it is
// in no rack, in the site of the site, building or room a person placed it in
// (located_in). A rack always wins over a place. An object with no site, or
// with two different sites, has none: site-restricted grants fail closed.
package place

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Relation types the rule reads.
const (
	MountedIn = "mounted_in"
	LocatedIn = "located_in"
)

// MaxDepth bounds the walk, whatever the data holds.
const MaxDepth = 32

// Querier is what the rule needs from a database: pgxpool.Pool and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// DeclaredOnly reports whether a relation type may only be claimed with
// declared confidence: people place devices, collectors do not (ADR 0058).
func DeclaredOnly(relType string) bool { return relType == LocatedIn }

// Exclusive reports whether a source holds at most one open claim of this type
// per origin: a new place replaces the one the same source gave before.
func Exclusive(relType string) bool { return relType == LocatedIn }

// Sites returns, for each id, the id of the site it is in (itself for a site).
// An object with no site, or two different ones, is absent from the map.
func Sites(ctx context.Context, q Querier, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, fmt.Sprintf(`
		WITH RECURSIVE up AS (
			SELECT id AS origin, id, type, 0 AS depth FROM objects WHERE id = ANY($1::uuid[])
			UNION ALL
			SELECT up.origin, n.id, n.type, up.depth + 1
			FROM up
			JOIN LATERAL (
				SELECT p.id, p.type FROM objects o JOIN objects p ON p.id = o.parent_id WHERE o.id = up.id
				UNION
				SELECT t.id, t.type FROM relations r JOIN objects t ON t.id = r.to_id
				WHERE r.type = '%[1]s' AND r.from_id = up.id AND upper_inf(r.valid)
				UNION
				SELECT t.id, t.type FROM relations r JOIN objects t ON t.id = r.to_id
				WHERE r.type = '%[2]s' AND r.from_id = up.id AND upper_inf(r.valid)
				AND NOT EXISTS (SELECT 1 FROM relations m WHERE m.type = '%[1]s' AND m.from_id = up.id AND upper_inf(m.valid))
			) n ON true
			WHERE up.type <> 'site' AND up.depth < %[3]d
		)
		SELECT DISTINCT origin::text, id::text FROM up WHERE type = 'site'`, MountedIn, LocatedIn, MaxDepth), ids)
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

// WithinSites returns a SQL condition on o.id that holds for the sites siteCond
// selects (a condition on alias s, over objects) and everything below them:
// children, devices mounted in a rack there, devices placed there and in no
// rack. It is the rule of Sites read from the sites downwards.
func WithinSites(siteCond string) string {
	return fmt.Sprintf(`o.id IN (
		WITH RECURSIVE hit(id, depth) AS (
			SELECT s.id, 0 FROM objects s WHERE s.type = 'site' AND (%[1]s)
			UNION
			SELECT n.id, hit.depth + 1 FROM hit
			JOIN LATERAL (
				SELECT c.id FROM objects c WHERE c.parent_id = hit.id
				UNION ALL
				SELECT r.from_id FROM relations r
				WHERE r.to_id = hit.id AND r.type = '%[2]s' AND upper_inf(r.valid)
				UNION ALL
				SELECT r.from_id FROM relations r
				WHERE r.to_id = hit.id AND r.type = '%[3]s' AND upper_inf(r.valid)
				AND NOT EXISTS (SELECT 1 FROM relations m WHERE m.type = '%[2]s' AND m.from_id = r.from_id AND upper_inf(m.valid))
			) n ON true
			WHERE hit.depth < %[4]d
		)
		SELECT id FROM hit)`, siteCond, MountedIn, LocatedIn, MaxDepth)
}
