package transport

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/VargaFoundation/trica/internal/pki"
)

// ErrInvalidToken is returned for an enrolment token that is unknown, already
// used or expired. The three cases are deliberately not told apart: the
// caller learns nothing about which tokens exist.
var ErrInvalidToken = errors.New("transport: invalid enrolment token")

// DefaultTokenTTL is the validity of an enrolment token created without an
// explicit lifetime.
const DefaultTokenTTL = time.Hour

// Registry is the PostgreSQL side of the transport: enrolment tokens, issued
// certificates and the revocation list (migration 00003). It touches no
// inventory table.
type Registry struct {
	pool *pgxpool.Pool
	// Now is the clock; time.Now when nil. Tests inject their own.
	Now func() time.Time
}

// NewRegistry returns a registry on pool.
func NewRegistry(pool *pgxpool.Pool) *Registry { return &Registry{pool: pool} }

func (r *Registry) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// NewEnrolmentToken creates a one-time token granting binding (for example
// "agent:host-01", see [pki.Allows]) and valid for ttl (DefaultTokenTTL when
// ttl <= 0). It returns the token in clear: this is the only moment it exists
// outside the client; only its sha256 is stored. An operator hands it to the
// machine to enrol, over a channel of their choice, together with the CA
// certificate.
func (r *Registry) NewEnrolmentToken(ctx context.Context, binding string, ttl time.Duration) (string, error) {
	if err := pki.ValidBinding(binding); err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("transport: token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := r.now()
	_, err := r.pool.Exec(ctx,
		`INSERT INTO enrol_tokens (token_hash, binding, created_at, expires_at) VALUES ($1, $2, $3, $4)`,
		hashToken(token), binding, now, now.Add(ttl))
	if err != nil {
		return "", fmt.Errorf("transport: store token: %w", err)
	}
	return token, nil
}

// enrol consumes token and, in the same transaction, signs with issue: a
// token is marked used only if the certificate was produced, and two
// concurrent uses of one token cannot both succeed (the UPDATE is the lock).
func (r *Registry) enrol(ctx context.Context, token string, issue func(binding string) (*x509.Certificate, error)) (*x509.Certificate, error) {
	var cert *x509.Certificate
	now := r.now()
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var binding string
		err := tx.QueryRow(ctx,
			`UPDATE enrol_tokens SET used_at = $2
			  WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2
			  RETURNING binding`, hashToken(token), now).Scan(&binding)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return fmt.Errorf("transport: consume token: %w", err)
		}
		cert, err = issue(binding)
		if err != nil {
			return err
		}
		return recordIssued(ctx, tx, cert, binding, now)
	})
	if err != nil {
		return nil, err
	}
	return cert, nil
}

// renew signs with issue and records the certificate.
func (r *Registry) renew(ctx context.Context, binding string, issue func() (*x509.Certificate, error)) (*x509.Certificate, error) {
	cert, err := issue()
	if err != nil {
		return nil, err
	}
	if err := recordIssued(ctx, r.pool, cert, binding, r.now()); err != nil {
		return nil, err
	}
	return cert, nil
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func recordIssued(ctx context.Context, q execer, cert *x509.Certificate, binding string, now time.Time) error {
	_, err := q.Exec(ctx,
		`INSERT INTO issued_certs (serial, binding, not_before, not_after, issued_at) VALUES ($1, $2, $3, $4, $5)`,
		pki.SerialString(cert), binding, cert.NotBefore, cert.NotAfter, now)
	if err != nil {
		return fmt.Errorf("transport: record certificate: %w", err)
	}
	return nil
}

// Revoke adds a certificate serial (lowercase hex, as listed by
// [Registry.IssuedTo]) to the revocation list. Revoking twice is not an error.
// Every request is checked against the list, so the effect is immediate.
func (r *Registry) Revoke(ctx context.Context, serial, reason string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO revoked_certs (serial, reason, revoked_at) VALUES ($1, $2, $3) ON CONFLICT (serial) DO NOTHING`,
		serial, reason, r.now())
	if err != nil {
		return fmt.Errorf("transport: revoke: %w", err)
	}
	return nil
}

// RevokeBinding revokes every certificate issued for binding that has not
// expired yet, and returns how many it added to the list.
func (r *Registry) RevokeBinding(ctx context.Context, binding, reason string) (int, error) {
	now := r.now()
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO revoked_certs (serial, reason, revoked_at)
		 SELECT serial, $2, $3 FROM issued_certs WHERE binding = $1 AND not_after > $3
		 ON CONFLICT (serial) DO NOTHING`, binding, reason, now)
	if err != nil {
		return 0, fmt.Errorf("transport: revoke binding: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// IsRevoked reports whether serial is on the revocation list.
func (r *Registry) IsRevoked(ctx context.Context, serial string) (bool, error) {
	var revoked bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM revoked_certs WHERE serial = $1)`, serial).Scan(&revoked)
	if err != nil {
		return false, fmt.Errorf("transport: revocation check: %w", err)
	}
	return revoked, nil
}

// IssuedTo lists the serials of the certificates issued for binding, newest
// first: what an operator needs to revoke one.
func (r *Registry) IssuedTo(ctx context.Context, binding string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT serial FROM issued_certs WHERE binding = $1 ORDER BY issued_at DESC, serial`, binding)
	if err != nil {
		return nil, fmt.Errorf("transport: list certificates: %w", err)
	}
	serials, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("transport: list certificates: %w", err)
	}
	return serials, nil
}
