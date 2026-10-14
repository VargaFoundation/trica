package mcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
)

func dispatchFixture(t *testing.T) (*Server, *api.MemoryAuditor, api.Principal) {
	t.Helper()
	reg := schema.Builtin()
	eng := &reconcile.Engine{Store: &store.Store{}, Schema: func() *schema.Registry { return reg }}
	bob := api.Principal{Subject: "bob", Kind: api.KindUser}
	aud := &api.MemoryAuditor{}
	s, err := New(Config{Engine: eng, Auditor: aud, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authorizer: &api.StaticAuthorizer{Tokens: map[string]api.Grant{"t": {Principal: bob, Actions: []string{api.ActionRead}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return s, aud, bob
}

func TestDispatch(t *testing.T) {
	s, aud, bob := dispatchFixture(t)
	as := func(p api.Principal, err error) resolver {
		return func(*gosdk.CallToolRequest) (api.Principal, error) { return p, err }
	}
	run := func(r resolver, action string, h func(*tc) (any, error)) (any, error) {
		return s.dispatch(context.Background(), nil, r, "probe", action, map[string]any{"q": "x"}, h)
	}

	t.Run("a handler that panics is an internal error, audited, and does not take the server down", func(t *testing.T) {
		out, err := run(as(bob, nil), api.ActionRead, func(*tc) (any, error) { panic("boom: secret detail") })
		if out != nil || err == nil || !strings.HasPrefix(err.Error(), "internal:") || strings.Contains(err.Error(), "secret detail") {
			t.Fatalf("out %v, err %v", out, err)
		}
		e := aud.Entries()[len(aud.Entries())-1]
		if e.Outcome != api.OutcomeError || e.Details["reason"] != "panic" || e.Action != "mcp.tool.probe" {
			t.Fatalf("audit %+v", e)
		}
	})
	t.Run("an unauthenticated call never reaches the handler, and is audited as denied", func(t *testing.T) {
		reached := false
		_, err := run(as(api.Principal{}, errors.New("no credential")), api.ActionRead, func(*tc) (any, error) { reached = true; return nil, nil })
		if reached || err == nil || !strings.HasPrefix(err.Error(), "unauthenticated:") {
			t.Fatalf("reached %v, err %v", reached, err)
		}
		e := aud.Entries()[len(aud.Entries())-1]
		if e.Outcome != api.OutcomeDenied || e.Details["reason"] != "unauthenticated" {
			t.Fatalf("audit %+v", e)
		}
	})
	t.Run("an action the caller does not hold never reaches the handler", func(t *testing.T) {
		reached := false
		_, err := run(as(bob, nil), api.ActionApprove, func(*tc) (any, error) { reached = true; return nil, nil })
		if reached || err == nil || !strings.HasPrefix(err.Error(), "forbidden:") {
			t.Fatalf("reached %v, err %v", reached, err)
		}
		if e := aud.Entries()[len(aud.Entries())-1]; e.Outcome != api.OutcomeDenied || e.Actor != "bob" {
			t.Fatalf("audit %+v", e)
		}
	})
	t.Run("a success is audited with its target and its arguments", func(t *testing.T) {
		out, err := run(as(bob, nil), api.ActionRead, func(t *tc) (any, error) { t.target = "obj-1"; return "fine", nil })
		if err != nil || out != "fine" {
			t.Fatalf("out %v, err %v", out, err)
		}
		e := aud.Entries()[len(aud.Entries())-1]
		if e.Outcome != api.OutcomeOK || e.Target != "obj-1" || e.Details["args"] == nil {
			t.Fatalf("audit %+v", e)
		}
	})
	t.Run("a forbidden error is audited as denied, any other as an error, and the detail of an internal one stays out of the answer", func(t *testing.T) {
		_, err := run(as(bob, nil), api.ActionRead, func(*tc) (any, error) { return nil, api.Forbidden("not yours") })
		if err == nil || !strings.HasPrefix(err.Error(), "forbidden:") || aud.Entries()[len(aud.Entries())-1].Outcome != api.OutcomeDenied {
			t.Fatalf("err %v", err)
		}
		_, err = run(as(bob, nil), api.ActionRead, func(*tc) (any, error) { return nil, api.BadRequest("nope") })
		if err == nil || !strings.HasPrefix(err.Error(), "invalid-request:") || aud.Entries()[len(aud.Entries())-1].Outcome != api.OutcomeError {
			t.Fatalf("err %v", err)
		}
		_, err = run(as(bob, nil), api.ActionRead, func(*tc) (any, error) { return nil, errors.New("pq: relation \"claims\" does not exist") })
		if err == nil || strings.Contains(err.Error(), "claims") {
			t.Fatalf("an internal error leaked: %v", err)
		}
	})
}

// The write handler refuses a caller without the propose action even when it
// is reached without going through dispatch. Two checks stand in the way (the
// handler's own, then the batch check); the refusal is the same, so removing
// one of them alone cannot be seen from outside: this pins the pair.
func TestProposeChecksTheActionItself(t *testing.T) {
	s, _, bob := dispatchFixture(t) // bob holds read only
	call := &tc{s: s, ctx: context.Background(), p: bob, g: s.api.Guard(context.Background(), bob), details: map[string]any{}}
	_, err := s.toolPropose(call, ProposeInput{Reason: "r", Objects: []*ProposedObject{{Ref: "a", Type: "device", Identity: map[string]string{"name": "x"}}}})
	if err == nil || !strings.Contains(err.Error(), `the "propose" action is not granted`) {
		t.Fatalf("err %v", err)
	}
}
