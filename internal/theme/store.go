package theme

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/VargaFoundation/trica/internal/store"
)

// Channel is the PostgreSQL notification channel a change of the selection is
// announced on, so that every replica applies it at once (ADR 0032).
const Channel = "trica_theme"

// Origins of a stored archive.
const (
	OriginUpload = "upload"
	OriginURL    = "url"
)

// Errors of the storage and of the service; the API maps them to statuses.
var (
	ErrNotFound = errors.New("theme: no such theme")
	ErrLocked   = errors.New("theme: the deployment configuration locks the theme")
	ErrActive   = errors.New("theme: the theme is in force")
)

// Info describes a stored archive (not its bytes).
type Info struct {
	SHA256     string
	Name       string
	Title      string
	Version    string
	Size       int
	Origin     string
	UploadedBy string
	UploadedAt time.Time
}

// Selection is the administrator's choice: a built-in theme or a stored
// archive, exactly one of the two.
type Selection struct {
	Builtin string
	SHA256  string
	SetBy   string
	SetAt   time.Time
}

// Store keeps the archives and the selection in PostgreSQL. It is not the
// inventory (ADR 0054): rows are deleted, and the API audits every change.
type Store struct{ DB *store.Store }

func shaBytes(sha string) ([]byte, error) {
	b, err := hex.DecodeString(sha)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("theme: %q is not a sha256", sha)
	}
	return b, nil
}

// Put stores an archive under its identity. It reports whether the archive was
// new: the same bytes stored twice are kept once, with their first author.
func (s *Store) Put(ctx context.Context, t *Theme, archive []byte, origin, by string) (bool, error) {
	sum, err := shaBytes(t.SHA)
	if err != nil {
		return false, err
	}
	tag, err := s.DB.Pool.Exec(ctx, `
		INSERT INTO ui_themes (sha256, name, title, version, archive, origin, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (sha256) DO NOTHING`,
		sum, t.Name, t.Title, t.Version, archive, origin, by)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Archive returns the bytes stored under sha.
func (s *Store) Archive(ctx context.Context, sha string) ([]byte, error) {
	sum, err := shaBytes(sha)
	if err != nil {
		return nil, ErrNotFound
	}
	var b []byte
	err = s.DB.Pool.QueryRow(ctx, `SELECT archive FROM ui_themes WHERE sha256 = $1`, sum).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// Info returns the description of one stored archive.
func (s *Store) Info(ctx context.Context, sha string) (Info, error) {
	all, err := s.List(ctx)
	if err != nil {
		return Info{}, err
	}
	for _, i := range all {
		if i.SHA256 == sha {
			return i, nil
		}
	}
	return Info{}, ErrNotFound
}

// List describes every stored archive, newest first.
func (s *Store) List(ctx context.Context) ([]Info, error) {
	rows, err := s.DB.Pool.Query(ctx, `
		SELECT encode(sha256, 'hex'), name, title, version, octet_length(archive), origin, uploaded_by, uploaded_at
		FROM ui_themes ORDER BY uploaded_at DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Info{}
	for rows.Next() {
		var i Info
		if err := rows.Scan(&i.SHA256, &i.Name, &i.Title, &i.Version, &i.Size, &i.Origin, &i.UploadedBy, &i.UploadedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Selected returns the administrator's selection, or nil when there is none.
func (s *Store) Selected(ctx context.Context) (*Selection, error) {
	var sel Selection
	var sha []byte
	var builtin *string
	err := s.DB.Pool.QueryRow(ctx, `SELECT sha256, builtin, set_by, set_at FROM ui_theme_active`).Scan(&sha, &builtin, &sel.SetBy, &sel.SetAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if builtin != nil {
		sel.Builtin = *builtin
	}
	if sha != nil {
		sel.SHA256 = hex.EncodeToString(sha)
	}
	return &sel, nil
}

// Select records the choice and announces it, in one transaction: a replica
// that hears the notification reads the new row.
func (s *Store) Select(ctx context.Context, sel Selection) error {
	var sha []byte
	var builtin *string
	switch {
	case sel.Builtin != "" && sel.SHA256 == "":
		builtin = &sel.Builtin
	case sel.SHA256 != "" && sel.Builtin == "":
		var err error
		if sha, err = shaBytes(sel.SHA256); err != nil {
			return ErrNotFound
		}
	default:
		return errors.New("theme: a selection names a built-in theme or a stored one")
	}
	err := s.DB.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ui_theme_active (singleton, sha256, builtin, set_by, set_at) VALUES (true, $1, $2, $3, now())
			ON CONFLICT (singleton) DO UPDATE SET sha256 = EXCLUDED.sha256, builtin = EXCLUDED.builtin,
				set_by = EXCLUDED.set_by, set_at = EXCLUDED.set_at`, sha, builtin, sel.SetBy); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT pg_notify($1, '')`, Channel)
		return err
	})
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23503" { // foreign_key_violation: no such archive
		return ErrNotFound
	}
	return err
}

// Clear drops the selection: the deployment default is back in force.
func (s *Store) Clear(ctx context.Context) error {
	return s.DB.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM ui_theme_active`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT pg_notify($1, '')`, Channel)
		return err
	})
}

// Delete removes a stored archive that is not selected.
func (s *Store) Delete(ctx context.Context, sha string) error {
	sum, err := shaBytes(sha)
	if err != nil {
		return ErrNotFound
	}
	return s.DB.InTx(ctx, func(tx pgx.Tx) error {
		var selected bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ui_theme_active WHERE sha256 = $1)`, sum).Scan(&selected); err != nil {
			return err
		}
		if selected {
			return ErrActive
		}
		tag, err := tx.Exec(ctx, `DELETE FROM ui_themes WHERE sha256 = $1`, sum)
		var pg *pgconn.PgError
		switch {
		case errors.As(err, &pg) && pg.Code == "23503": // selected between the check and the delete
			return ErrActive
		case err != nil:
			return err
		case tag.RowsAffected() == 0:
			return ErrNotFound
		}
		return nil
	})
}
