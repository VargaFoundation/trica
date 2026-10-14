package audit

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
)

// fakeJWT builds a three-part compact token at test time, so no token-shaped literal sits in
// the repository.
func fakeJWT() string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(`{"sub":"alice"}`)) + "." + enc([]byte("not-a-signature"))
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func newLog(t *testing.T) *Log {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 14, 9, 0, 0, 123456789, time.UTC)}
	return &Log{Store: testdb.New(t), Now: c.now}
}

func appendN(t *testing.T, l *Log, n int) []*Entry {
	t.Helper()
	var out []*Entry
	for i := range n {
		e, err := l.Append(context.Background(), Entry{
			Actor: "user:alice", Action: "proposal.approve", Target: fmt.Sprintf("proposal/%d", i),
			RequestID: fmt.Sprintf("req-%d", i), Details: map[string]any{"n": i, "nested": map[string]any{"b": 1, "a": []any{"x", 2.5}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// tamper runs raw SQL against audit_log with the append-only triggers disabled, the way a
// database administrator (or an attacker with table ownership) would. Allowed in tests:
// audit_log is not an inventory table.
func tamper(t *testing.T, s *store.Store, sql string, args ...any) {
	t.Helper()
	err := s.InTx(context.Background(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `ALTER TABLE audit_log DISABLE TRIGGER audit_log_no_change`); err != nil {
			return err
		}
		if _, err := tx.Exec(context.Background(), sql, append([]any{pgx.QueryExecModeSimpleProtocol}, args...)...); err != nil {
			return err
		}
		_, err := tx.Exec(context.Background(), `ALTER TABLE audit_log ENABLE TRIGGER audit_log_no_change`)
		return err
	})
	if err != nil {
		t.Fatalf("tamper: %v", err)
	}
}

func verify(t *testing.T, l *Log) *Result {
	t.Helper()
	r, err := l.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAppendAndVerify(t *testing.T) {
	l := newLog(t)
	if r := verify(t, l); !r.Intact() || r.Entries != 0 {
		t.Fatalf("empty chain: %+v", r)
	}
	es := appendN(t, l, 5)
	r := verify(t, l)
	if !r.Intact() || r.Entries != 5 || r.HeadID != es[4].ID || !bytes.Equal(r.Head, es[4].Hash) {
		t.Fatalf("result = %+v", r)
	}
	if !bytes.Equal(es[0].PrevHash, genesis) {
		t.Error("the first entry must chain from the genesis hash")
	}
	for i := 1; i < len(es); i++ {
		if !bytes.Equal(es[i].PrevHash, es[i-1].Hash) {
			t.Errorf("entry %d does not chain from entry %d", i, i-1)
		}
	}
	list, err := l.List(context.Background(), 3)
	if err != nil || len(list) != 3 || list[0].ID != es[4].ID {
		t.Errorf("list = %v, %v", list, err)
	}
	if list[0].Outcome != OutcomeSuccess || list[0].RequestID != "req-4" || list[0].Actor != "user:alice" {
		t.Errorf("entry = %+v", list[0])
	}
}

func TestAppendValidation(t *testing.T) {
	l := newLog(t)
	ctx := context.Background()
	for name, e := range map[string]Entry{
		"no actor":         {Action: "x"},
		"no action":        {Actor: "a"},
		"unknown outcome":  {Actor: "a", Action: "x", Outcome: "meh"},
		"details not json": {Actor: "a", Action: "x", Details: map[string]any{"f": func() {}}},
	} {
		_, err := l.Append(ctx, e)
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
		// The database CHECK would refuse it too; the guard in Go must be the one speaking.
		if name == "unknown outcome" && (err == nil || !strings.Contains(err.Error(), "unknown outcome")) {
			t.Errorf("%s: refused by the wrong layer: %v", name, err)
		}
	}
	if r := verify(t, l); r.Entries != 0 {
		t.Error("a refused entry was stored")
	}
	// Details that are nil or empty verify.
	if _, err := l.Append(ctx, Entry{Actor: "a", Action: "x"}); err != nil {
		t.Fatal(err)
	}
	if r := verify(t, l); !r.Intact() {
		t.Errorf("entry without details: %+v", r)
	}
}

func TestVerifyDetectsTamperedEntry(t *testing.T) {
	cases := map[string]struct {
		sql  string
		want string // substring of the reason
	}{
		"target":     {`UPDATE audit_log SET target = 'proposal/evil' WHERE id = $1`, "content"},
		"actor":      {`UPDATE audit_log SET actor = 'user:mallory' WHERE id = $1`, "content"},
		"action":     {`UPDATE audit_log SET action = 'noop' WHERE id = $1`, "content"},
		"outcome":    {`UPDATE audit_log SET outcome = 'failure' WHERE id = $1`, "content"},
		"request id": {`UPDATE audit_log SET request_id = 'forged' WHERE id = $1`, "content"},
		"details":    {`UPDATE audit_log SET details = '{"n": 99}' WHERE id = $1`, "content"},
		"time":       {`UPDATE audit_log SET at = at + interval '1 second' WHERE id = $1`, "content"},
		// The hash commits to the predecessor: an entry grafted onto another chain position
		// is a content mismatch of its own, not only a link mismatch.
		"previous hash": {`UPDATE audit_log SET prev_hash = decode(repeat('ab', 32), 'hex') WHERE id = $1`, "content"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			l := newLog(t)
			es := appendN(t, l, 6)
			victim := es[3]
			tamper(t, l.Store, c.sql, victim.ID)
			r := verify(t, l)
			if r.Intact() {
				t.Fatal("tampering not detected")
			}
			if r.First.ID != victim.ID {
				t.Errorf("reported entry %d, want %d", r.First.ID, victim.ID)
			}
			if !strings.Contains(r.First.Reason, c.want) {
				t.Errorf("reason = %q", r.First.Reason)
			}
			if r.Entries != 4 {
				t.Errorf("checked %d entries before stopping, want 4", r.Entries)
			}
		})
	}
}

func TestVerifyDetectsDeletedEntry(t *testing.T) {
	l := newLog(t)
	es := appendN(t, l, 6)
	tamper(t, l.Store, `DELETE FROM audit_log WHERE id = $1`, es[2].ID)
	r := verify(t, l)
	if r.Intact() || r.First.ID != es[3].ID {
		t.Fatalf("result = %+v, want a break at the entry after the gap (%d)", r.First, es[3].ID)
	}
	if !strings.Contains(r.First.Reason, "previous hash") {
		t.Errorf("reason = %q", r.First.Reason)
	}
}

func TestVerifyDetectsDeletedFirstEntry(t *testing.T) {
	l := newLog(t)
	es := appendN(t, l, 3)
	tamper(t, l.Store, `DELETE FROM audit_log WHERE id = $1`, es[0].ID)
	if r := verify(t, l); r.Intact() || r.First.ID != es[1].ID {
		t.Fatalf("result = %+v", r.First)
	}
}

// An attacker who rewrites an entry and recomputes its hash is caught at the next entry,
// whose previous hash no longer matches.
func TestVerifyDetectsRehashedEntry(t *testing.T) {
	l := newLog(t)
	es := appendN(t, l, 6)
	forged := *es[3]
	forged.Target = "proposal/evil"
	h, err := computeHash(forged.PrevHash, &forged)
	if err != nil {
		t.Fatal(err)
	}
	tamper(t, l.Store, `UPDATE audit_log SET target = $2, hash = $3 WHERE id = $1`, forged.ID, forged.Target, h)
	r := verify(t, l)
	if r.Intact() || r.First.ID != es[4].ID {
		t.Fatalf("result = %+v, want a break at %d", r.First, es[4].ID)
	}
}

func TestVerifyDetectsReorderedEntries(t *testing.T) {
	l := newLog(t)
	es := appendN(t, l, 6)
	a, b := es[2].ID, es[3].ID
	tamper(t, l.Store, fmt.Sprintf(`UPDATE audit_log SET id = 1000000 WHERE id = %d;
		UPDATE audit_log SET id = %d WHERE id = %d;
		UPDATE audit_log SET id = %d WHERE id = 1000000`, a, a, b, b))
	r := verify(t, l)
	if r.Intact() || r.First.ID != a {
		t.Fatalf("result = %+v, want a break at the first swapped position (%d)", r.First, a)
	}
}

// A forged entry appended at the end with a made-up hash is caught at itself.
func TestVerifyDetectsForgedAppend(t *testing.T) {
	l := newLog(t)
	es := appendN(t, l, 3)
	var id int64
	err := l.Store.Pool.QueryRow(context.Background(), `
		INSERT INTO audit_log (actor, action, prev_hash, hash) VALUES ('user:mallory', 'x', $1, $2) RETURNING id`,
		es[2].Hash, bytes.Repeat([]byte{7}, 32)).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if r := verify(t, l); r.Intact() || r.First.ID != id {
		t.Fatalf("result = %+v, want a break at %d", r.First, id)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	l := newLog(t)
	es := appendN(t, l, 2)
	ctx := context.Background()
	for name, sql := range map[string]string{
		"update":   `UPDATE audit_log SET target = 'x'`,
		"delete":   `DELETE FROM audit_log`,
		"truncate": `TRUNCATE audit_log`,
	} {
		if _, err := l.Store.Pool.Exec(ctx, sql); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if r := verify(t, l); !r.Intact() || r.Entries != len(es) {
		t.Errorf("chain after refused changes: %+v", r)
	}
}

func TestConcurrentAppendsKeepOneChain(t *testing.T) {
	l := newLog(t)
	ctx := context.Background()
	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := l.Append(ctx, Entry{Actor: "user:alice", Action: "x", Target: fmt.Sprint(i)})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	r := verify(t, l)
	if !r.Intact() || r.Entries != n {
		t.Fatalf("result = %+v, %d entries", r.First, r.Entries)
	}
	// No fork: every previous hash is used exactly once.
	var dup int
	err := l.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT prev_hash FROM audit_log GROUP BY prev_hash HAVING count(*) > 1) d`).Scan(&dup)
	if err != nil || dup != 0 {
		t.Errorf("%d previous hashes are shared by several entries (a fork): %v", dup, err)
	}
}

func TestRedact(t *testing.T) {
	in := map[string]any{
		"password":      "hunter2",
		"Password":      "hunter2",
		"api_key":       "k",
		"API-Key":       "k",
		"clientSecret":  "s",
		"Authorization": "Bearer abc.def.ghi",
		"cookie":        "sid=1",
		"access_token":  "t",
		"refreshToken":  "t",
		"private_key":   "-----BEGIN",
		"session":       "s",
		"keep":          "visible",
		"count":         3,
		"nested":        map[string]any{"db_password": "p", "ok": "fine", "deeper": []any{map[string]any{"token": "t", "x": 1}}},
		"list":          []any{"a", "Bearer secretvalue"},
		"leak_in_value": "see trica_12_" + strings.Repeat("A", 43) + " for details",
		"jwt_in_value":  "got " + fakeJWT() + " here",
	}
	out := Redact(in).(map[string]any)
	for _, k := range []string{"password", "Password", "api_key", "API-Key", "clientSecret", "Authorization", "cookie", "access_token", "refreshToken", "private_key", "session"} {
		if out[k] != Redacted {
			t.Errorf("%s = %v, want redacted", k, out[k])
		}
	}
	if out["keep"] != "visible" || out["count"] != 3 {
		t.Errorf("harmless values changed: %v %v", out["keep"], out["count"])
	}
	nested := out["nested"].(map[string]any)
	if nested["db_password"] != Redacted || nested["ok"] != "fine" {
		t.Errorf("nested = %v", nested)
	}
	if deeper := nested["deeper"].([]any)[0].(map[string]any); deeper["token"] != Redacted || deeper["x"] != 1 {
		t.Errorf("deeper = %v", deeper)
	}
	if got := out["list"].([]any); got[0] != "a" || got[1] != Redacted {
		t.Errorf("list = %v", got)
	}
	if s := out["leak_in_value"].(string); strings.Contains(s, "trica_12_") || !strings.Contains(s, Redacted) || !strings.HasSuffix(s, "for details") {
		t.Errorf("token in value: %q", s)
	}
	if s := out["jwt_in_value"].(string); strings.Contains(s, "eyJ") {
		t.Errorf("jwt in value: %q", s)
	}
	// The input is not modified.
	if in["password"] != "hunter2" {
		t.Error("Redact modified its input")
	}
	// A pathologically deep structure is cut, not trusted.
	deep := any("secret")
	for range 40 {
		deep = map[string]any{"a": deep}
	}
	if !strings.Contains(fmt.Sprint(Redact(deep)), Redacted) || strings.Contains(fmt.Sprint(Redact(deep)), "secret") {
		t.Error("deep structure not cut")
	}
}

func TestAppendRedactsBeforeStoring(t *testing.T) {
	l := newLog(t)
	ctx := context.Background()
	tok := "trica_7_" + strings.Repeat("B", 43)
	_, err := l.Append(ctx, Entry{
		Actor: "user:alice", Action: "token.create", Target: "/v1/tokens/" + tok,
		Details: map[string]any{"name": "ci", "password": "hunter2", "nested": map[string]any{"secret": "s3"}, "note": "Authorization: Bearer abcdef"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var dump string
	if err := l.Store.Pool.QueryRow(ctx, `SELECT row(audit_log.*)::text FROM audit_log`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"hunter2", "s3", tok, "abcdef"} {
		if strings.Contains(dump, leak) {
			t.Errorf("%q reached the database: %s", leak, dump)
		}
	}
	if !strings.Contains(dump, "ci") || !strings.Contains(dump, Redacted) {
		t.Errorf("row = %s", dump)
	}
	if r := verify(t, l); !r.Intact() {
		t.Errorf("redacted entry does not verify: %+v", r.First)
	}
}
