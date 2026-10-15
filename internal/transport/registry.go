package transport

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
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

// ErrBindingRefused is returned when the binding a client asks for is not one
// its token can grant: another binding than a one-time token's, or not one
// segment below a class token's prefix. The token is not consumed.
var ErrBindingRefused = errors.New("transport: binding not granted by this token")

// ErrBindingInUse is returned when a class token is asked for a binding that
// an unexpired, unrevoked certificate already holds: the class token mints a
// name once, so its holder cannot take over a node that is enrolled. Revoking
// that node's certificates frees the name.
var ErrBindingInUse = errors.New("transport: binding already enrolled")

// classLockSpace is the first key of the transaction advisory lock that
// serialises the class enrolments of one binding. The two-key form does not
// overlap the one-key locks of the deducer, the engine and the audit log.
const classLockSpace = 7426300

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

// tokenIDLen is the number of bytes of the token hash an operator names a
// token by: enough to be unique, useless to enrol.
const tokenIDLen = 8

// TokenID is how an operator names token (to revoke it): the start of its
// sha256, in hex. It is printed when the token is created and logged on each
// enrolment, and gives nothing to enrol with.
func TokenID(token string) string { return hex.EncodeToString(hashToken(token)[:tokenIDLen]) }

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
	return r.newToken(ctx, binding, false, ttl)
}

// NewClassEnrolmentToken creates a token that is not consumed (ADR 0035):
// until it expires or is revoked, each use mints the binding one segment below
// prefix that the client asks for ("agent" mints "agent:<hostname>"), once per
// binding, as long as no unexpired, unrevoked certificate holds it. It is what
// a DaemonSet shares. Whoever holds it can enrol any name not enrolled yet
// under prefix: give it the lifetime of a rollout, and revoke it after.
func (r *Registry) NewClassEnrolmentToken(ctx context.Context, prefix string, ttl time.Duration) (string, error) {
	if err := pki.ValidPrefix(prefix); err != nil {
		return "", err
	}
	return r.newToken(ctx, prefix, true, ttl)
}

func (r *Registry) newToken(ctx context.Context, binding string, class bool, ttl time.Duration) (string, error) {
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
		`INSERT INTO enrol_tokens (token_hash, binding, class, created_at, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		hashToken(token), binding, class, now, now.Add(ttl))
	if err != nil {
		return "", fmt.Errorf("transport: store token: %w", err)
	}
	return token, nil
}

// RevokeEnrolmentToken makes the token named id (see [TokenID]) unusable from
// now on; certificates it already minted are not touched (revoke those by
// binding). It reports whether a token still usable was found.
func (r *Registry) RevokeEnrolmentToken(ctx context.Context, id string) (bool, error) {
	prefix, err := hex.DecodeString(id)
	if err != nil || len(prefix) != tokenIDLen {
		return false, fmt.Errorf("transport: invalid token id %q (want %d hex digits)", id, 2*tokenIDLen)
	}
	now := r.now()
	tag, err := r.pool.Exec(ctx,
		`UPDATE enrol_tokens SET expires_at = $2
		  WHERE substring(token_hash FROM 1 FOR $3) = $1 AND used_at IS NULL AND expires_at > $2`,
		prefix, now, tokenIDLen)
	if err != nil {
		return false, fmt.Errorf("transport: revoke token: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// enrol uses token to sign with issue, in one transaction.
//
// A one-time token is consumed: it is marked used only if the certificate was
// produced, and two concurrent uses cannot both succeed (the UPDATE is the
// lock). It grants its binding; a client that asks for another (requested
// not empty) is refused before anything is consumed.
//
// A class token is not consumed. The client must ask for a binding one segment
// below its prefix, which is refused while an unexpired, unrevoked
// certificate holds it; an advisory lock on the binding makes two concurrent
// enrolments of one name take turns, so only the first gets it.
func (r *Registry) enrol(ctx context.Context, token, requested string, issue func(binding string) (*x509.Certificate, error)) (*x509.Certificate, error) {
	var cert *x509.Certificate
	now := r.now()
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var granted string
		var class bool
		err := tx.QueryRow(ctx,
			`SELECT binding, class FROM enrol_tokens
			  WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2
			  FOR UPDATE`, hashToken(token), now).Scan(&granted, &class)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return fmt.Errorf("transport: read token: %w", err)
		}
		binding := granted
		if class {
			if pki.UnderPrefix(granted, requested) != nil {
				return ErrBindingRefused
			}
			binding = requested
			if err := lockBinding(ctx, tx, binding, now); err != nil {
				return err
			}
		} else {
			if requested != "" && requested != granted {
				return ErrBindingRefused
			}
			if _, err := tx.Exec(ctx, `UPDATE enrol_tokens SET used_at = $2 WHERE token_hash = $1`, hashToken(token), now); err != nil {
				return fmt.Errorf("transport: consume token: %w", err)
			}
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

// lockBinding takes the class enrolment lock of binding until the end of tx,
// then refuses the binding if a certificate still valid at now holds it.
func lockBinding(ctx context.Context, tx pgx.Tx, binding string, now time.Time) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, hashtext($2))`, classLockSpace, binding); err != nil {
		return fmt.Errorf("transport: lock binding: %w", err)
	}
	var held bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM issued_certs c
		                 WHERE c.binding = $1 AND c.not_after > $2
		                   AND NOT EXISTS (SELECT 1 FROM revoked_certs r WHERE r.serial = c.serial))`,
		binding, now).Scan(&held)
	if err != nil {
		return fmt.Errorf("transport: check binding: %w", err)
	}
	if held {
		return ErrBindingInUse
	}
	return nil
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
