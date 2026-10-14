package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)} }

func newTokenStore(t *testing.T) (*TokenStore, *clock) {
	t.Helper()
	c := newClock()
	return &TokenStore{Store: testdb.New(t), Policy: DefaultPolicy(), Now: c.now}, c
}

func TestTokenCreateAuthenticate(t *testing.T) {
	ts, _ := newTokenStore(t)
	ctx := context.Background()
	owner := user("operator")
	secret, tok, err := ts.Create(ctx, owner, CreateParams{Name: "ci", Scopes: []string{ScopeWrite}, Types: []string{"device"}, Sites: []string{"paris-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, TokenPrefix) {
		t.Fatalf("token %q has no %s prefix", secret, TokenPrefix)
	}
	p, err := ts.Authenticate(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != KindToken || p.Subject != "token:"+itoa(tok.ID) || p.Owner != "user:alice" {
		t.Errorf("principal = %+v", p)
	}
	if len(p.Roles) != 1 || p.Roles[0] != "operator" || len(p.Scopes) != 1 || p.Scopes[0] != ScopeWrite {
		t.Errorf("roles/scopes = %v/%v", p.Roles, p.Scopes)
	}
	if len(p.Types) != 1 || p.Types[0] != "device" || len(p.Sites) != 1 || p.Sites[0] != "paris-1" {
		t.Errorf("restrictions = %v/%v", p.Types, p.Sites)
	}
	// The principal built from the token is restricted by the policy.
	if !ts.Policy.Allow(p, ActionPropose, "device", "paris-1") || ts.Policy.Allow(p, ActionPropose, "rack", "paris-1") {
		t.Error("token restrictions not enforced")
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestTokenStoresOnlyAHash(t *testing.T) {
	ts, _ := newTokenStore(t)
	ctx := context.Background()
	raw, tok, err := ts.Create(ctx, user("admin"), CreateParams{Name: "x", Scopes: []string{ScopeRead}})
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.SplitN(strings.TrimPrefix(raw, TokenPrefix), "_", 2)[1]
	if len(secret) < 43 { // 256 bits in base64url
		t.Errorf("secret is only %d characters", len(secret))
	}
	var dump string
	err = ts.Store.Pool.QueryRow(ctx, `SELECT row(api_tokens.*)::text FROM api_tokens WHERE id = $1`, tok.ID).Scan(&dump)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, secret) || strings.Contains(dump, raw) {
		t.Fatal("the secret is stored in clear")
	}
	var hash []byte
	if err := ts.Store.Pool.QueryRow(ctx, `SELECT hash FROM api_tokens WHERE id = $1`, tok.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(secret))
	if !bytes.Equal(hash, want[:]) {
		t.Error("stored hash is not the SHA-256 of the secret")
	}
	// Two tokens never share a secret.
	raw2, _, _ := ts.Create(ctx, user("admin"), CreateParams{Name: "y", Scopes: []string{ScopeRead}})
	if raw == raw2 {
		t.Error("two tokens with the same value")
	}
}

func TestTokenRejected(t *testing.T) {
	ts, clk := newTokenStore(t)
	ctx := context.Background()
	exp := clk.t.Add(time.Hour)
	raw, tok, err := ts.Create(ctx, user("admin"), CreateParams{Name: "x", Scopes: []string{ScopeRead}, ExpiresAt: &exp})
	if err != nil {
		t.Fatal(err)
	}
	prefix := TokenPrefix + itoa(tok.ID) + "_"

	if _, err := ts.Authenticate(ctx, raw); err != nil {
		t.Fatalf("fresh token refused: %v", err)
	}
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"wrong secret", prefix + strings.Repeat("A", 43), ErrTokenUnknown},
		{"unknown id", TokenPrefix + "99999_" + strings.TrimPrefix(raw, prefix), ErrTokenUnknown},
		{"no prefix", strings.TrimPrefix(raw, TokenPrefix), ErrTokenMalformed},
		{"no secret", prefix, ErrTokenMalformed},
		{"no separator", TokenPrefix + "12", ErrTokenMalformed},
		{"non numeric id", TokenPrefix + "abc_secret", ErrTokenMalformed},
		{"zero id", TokenPrefix + "0_secret", ErrTokenMalformed},
		{"empty", "", ErrTokenMalformed},
	}
	for _, c := range cases {
		if _, err := ts.Authenticate(ctx, c.raw); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}

	// Expiry.
	clk.advance(2 * time.Hour)
	if _, err := ts.Authenticate(ctx, raw); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("expired token: err = %v", err)
	}
}

func TestTokenExpiresExactlyAtExpiry(t *testing.T) {
	ts, clk := newTokenStore(t)
	ctx := context.Background()
	exp := clk.t.Add(time.Hour)
	raw, _, _ := ts.Create(ctx, user("admin"), CreateParams{Name: "x", Scopes: []string{ScopeRead}, ExpiresAt: &exp})
	clk.advance(time.Hour - time.Second)
	if _, err := ts.Authenticate(ctx, raw); err != nil {
		t.Fatalf("one second before expiry: %v", err)
	}
	clk.advance(time.Second)
	if _, err := ts.Authenticate(ctx, raw); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("at expiry: %v", err)
	}
}

