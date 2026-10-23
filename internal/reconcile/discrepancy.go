package reconcile

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/store"
)

// Discrepancy is a first-class gap between what is declared and what is
// observed, or an observation that cannot be tied to the inventory.
//
// Kinds: value_mismatch (declared and observed values differ),
// unresolved_neighbor (an LLDP neighbour no collector reports),
// unresolved_reference, cable_not_observed (documented, no LLDP),
// cable_not_declared (seen in LLDP, not documented), cable_conflict,
// location_conflict (the places of a device disagree on the site, ADR 0061).
type Discrepancy struct {
	ID          int64           `json:"id"`
	Kind        string          `json:"kind"`
	Fingerprint string          `json:"fingerprint"`
	ObjectID    string          `json:"object_id,omitempty"`
	RelatedID   string          `json:"related_id,omitempty"`
	Key         string          `json:"key,omitempty"`
	Declared    json.RawMessage `json:"declared,omitempty"`
	Observed    json.RawMessage `json:"observed,omitempty"`
	Details     json.RawMessage `json:"details,omitempty"`
	Status      string          `json:"status"`
	OpenedAt    time.Time       `json:"opened_at"`
	LastSeen    time.Time       `json:"last_seen"`
	ClosedAt    *time.Time      `json:"closed_at,omitempty"`
	ClosedBy    string          `json:"closed_by,omitempty"`
	Note        string          `json:"note,omitempty"`
}

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

// OpenDiscrepancy opens d, or refreshes it when the same fingerprint is open.
// An accepted discrepancy with the same fingerprint stays accepted.
func OpenDiscrepancy(ctx context.Context, tx pgx.Tx, d Discrepancy, now time.Time) error {
	var accepted bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM discrepancies WHERE fingerprint = $1 AND status = 'accepted')`, d.Fingerprint).Scan(&accepted); err != nil || accepted {
		return err
	}
	details := d.Details
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO discrepancies (kind, fingerprint, object_id, related_id, key, declared, observed, details, opened_at, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		ON CONFLICT (fingerprint) WHERE status = 'open' DO UPDATE
		SET declared = EXCLUDED.declared, observed = EXCLUDED.observed, details = EXCLUDED.details, last_seen = EXCLUDED.last_seen`,
		d.Kind, d.Fingerprint, nullUUID(d.ObjectID), nullUUID(d.RelatedID), d.Key, nullJSON(d.Declared), nullJSON(d.Observed), []byte(details), now)
	return err
}

// ResolveDiscrepancy closes the open discrepancy with this fingerprint, if any.
func ResolveDiscrepancy(ctx context.Context, tx pgx.Tx, fingerprint, by string, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE discrepancies SET status = 'resolved', closed_at = $2, closed_by = $3 WHERE fingerprint = $1 AND status = 'open'`,
		fingerprint, now, by)
	return err
}

// KindLocationConflict is raised by the engine when the places of a device
// disagree on the site (ADR 0061).
const KindLocationConflict = "location_conflict"

// ResolveOthers closes the open discrepancies of kind on objectID except the
// one with fingerprint keep ("" closes them all).
func ResolveOthers(ctx context.Context, tx pgx.Tx, kind, objectID, keep, by string, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE discrepancies SET status = 'resolved', closed_at = $4, closed_by = $5
		WHERE kind = $1 AND object_id = $2 AND status = 'open' AND fingerprint <> $3`, kind, objectID, keep, now, by)
	return err
}

// ResolveStale closes open discrepancies of the given kinds whose fingerprint
// starts with prefix and that were not refreshed since before. Producers that
// recompute a family of discrepancies in one pass (the cabling deducer) call
// it at the end of the pass.
func ResolveStale(ctx context.Context, tx pgx.Tx, kinds []string, prefix string, before time.Time, by string) (int64, error) {
	tag, err := tx.Exec(ctx, `UPDATE discrepancies SET status = 'resolved', closed_at = now(), closed_by = $4
		WHERE status = 'open' AND kind = ANY($1) AND fingerprint LIKE $2 || '%' AND last_seen < $3`, kinds, prefix, before, by)
	return tag.RowsAffected(), err
}

// DiscrepancyFilter selects discrepancies.
type DiscrepancyFilter struct {
	Status   string
	Kind     string
	ObjectID string
	Limit    int
	Offset   int
}

// ListDiscrepancies returns discrepancies, newest first, and the total count.
func ListDiscrepancies(ctx context.Context, s *store.Store, f DiscrepancyFilter) ([]Discrepancy, int, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	where := `($1 = '' OR status = $1) AND ($2 = '' OR kind = $2) AND ($3 = '' OR object_id = $3::uuid OR related_id = $3::uuid)`
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM discrepancies WHERE `+where, f.Status, f.Kind, f.ObjectID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, kind, fingerprint, coalesce(object_id::text, ''), coalesce(related_id::text, ''), key,
		declared, observed, details, status, opened_at, last_seen, closed_at, closed_by, note
		FROM discrepancies WHERE `+where+` ORDER BY last_seen DESC, id DESC LIMIT $4 OFFSET $5`, f.Status, f.Kind, f.ObjectID, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Discrepancy
	for rows.Next() {
		var d Discrepancy
		var decl, obs, det []byte
		if err := rows.Scan(&d.ID, &d.Kind, &d.Fingerprint, &d.ObjectID, &d.RelatedID, &d.Key, &decl, &obs, &det, &d.Status,
			&d.OpenedAt, &d.LastSeen, &d.ClosedAt, &d.ClosedBy, &d.Note); err != nil {
			return nil, 0, err
		}
		d.Declared, d.Observed, d.Details = decl, obs, det
		out = append(out, d)
	}
	return out, total, rows.Err()
}

// AcceptDiscrepancy marks a discrepancy as known and accepted: it will not
// reopen while the same gap persists.
func AcceptDiscrepancy(ctx context.Context, s *store.Store, id int64, by, note string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE discrepancies SET status = 'accepted', closed_at = now(), closed_by = $2, note = $3 WHERE id = $1 AND status = 'open'`, id, by, note)
	if err == nil && tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return err
}
