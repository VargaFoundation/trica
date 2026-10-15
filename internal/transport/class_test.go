package transport

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/pki"
)

// issuer returns an issue function for Registry.enrol that signs a fresh CSR
// with the env's CA, valid ttl from the registry's clock, after pause (to
// widen the window between the check and the commit).
func (e *env) issuer(ttl, pause time.Duration) func(string) (*x509.Certificate, error) {
	return func(binding string) (*x509.Certificate, error) {
		key, err := pki.GenerateKey()
		if err != nil {
			return nil, err
		}
		p, err := pki.NewCSR(key)
		if err != nil {
			return nil, err
		}
		csr, err := pki.ParseCSRPEM(p)
		if err != nil {
			return nil, err
		}
		time.Sleep(pause)
		return e.ca.IssueClient(csr, binding, ttl, e.reg.now())
	}
}

func TestClassTokenIsStoredAsAPrefixAndNotConsumed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, p := range []string{"", "agent:", "agent:*"} {
		if _, err := e.reg.NewClassEnrolmentToken(ctx, p, time.Hour); err == nil {
			t.Errorf("prefix %q got a class token", p)
		}
	}
	token, err := e.reg.NewClassEnrolmentToken(ctx, "agent", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"node-1", "node-2", "node-3"} {
		if _, err := e.reg.enrol(ctx, token, "agent:"+n, e.issuer(time.Hour, 0)); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	var binding string
	var class, used bool
	if err := e.pool.QueryRow(ctx, `SELECT binding, class, used_at IS NOT NULL FROM enrol_tokens`).Scan(&binding, &class, &used); err != nil {
		t.Fatal(err)
	}
	if binding != "agent" || !class || used {
		t.Fatalf("stored binding %q, class %v, used %v", binding, class, used)
	}
	issued, err := e.reg.IssuedTo(ctx, "agent:node-2")
	if err != nil || len(issued) != 1 {
		t.Fatalf("issued to agent:node-2: %v, %v", issued, err)
	}

	e.regTime.Advance(time.Hour + time.Second)
	if _, err := e.reg.enrol(ctx, token, "agent:node-4", e.issuer(time.Hour, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("an expired class token: %v", err)
	}
}

// Once the certificate of a name has expired (the node is gone for good), the
// class token can enrol the name again: the node was reinstalled.
func TestClassTokenFreesANameWhoseCertificateExpired(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	token, err := e.reg.NewClassEnrolmentToken(ctx, "agent", 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.reg.enrol(ctx, token, "agent:node-1", e.issuer(time.Hour, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reg.enrol(ctx, token, "agent:node-1", e.issuer(time.Hour, 0)); !errors.Is(err, ErrBindingInUse) {
		t.Fatalf("a name held by a valid certificate: %v", err)
	}
	e.regTime.Advance(2 * time.Hour)
	if _, err := e.reg.enrol(ctx, token, "agent:node-1", e.issuer(time.Hour, 0)); err != nil {
		t.Fatalf("a name whose certificate expired: %v", err)
	}
}

// Two class tokens of one prefix (two rollouts, two operators) used at once
// for one name: one enrolment wins, the other sees the name taken. The row
// lock of a token does not cover this, the advisory lock on the binding does.
func TestConcurrentClassEnrolmentsOfOneName(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var tokens []string
	for range 2 {
		tok, err := e.reg.NewClassEnrolmentToken(ctx, "agent", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, tok)
	}
	for round := range 5 {
		binding := fmt.Sprintf("agent:race-%d", round)
		errs := make([]error, len(tokens))
		var wg sync.WaitGroup
		for i, tok := range tokens {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = e.reg.enrol(ctx, tok, binding, e.issuer(time.Hour, 100*time.Millisecond))
			}()
		}
		wg.Wait()
		ok, inUse := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrBindingInUse):
				inUse++
			default:
				t.Fatalf("%s: %v", binding, err)
			}
		}
		if ok != 1 || inUse != 1 {
			t.Fatalf("%s: %d enrolled, %d refused, want one of each", binding, ok, inUse)
		}
	}
}

func TestRevokeEnrolmentToken(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	once, err := e.reg.NewEnrolmentToken(ctx, "agent:host-01", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	class, err := e.reg.NewClassEnrolmentToken(ctx, "agent", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if id := TokenID(once); len(id) != 16 || strings.Contains(once, id) {
		t.Fatalf("token id %q", id)
	}
	for _, id := range []string{"", "zz", "0123", strings.Repeat("0", 17), strings.Repeat("g", 16)} {
		if _, err := e.reg.RevokeEnrolmentToken(ctx, id); err == nil {
			t.Errorf("id %q accepted", id)
		}
	}
	if found, err := e.reg.RevokeEnrolmentToken(ctx, strings.Repeat("0", 16)); err != nil || found {
		t.Fatalf("an unknown id: %v, %v", found, err)
	}
	for _, tok := range []string{once, class} {
		if found, err := e.reg.RevokeEnrolmentToken(ctx, TokenID(tok)); err != nil || !found {
			t.Fatalf("revoke: %v, %v", found, err)
		}
	}
	if _, err := e.reg.enrol(ctx, once, "", e.issuer(time.Hour, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a revoked one-time token: %v", err)
	}
	if _, err := e.reg.enrol(ctx, class, "agent:node-1", e.issuer(time.Hour, 0)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a revoked class token: %v", err)
	}
	if found, err := e.reg.RevokeEnrolmentToken(ctx, TokenID(class)); err != nil || found {
		t.Fatalf("revoked twice: %v, %v", found, err)
	}
}

// The server logs who enrolled with which token by its id, never the token.
func TestClassEnrolmentLogsTheTokenID(t *testing.T) {
	e := newEnv(t)
	e.start()
	ctx := context.Background()
	token, err := e.reg.NewClassEnrolmentToken(ctx, "agent", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cfg := e.clientCfg("agent:unused")
	cfg.EnrolToken, cfg.Binding = token, "agent:node-1"
	c := e.clientFrom(cfg)
	if err := c.Emit(ctx, batch("agent:node-1")); err != nil {
		t.Fatal(err)
	}
	cfg.Dir = t.TempDir()
	if err := e.clientFrom(cfg).Emit(ctx, batch("agent:node-1")); err == nil {
		t.Fatal("a second node-1 enrolled")
	}
	logs := e.logs.String()
	if strings.Contains(logs, token) || strings.Count(logs, TokenID(token)) != 2 {
		t.Fatalf("logs: %s", logs)
	}
}