func TestTokenRevocation(t *testing.T) {
	ts, _ := newTokenStore(t)
	ctx := context.Background()
	raw, tok, _ := ts.Create(ctx, user("admin"), CreateParams{Name: "x", Scopes: []string{ScopeRead}})
	if _, err := ts.Authenticate(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if err := ts.Revoke(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Authenticate(ctx, raw); !errors.Is(err, ErrTokenRevoked) {
		t.Errorf("revoked token: err = %v", err)
	}
	if err := ts.Revoke(ctx, tok.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second revoke: %v", err)
	}
	if err := ts.Revoke(ctx, 424242); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("revoke unknown: %v", err)
	}
	list, err := ts.List(ctx)
	if err != nil || len(list) != 1 || list[0].RevokedAt == nil {
		t.Errorf("list = %+v, %v", list, err)
	}
}

func TestTokenLastUsed(t *testing.T) {
	ts, clk := newTokenStore(t)
	ctx := context.Background()
	raw, tok, _ := ts.Create(ctx, user("admin"), CreateParams{Name: "x", Scopes: []string{ScopeRead}})
	lastUsed := func() *time.Time {
		list, err := ts.List(ctx)
		if err != nil || len(list) != 1 || list[0].ID != tok.ID {
			t.Fatalf("list: %v %v", list, err)
		}
		return list[0].LastUsed
	}
	if lastUsed() != nil {
		t.Fatal("last_used set before any use")
	}
	if _, err := ts.Authenticate(ctx, raw); err != nil {
		t.Fatal(err)
	}
	first := lastUsed()
	if first == nil || !first.Equal(clk.t) {
		t.Fatalf("last_used = %v, want %v", first, clk.t)
	}
	// Within the granularity the row is not rewritten; after it, it is.
	clk.advance(10 * time.Second)
	_, _ = ts.Authenticate(ctx, raw)
	if got := lastUsed(); !got.Equal(*first) {
		t.Errorf("last_used rewritten within the granularity: %v", got)
	}
	clk.advance(2 * time.Minute)
	_, _ = ts.Authenticate(ctx, raw)
	if got := lastUsed(); !got.Equal(clk.t) {
		t.Errorf("last_used = %v, want %v", got, clk.t)
	}
}

func TestTokenCreateGuards(t *testing.T) {
	ts, clk := newTokenStore(t)
	ctx := context.Background()
	past := clk.t.Add(-time.Minute)
	cases := []struct {
		name  string
		owner *Principal
		p     CreateParams
		want  error
	}{
		{"scope exceeds owner role (viewer asks write)", user("viewer"), CreateParams{Name: "x", Scopes: []string{ScopeWrite}}, ErrScopeExceedsRole},
		{"scope exceeds owner role (operator asks admin)", user("operator"), CreateParams{Name: "x", Scopes: []string{ScopeAdmin}}, ErrScopeExceedsRole},
		{"scope exceeds owner role (no role)", user(), CreateParams{Name: "x", Scopes: []string{ScopeRead}}, ErrScopeExceedsRole},
		{"one scope too many", user("operator"), CreateParams{Name: "x", Scopes: []string{ScopeRead, ScopeAdmin}}, ErrScopeExceedsRole},
		{"unknown scope", user("admin"), CreateParams{Name: "x", Scopes: []string{"root"}}, ErrInvalidToken},
		{"no scope", user("admin"), CreateParams{Name: "x"}, ErrInvalidToken},
		{"no name", user("admin"), CreateParams{Scopes: []string{ScopeRead}}, ErrInvalidToken},
		{"expiry in the past", user("admin"), CreateParams{Name: "x", Scopes: []string{ScopeRead}, ExpiresAt: &past}, ErrInvalidToken},
		{"owner is a token", tokenP([]string{"admin"}, []string{ScopeAdmin}), CreateParams{Name: "x", Scopes: []string{ScopeRead}}, ErrForbidden},
		{"nil owner", nil, CreateParams{Name: "x", Scopes: []string{ScopeRead}}, ErrForbidden},
	}
	for _, c := range cases {
		if _, _, err := ts.Create(ctx, c.owner, c.p); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if list, _ := ts.List(ctx); len(list) != 0 {
		t.Errorf("%d tokens were stored by refused creations", len(list))
	}
	// A viewer may still create a read token.
	if _, _, err := ts.Create(ctx, user("viewer"), CreateParams{Name: "x", Scopes: []string{ScopeRead}}); err != nil {
		t.Errorf("viewer read token: %v", err)
	}
}

// A token row whose scopes are wider than its owner's roles (hand-edited, or an owner whose
// role is lower than the scope) still cannot do more than the roles allow.
func TestTokenWiderThanOwnerIsClamped(t *testing.T) {
	ts, _ := newTokenStore(t)
	ctx := context.Background()
	raw, tok, _ := ts.Create(ctx, user("viewer"), CreateParams{Name: "x", Scopes: []string{ScopeRead}})
	if _, err := ts.Store.Pool.Exec(ctx, `UPDATE api_tokens SET scopes = '{admin}' WHERE id = $1`, tok.ID); err != nil {
		t.Fatal(err)
	}
	p, err := ts.Authenticate(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Policy.Allow(p, ActionAdmin, "device", "s") || ts.Policy.Allow(p, ActionPropose, "device", "s") {
		t.Error("an admin-scoped token owned by a viewer acted beyond the viewer role")
	}
	if !ts.Policy.Allow(p, ActionRead, "device", "s") {
		t.Error("it should still read")
	}
}

func TestTokenScopesConstraint(t *testing.T) {
	ts, _ := newTokenStore(t)
	_, err := ts.Store.Pool.Exec(context.Background(),
		`INSERT INTO api_tokens (name, hash, scopes, created_by) VALUES ('x', '\x01', '{root}', 'u')`)
	if err == nil {
		t.Fatal("the database accepted an unknown scope")
	}
}
