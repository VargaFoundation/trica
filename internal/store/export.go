package store

import (
	"context"
	"encoding/json"
	"time"
)

// ObjectValue is a Value tied to the object it belongs to. System attributes
// (keys starting with "_": _status, _parent) are included.
type ObjectValue struct {
	ObjectID string `json:"object_id"`
	Value
}

// ValuesAt returns the resolved value of every attribute of every object that
// was valid at *at, or the current ones when at is nil. With history set it
// returns every value that ever was, closed ranges included, and at is ignored.
// Rows are ordered by object, key, then start of validity.
func (s *Store) ValuesAt(ctx context.Context, at *time.Time, history bool) ([]ObjectValue, error) {
	q := `SELECT object_id::text, key, value, source, confidence, author, lower(valid), upper(valid), changeset_id FROM attr_history`
	var args []any
	switch {
	case history:
	case at != nil:
		q += ` WHERE valid @> $1::timestamptz`
		args = append(args, *at)
	default:
		q += ` WHERE upper_inf(valid)`
	}
	q += ` ORDER BY object_id, key, lower(valid)`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ObjectValue
	for rows.Next() {
		var v ObjectValue
		var raw []byte
		if err := rows.Scan(&v.ObjectID, &v.Key, &raw, &v.Source, &v.Confidence, &v.Author, &v.ValidFrom, &v.ValidTo, &v.ChangesetID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &v.Value.Value); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RelationsAt returns every relation valid at *at, or the current ones when at
// is nil. With history set it returns every relation that ever existed.
func (s *Store) RelationsAt(ctx context.Context, at *time.Time, history bool) ([]Relation, error) {
	q := `SELECT ` + relationCols + ` FROM relations`
	var args []any
	switch {
	case history:
	case at != nil:
		q += ` WHERE valid @> $1::timestamptz`
		args = append(args, *at)
	default:
		q += ` WHERE upper_inf(valid)`
	}
	q += ` ORDER BY type, from_id, to_id, lower(valid)`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}
