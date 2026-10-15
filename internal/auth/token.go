package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/store"
)

// TokenPrefix identifies a trica API token, so a leaked one is recognisable by secret
// scanners and a bearer value can be routed without parsing it as a JWT.
const TokenPrefix = "trica_"

// Token errors. Authentication maps all of them to the same 401 for the client; the
// distinction is for the audit log.
var (
	ErrTokenMalformed = errors.New("auth: malformed API token")
	ErrTokenUnknown   = errors.New("auth: unknown API token")
	ErrTokenRevoked   = errors.New("auth: API token revoked")
	ErrTokenExpired   = errors.New("auth: API token expired")
	// ErrScopeExceedsRole means the creator's roles do not cover a requested scope.
	ErrScopeExceedsRole = errors.New("auth: token scope exceeds the owner's roles")
	ErrInvalidToken     = errors.New("auth: invalid token parameters")
)

// reservedSourceKinds cannot be bound to a token: "user" is how people's batches are
// attributed, and "agent" sources are bound by a certificate on the transport (ADR 0027). A
// token speaking as either would pass for a person or for a host.
var reservedSourceKinds = []string{"user", "agent"}

// secretBytes is the size of the random secret: 256 bits.
const secretBytes = 32

// lastUsedGranularity bounds the write load: last_used is refreshed at most this often.
const lastUsedGranularity = time.Minute

// Token is the stored metadata of an API token. The secret is never stored.
type Token struct {
	ID        int64
	Name      string
	Scopes    []string
	Types     []string
	Sites     []string
	Roles     []string // the owner's roles when the token was created
	Source    string   // the source its batches are attributed to; empty = derived from the id
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt *time.Time
	LastUsed  *time.Time
	RevokedAt *time.Time
}

// CreateParams describes a token to create.
type CreateParams struct {
	Name   string
	Scopes []string
	Types  []string // optional restriction, empty = all types
	Sites  []string // optional restriction, empty = all sites
	// Source binds the token to a source ("arista:sw-a1"): its batches are attributed to it
	// and ranked by it (ADR 0004). Optional; see ValidTokenSource.
	Source    string
	ExpiresAt *time.Time
}

// ValidTokenSource checks a source a token may be bound to: the format of a transport binding
// (kind:name, two segments at least) and not a reserved kind.
func ValidTokenSource(source string) error {
	if err := pki.ValidBinding(source); err != nil {
		return fmt.Errorf("%w: source: %w", ErrInvalidToken, err)
	}
	kind, _, _ := strings.Cut(source, ":")
	if slices.Contains(reservedSourceKinds, kind) {
		return fmt.Errorf("%w: source kind %q is reserved", ErrInvalidToken, kind)
	}
	return nil
}

