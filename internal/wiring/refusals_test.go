package wiring_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/audit"
	"github.com/VargaFoundation/trica/internal/store"
)

// auditEntries returns the rig's audit entries, oldest first.
func (r *rig) auditEntries() (*audit.Log, []audit.Entry) {
	r.t.Helper()
	st, err := store.Open(context.Background(), r.url)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(st.Close)
	l := &audit.Log{Store: st}
	es, err := l.List(context.Background(), 1000)
	if err != nil {
		r.t.Fatal(err)
	}
	for i, j := 0, len(es)-1; i < j; i, j = i+1, j-1 {
		es[i], es[j] = es[j], es[i]
	}
	return l, es
}

func find(es []audit.Entry, keep func(audit.Entry) bool) []audit.Entry {
	var out []audit.Entry
	for _, e := range es {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

// The refusals answered in front of the API's and the MCP server's handlers
// are in the audit log (#56); what the handlers record is not recorded twice.
func TestServerAuditsRefusals(t *testing.T) {
	r := newRig(t, "audit: {anchor_interval: 50ms}")
	for _, c := range []struct {
		method, path, token, body string
		want                      int
	}{
		{"GET", "/api/v1/me", "", "", http.StatusUnauthorized},
		{"GET", "/api/v1/me", "trica_1_forged", "", http.StatusUnauthorized},
		{"POST", "/mcp", "", `{}`, http.StatusUnauthorized},
		{"POST", "/api/v1/batches", r.readToken, topology(), http.StatusForbidden},
		{"POST", "/api/v1/batches", r.token, topology(), http.StatusOK},
	} {
		if code, body := r.do(c.method, c.path, c.token, c.body); code != c.want {
			t.Fatalf("%s %s: %d, want %d: %.200s", c.method, c.path, code, c.want, body)
		}
	}
	l, es := r.auditEntries()
	http401 := find(es, func(e audit.Entry) bool { return e.Details["status"] == 401.0 })
	if len(http401) != 3 {
		t.Fatalf("%d entries for the three 401s: %+v", len(http401), es)
	}
	if e := http401[0]; e.Actor != "anonymous" || e.Action != "http.GET" || e.Target != "/api/v1/me" ||
		e.Outcome != audit.OutcomeDenied || e.Details["reason"] != "no credential presented" {
		t.Errorf("401 without credential: %+v", e)
	}
	if e := http401[1]; e.Actor != "anonymous" || e.Details["reason"] == nil || strings.Contains(fmt.Sprint(e.Details), "forged") {
		t.Errorf("401 with a bad credential: %+v", e)
	}
	if e := http401[2]; e.Action != "http.POST" || e.Target != "/mcp" {
		t.Errorf("401 of the MCP endpoint: %+v", e)
	}
	// The API records the 403 and the submission itself; the middleware adds
	// nothing for them.
	if http := find(es, func(e audit.Entry) bool { return strings.HasPrefix(e.Action, "http.") }); len(http) != 3 {
		t.Errorf("%d middleware entries, want the three 401s: %+v", len(http), http)
	}
	if denied := find(es, func(e audit.Entry) bool { return e.Actor == "token:2" && e.Outcome == audit.OutcomeDenied }); len(denied) != 1 {
		t.Errorf("the read token's refused submission has %d entries, want 1: %+v", len(denied), denied)
	}

	// The head of the chain is published as a metric.
	id, _, err := l.Head(context.Background())
	if err != nil || id == 0 {
		t.Fatalf("head = %d, %v", id, err)
	}
	want := fmt.Sprintf("trica_audit_head_id %d\n", id)
	deadline := time.Now().Add(patience)
	for {
		_, body := r.do("GET", "/metrics", "", "")
		if strings.Contains(body, want) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q in /metrics", want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A caller over its rate limit leaves one entry per period, not one per
// refused request.
func TestServerSamplesRateLimitedAudit(t *testing.T) {
	r := newRig(t, "auth: {rate_limit: {rate: 0.001, burst: 2}}")
	codes := map[int]int{}
	for range 6 {
		code, _ := r.do("GET", "/api/v1/me", r.readToken, "")
		codes[code]++
	}
	if codes[http.StatusOK] != 2 || codes[http.StatusTooManyRequests] != 4 {
		t.Fatalf("codes = %v", codes)
	}
	_, es := r.auditEntries()
	limited := find(es, func(e audit.Entry) bool { return e.Details["status"] == 429.0 })
	if len(limited) != 1 || limited[0].Actor != "token:2" || limited[0].Details["reason"] != "rate limit exceeded" {
		t.Fatalf("429 entries = %+v", limited)
	}
}
