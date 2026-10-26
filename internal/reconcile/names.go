package reconcile

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// ErrNameTaken is returned when a batch gives an existing place a name that
// another place of the same type already carries under the same parent
// (HTTP 409, ADR 0067). It wraps ErrConflict.
var ErrNameTaken = fmt.Errorf("%w: name taken", ErrConflict)

// placeLayer is the schema layer of sites, buildings, rooms, rows, aisles and racks.
const placeLayer = "location"

// checkNames refuses to give a place, designated by its trica id, a weak
// identifier (its name) that a sibling of the same type already holds. A
// second place of that name would make the next declaration file import,
// which finds places by name, ambiguous. Only places are checked: devices and
// the rest may share a name, the duplicate being visible (ADR 0006).
//
// A sibling holds a name when any source claims it, including a claim another
// value has since outranked: that claim is what an import matches on.
func (r *run) checkNames(ctx context.Context) error {
	for _, o := range r.b.Objects {
		if o.Identity[sdk.IDKey] == "" {
			continue
		}
		t := r.reg.Types[o.Type]
		if t == nil || t.Layer != placeLayer {
			continue
		}
		id := r.ids[o.Ref]
		for _, k := range sdk.SortedKeys(t.Attributes) {
			a := t.Attributes[k]
			if a.Identity != schema.Weak {
				continue
			}
			raw, given := o.Attributes[k]
			if !given {
				if s, ok := o.Identity[k]; ok {
					raw, given = s, true
				}
			}
			if !given || raw == nil {
				continue
			}
			nv, err := schema.Normalize(a, raw)
			if err != nil {
				continue // writeClaims reports it
			}
			ident, err := t.ScopedIdentity(k, fmt.Sprint(nv), "")
			if err != nil || ident == "" {
				continue
			}
			var holder, display, source string
			err = r.tx.QueryRow(ctx, `SELECT o.id::text, o.display, c.source
				FROM claims c JOIN objects o ON o.id = c.object_id
				WHERE o.type = $1 AND c.key = $2 AND c.ident = $3 AND c.object_id <> $4::uuid
				AND o.parent_id IS NOT DISTINCT FROM (SELECT parent_id FROM objects WHERE id = $4::uuid)
				ORDER BY o.id, c.source LIMIT 1`, o.Type, k, ident, id).Scan(&holder, &display, &source)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: another %s here already carries the name %q (%s, claimed by %s); give %s a different name",
				ErrNameTaken, o.Type, fmt.Sprint(nv), display, source, o.Type)
		}
	}
	return nil
}
