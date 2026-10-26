package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/schema"
)

// displayBatch is how many objects one recompute transaction rewrites.
var displayBatch = 500

// DisplayRule is the identity of how a type derives its display name: the
// display attribute and the template. Two types with the same rule text name
// their objects the same way. A type with no template has the empty rule: that
// is what every object created before templates existed follows.
func DisplayRule(t *schema.Type) string {
	if t == nil || t.DisplayTemplate == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(t.Display + "\x00" + t.DisplayTemplate))
	return hex.EncodeToString(sum[:8])
}

// RecomputeDisplays brings the stored display name and search text of the
// objects whose type's display rule is not the one recorded (display_rules)
// in line with the current schema, and records the rule. It is idempotent: a
// second call finds every rule recorded and does nothing. Objects are never
// created, deleted or given a new value: only the derived columns of objects
// are rewritten, in batches, each in its own transaction. It returns the number
// of objects rewritten.
func (e *Engine) RecomputeDisplays(ctx context.Context, log *slog.Logger) (int, error) {
	reg := e.Schema()
	recorded := map[string]string{}
	rows, err := e.Store.Pool.Query(ctx, `SELECT type, rule FROM display_rules`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var t, r string
		if err := rows.Scan(&t, &r); err != nil {
			rows.Close()
			return 0, err
		}
		recorded[t] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	total := 0
	for _, name := range reg.TypeNames() {
		t := reg.Types[name]
		want := DisplayRule(t)
		if have, ok := recorded[name]; (ok && have == want) || (!ok && want == "") {
			continue
		}
		n, err := e.recomputeType(ctx, t, want)
		if err != nil {
			return total, err
		}
		total += n
		if log != nil {
			log.Info("display names recomputed", "type", name, "objects", n)
		}
	}
	return total, nil
}

func (e *Engine) recomputeType(ctx context.Context, t *schema.Type, rule string) (int, error) {
	n, after := 0, "00000000-0000-0000-0000-000000000000"
	for {
		var done bool
		err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id::text, attrs, display, search_text FROM objects WHERE type = $1 AND id > $2::uuid ORDER BY id LIMIT $3`, t.Name, after, displayBatch)
			if err != nil {
				return err
			}
			type row struct {
				id, display, search string
				attrs               []byte
			}
			var batch []row
			for rows.Next() {
				var r row
				if err := rows.Scan(&r.id, &r.attrs, &r.display, &r.search); err != nil {
					rows.Close()
					return err
				}
				batch = append(batch, r)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, r := range batch {
				attrs := map[string]json.RawMessage{}
				if err := json.Unmarshal(r.attrs, &attrs); err != nil {
					return err
				}
				display, search := DisplayAndSearch(t, attrs)
				if display != r.display || search != r.search {
					if _, err := tx.Exec(ctx, `UPDATE objects SET display = $2, search_text = $3 WHERE id = $1::uuid`, r.id, display, search); err != nil {
						return err
					}
					n++
				}
			}
			if len(batch) < displayBatch {
				done = true
				_, err := tx.Exec(ctx, `INSERT INTO display_rules (type, rule, applied_at) VALUES ($1, $2, now())
					ON CONFLICT (type) DO UPDATE SET rule = EXCLUDED.rule, applied_at = EXCLUDED.applied_at`, t.Name, rule)
				return err
			}
			after = batch[len(batch)-1].id
			return nil
		})
		if err != nil {
			return n, err
		}
		if done {
			return n, nil
		}
	}
}
