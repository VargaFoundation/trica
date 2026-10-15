package reconcile

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// SweepAbsent marks as absent every observed object no source confirmed for
// longer than the policy's AbsentAfter. Objects are never deleted: absence is
// a historised state, so "when did this disk disappear" stays answerable.
// Declared-only objects are never marked absent; nothing is expected to see them.
func (e *Engine) SweepAbsent(ctx context.Context) ([]string, error) {
	now := e.now()
	cutoff := now.Add(-e.Policy.absentAfter())
	var ids []string
	err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7426201)`); err != nil {
			return err
		}
		var cs int64
		if err := tx.QueryRow(ctx, `INSERT INTO changesets (at, source, reason, kind) VALUES ($1, 'system:absence', 'not observed since cutoff', 'system') RETURNING id`, now).Scan(&cs); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `UPDATE objects SET status = 'absent' WHERE observed AND status = 'active' AND last_seen < $1 RETURNING id::text, type`, cutoff)
		if err != nil {
			return err
		}
		types := map[string]string{}
		for rows.Next() {
			var id, t string
			if err := rows.Scan(&id, &t); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			types[id] = t
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) == 0 {
			_, err := tx.Exec(ctx, `DELETE FROM changesets WHERE id = $1`, cs)
			return err
		}
		r := &run{e: e, tx: tx, now: now, cs: cs, res: &Result{}, types: types, touched: map[string]bool{}}
		r.b = nil
		raw, _ := json.Marshal("absent")
		for _, id := range ids {
			if err := r.writeHistory(ctx, id, "_status", &candidate{Source: "system:absence", Value: raw, Confidence: "observed"}); err != nil {
				return err
			}
		}
		stats, _ := json.Marshal(map[string]int{"absent": len(ids)})
		if _, err := tx.Exec(ctx, `UPDATE changesets SET stats = $2 WHERE id = $1`, cs, stats); err != nil {
			return err
		}
		return notify(ctx, tx, cs)
	})
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 && e.OnCommit != nil {
		e.OnCommit(Result{Touched: ids})
	}
	return ids, nil
}
