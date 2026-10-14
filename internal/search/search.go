package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/tql"
)

// DefaultLimit is the page size when neither the caller nor the query sets one.
const DefaultLimit = 50

// StatementTimeout bounds every search statement, so that one pathological
// query cannot hold the database.
const StatementTimeout = 5 * time.Second

// Searcher runs searches against the store. It only reads.
type Searcher struct {
	Store *store.Store
	// Now is the clock for relative times (-7d); tests replace it.
	Now func() time.Time
}

// Page selects a slice of the results. Zero values mean the defaults.
type Page struct {
	Limit  int
	Offset int
}

// Result is one page of results with the counts the UI and the MCP tools
// show first.
type Result struct {
	Query  string          `json:"query"`
	Total  int             `json:"total"`
	ByType map[string]int  `json:"by_type"`
	Items  []*store.Object `json:"items"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
	// Fuzzy says the free text matched nothing exactly and the results are
	// near matches (typos).
	Fuzzy bool          `json:"fuzzy,omitempty"`
	Took  time.Duration `json:"took"`
}

// ErrQuery wraps errors in the query itself, as opposed to the database.
var ErrQuery = errors.New("invalid query")

// Validate parses and compiles a query without running it.
func Validate(query string) error {
	q, err := tql.Parse(query)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrQuery, err)
	}
	if _, err := compile(q, time.Now(), false); err != nil {
		return fmt.Errorf("%w: %w", ErrQuery, err)
	}
	return nil
}

func (s *Searcher) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Search runs a TQL query. A limit: in the query caps the whole result (a
// "top 10"); the page then slices within it. Free text matches exactly
// first; only when that finds nothing is it retried tolerating typos.
func (s *Searcher) Search(ctx context.Context, query string, page Page) (*Result, error) {
	q, err := tql.Parse(query)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrQuery, err)
	}
	return s.SearchQuery(ctx, q, query, page)
}

// SearchQuery runs an already parsed query; label is echoed in Result.Query.
// Callers that must add conditions (the API restricts a search to the types a
// caller may read) build the syntax tree instead of splicing query text.
func (s *Searcher) SearchQuery(ctx context.Context, q *tql.Query, label string, page Page) (*Result, error) {
	start := time.Now()
	res, err := s.run(ctx, q, label, page, false)
	if err == nil && res.Total == 0 && len(q.Texts()) > 0 {
		res, err = s.run(ctx, q, label, page, true)
	}
	if err != nil {
		return nil, err
	}
	res.Took = time.Since(start)
	return res, nil
}

func (s *Searcher) run(ctx context.Context, q *tql.Query, query string, page Page, fuzzy bool) (*Result, error) {
	c, err := compile(q, s.now(), fuzzy)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrQuery, err)
	}
	limit := page.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, tql.MaxLimit)
	offset := max(page.Offset, 0)
	if c.limit > 0 {
		limit = max(min(limit, c.limit-offset), 0)
	}
	res := &Result{Query: strings.TrimSpace(query), ByType: map[string]int{}, Items: []*store.Object{}, Limit: limit, Offset: offset, Fuzzy: fuzzy}

	err = pgx.BeginTxFunc(ctx, s.Store.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", StatementTimeout.Milliseconds())); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT o.type, count(*) FROM objects o WHERE `+c.where+` GROUP BY o.type`, c.args[:c.nWhere]...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var typ string
			var n int
			if err := rows.Scan(&typ, &n); err != nil {
				rows.Close()
				return err
			}
			res.ByType[typ] = n
			res.Total += n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if c.limit > 0 {
			res.Total = min(res.Total, c.limit)
		}
		if limit == 0 || res.Total == 0 {
			return nil
		}
		args := append(append([]any(nil), c.args...), limit, offset)
		sql := fmt.Sprintf(`SELECT `+qualified()+` FROM objects o WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`,
			c.where, c.order, len(args)-1, len(args))
		rows, err = tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		items, err := store.CollectObjects(rows)
		if err != nil {
			return err
		}
		if items != nil {
			res.Items = items
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return res, nil
}

// qualified prefixes the object columns with the "o" alias.
func qualified() string {
	cols := strings.Split(store.ObjectColumns, ",")
	for i, c := range cols {
		cols[i] = "o." + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}