// TokenStore creates, authenticates and revokes API tokens in PostgreSQL.
//
// The secret is 256 random bits, so a plain SHA-256 is enough to store: there is nothing to
// brute-force, and a slow hash would cost every request. Tokens are looked up by the id in
// the token itself and compared in constant time.
type TokenStore struct {
	Store  *store.Store
	Policy *Policy
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

func (s *TokenStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func hashSecret(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// Create issues a token for owner and returns its secret value, shown once: only its hash
// is stored. Only a user can own a token (a token minting tokens would be a way to outlive
// revocation), and the requested scopes must be covered by the owner's roles.
func (s *TokenStore) Create(ctx context.Context, owner *Principal, p CreateParams) (string, *Token, error) {
	if owner == nil || owner.Kind != KindUser {
		return "", nil, fmt.Errorf("%w: only a user can create a token", ErrForbidden)
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Scopes) == 0 {
		return "", nil, fmt.Errorf("%w: a name and at least one scope are required", ErrInvalidToken)
	}
	for _, sc := range p.Scopes {
		if !slices.Contains(Scopes, sc) {
			return "", nil, fmt.Errorf("%w: unknown scope %q", ErrInvalidToken, sc)
		}
		for _, a := range scopeActions[sc] {
			if !s.Policy.CanGrant(owner.Roles, a) {
				return "", nil, fmt.Errorf("%w: scope %q needs action %q", ErrScopeExceedsRole, sc, a)
			}
		}
	}
	if p.Source != "" {
		if err := ValidTokenSource(p.Source); err != nil {
			return "", nil, err
		}
	}
	now := s.now()
	if p.ExpiresAt != nil && !p.ExpiresAt.After(now) {
		return "", nil, fmt.Errorf("%w: expiry is in the past", ErrInvalidToken)
	}
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: random secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	t := &Token{
		Name: p.Name, Scopes: nonNil(p.Scopes), Types: nonNil(p.Types), Sites: nonNil(p.Sites),
		Roles: nonNil(owner.Roles), Source: p.Source, CreatedBy: owner.Subject, CreatedAt: now, ExpiresAt: p.ExpiresAt,
	}
	err := s.Store.Pool.QueryRow(ctx, `
		INSERT INTO api_tokens (name, hash, scopes, types, sites, owner_roles, source, created_by, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10) RETURNING id`,
		t.Name, hashSecret(secret), t.Scopes, t.Types, t.Sites, t.Roles, t.Source, t.CreatedBy, t.CreatedAt, t.ExpiresAt).Scan(&t.ID)
	if err != nil {
		return "", nil, fmt.Errorf("auth: store token: %w", err)
	}
	return TokenPrefix + strconv.FormatInt(t.ID, 10) + "_" + secret, t, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// parseToken splits "trica_<id>_<secret>".
func parseToken(raw string) (int64, string, error) {
	rest, ok := strings.CutPrefix(raw, TokenPrefix)
	if !ok {
		return 0, "", ErrTokenMalformed
	}
	idStr, secret, ok := strings.Cut(rest, "_")
	if !ok || secret == "" {
		return 0, "", ErrTokenMalformed
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return 0, "", ErrTokenMalformed
	}
	return id, secret, nil
}

// Authenticate verifies a raw token value and returns its principal. Expired and revoked
// tokens are refused. A successful use refreshes last_used (at most once a minute).
func (s *TokenStore) Authenticate(ctx context.Context, raw string) (*Principal, error) {
	id, secret, err := parseToken(raw)
	if err != nil {
		return nil, err
	}
	var (
		hash []byte
		t    Token
	)
	err = s.Store.Pool.QueryRow(ctx, selectRows+` WHERE id = $1`, id).Scan(tokenDest(&t, &hash)...)
	if errors.Is(err, pgx.ErrNoRows) {
		// Compare anyway so an unknown id costs the same as a wrong secret.
		subtle.ConstantTimeCompare(hashSecret(secret), make([]byte, sha256.Size))
		return nil, ErrTokenUnknown
	}
	if err != nil {
		return nil, fmt.Errorf("auth: load token: %w", err)
	}
	if subtle.ConstantTimeCompare(hashSecret(secret), hash) != 1 {
		return nil, ErrTokenUnknown
	}
	if t.RevokedAt != nil {
		return nil, ErrTokenRevoked
	}
	now := s.now()
	if t.ExpiresAt != nil && !t.ExpiresAt.After(now) {
		return nil, ErrTokenExpired
	}
	if t.LastUsed == nil || now.Sub(*t.LastUsed) >= lastUsedGranularity {
		if _, err := s.Store.Pool.Exec(ctx, `UPDATE api_tokens SET last_used = $2 WHERE id = $1`, id, now); err != nil {
			return nil, fmt.Errorf("auth: record token use: %w", err)
		}
	}
	return &Principal{
		Subject: "token:" + strconv.FormatInt(t.ID, 10),
		Kind:    KindToken,
		Roles:   t.Roles,
		Scopes:  t.Scopes,
		Types:   t.Types,
		Sites:   t.Sites,
		Source:  t.Source,
		Owner:   t.CreatedBy,
	}, nil
}

// Revoke disables a token immediately (every request checks the database). It returns
// store.ErrNotFound when the token does not exist or is already revoked.
func (s *TokenStore) Revoke(ctx context.Context, id int64) error {
	tag, err := s.Store.Pool.Exec(ctx, `UPDATE api_tokens SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, id, s.now())
	if err != nil {
		return fmt.Errorf("auth: revoke token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// List returns the metadata of every token, newest first. Hashes are never returned.
func (s *TokenStore) List(ctx context.Context) ([]Token, error) {
	rows, err := s.Store.Pool.Query(ctx, selectRows+` ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("auth: list tokens: %w", err)
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		var hash []byte
		if err := rows.Scan(tokenDest(&t, &hash)...); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const selectRows = `SELECT id, name, hash, scopes, types, sites, owner_roles, coalesce(source, ''), created_by, created_at, expires_at, last_used, revoked_at FROM api_tokens`

func tokenDest(t *Token, hash *[]byte) []any {
	return []any{&t.ID, &t.Name, hash, &t.Scopes, &t.Types, &t.Sites, &t.Roles, &t.Source, &t.CreatedBy, &t.CreatedAt, &t.ExpiresAt, &t.LastUsed, &t.RevokedAt}
}
