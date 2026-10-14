package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/store"
)

// Saved is a named query: it becomes a view, an alert or a report, and an
// MCP resource.
type Saved struct {
	Name        string    `json:"name"`
	Query       string    `json:"query"`
	Description string    `json:"description,omitempty"`
	Owner       string    `json:"owner,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// MaxNameLength bounds a saved query's name.
const MaxNameLength = 100

// Save creates or replaces a saved query. The query must be valid.
func (s *Searcher) Save(ctx context.Context, q Saved) error {
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" || len(q.Name) > MaxNameLength {
		return fmt.Errorf("%w: a saved query needs a name of 1 to %d characters", ErrQuery, MaxNameLength)
	}
	if err := Validate(q.Query); err != nil {
		return err
	}
	_, err := s.Store.Pool.Exec(ctx, `
		INSERT INTO saved_queries (name, query, description, owner) VALUES ($1, $2, $3, $4)
		ON CONFLICT (name) DO UPDATE SET query = EXCLUDED.query, description = EXCLUDED.description, owner = EXCLUDED.owner`,
		q.Name, q.Query, q.Description, q.Owner)
	return err
}

// SavedQueries lists the saved queries by name.
func (s *Searcher) SavedQueries(ctx context.Context) ([]Saved, error) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT name, query, description, owner, created_at FROM saved_queries ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Saved, error) {
		var q Saved
		err := r.Scan(&q.Name, &q.Query, &q.Description, &q.Owner, &q.CreatedAt)
		return q, err
	})
}

// SavedQuery returns one saved query, or store.ErrNotFound.
func (s *Searcher) SavedQuery(ctx context.Context, name string) (*Saved, error) {
	var q Saved
	err := s.Store.Pool.QueryRow(ctx, `SELECT name, query, description, owner, created_at FROM saved_queries WHERE name = $1`, name).
		Scan(&q.Name, &q.Query, &q.Description, &q.Owner, &q.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// DeleteSavedQuery removes a saved query (not inventory: nothing to keep).
func (s *Searcher) DeleteSavedQuery(ctx context.Context, name string) error {
	tag, err := s.Store.Pool.Exec(ctx, `DELETE FROM saved_queries WHERE name = $1`, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
