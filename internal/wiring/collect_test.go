package wiring_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/collect/arista"
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/wiring"
)

// targetsYAML lists an Arista fake as a target; the password is a file
// reference, like a mounted Secret.
func targetsYAML(t *testing.T, pwFile string, fakes map[string]*arista.FakeArista) string {
	var b strings.Builder
	b.WriteString("targets:\n")
	for name, f := range fakes {
		fmt.Fprintf(&b, `  - kind: arista
    name: %s
    address: %s
    username: %s
    password: "file:%s"
    interval: 200ms
    timeout: 10s
`, name, f.URL(), arista.FakeUsername, pwFile)
	}
	return writeFile(t, "targets.yaml", b.String())
}

// A target of the targets file is collected by the running server and ends up
// in the inventory, through the engine, with the collection health recorded.
func TestServerCollectsTheConfiguredTargets(t *testing.T) {
	good := arista.NewFakeArista(arista.EOS414)
	defer good.Close()
	bad := arista.NewFakeArista(arista.EOS420)
	defer bad.Close()
	bad.RejectCredentials()
	pw := writeFile(t, "arista-password", arista.FakePassword+"\n")
	targets := targetsYAML(t, pw, map[string]*arista.FakeArista{"sw-a1": good, "sw-a2": bad})

	r := newRig(t, "collect: {targets_file: "+targets+"}\n")

	waitHTTP := func(what, path, token string, ok func(body string) bool) string {
		t.Helper()
		deadline := time.Now().Add(patience)
		var body string
		for {
			var code int
			code, body = r.do("GET", path, token, "")
			if code == 200 && ok(body) {
				return body
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d %s", what, code, body)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	t.Run("the switch it can read is in the inventory", func(t *testing.T) {
		waitHTTP("the collected switch", "/api/v1/objects?type=device&q=sw-a1", r.readToken, func(b string) bool {
			return strings.Contains(b, "sw-a1")
		})
	})

	t.Run("the failing one is on the health board, the good one too", func(t *testing.T) {
		waitHTTP("health of both sources", "/api/v1/health/collection", r.readToken, func(b string) bool {
			return strings.Contains(b, "arista:sw-a1") && strings.Contains(b, "arista:sw-a2")
		})
		_, body := r.do("GET", "/api/v1/health/collection", r.readToken, "")
		if !strings.Contains(body, "arista:sw-a2") || !strings.Contains(strings.ToLower(body), "false") {
			t.Fatalf("health board: %s", body)
		}
		if strings.Contains(body, arista.FakePassword) {
			t.Fatalf("a credential leaked into the health board: %s", body)
		}
	})

	t.Run("the metrics say what ran and what failed", func(t *testing.T) {
		waitHTTP("metrics", "/metrics", "", func(b string) bool {
			return strings.Contains(b, `trica_collect_runs_total{collector="arista",target="sw-a1"}`) &&
				strings.Contains(b, `trica_collect_failures_total{collector="arista",target="sw-a2"}`) &&
				!strings.Contains(b, `trica_collect_failures_total{collector="arista",target="sw-a2"} 0`)
		})
		_, body := r.do("GET", "/metrics", "", "")
		if strings.Contains(body, `trica_collect_failures_total{collector="arista",target="sw-a1"} 0`) == false {
			t.Fatalf("the healthy target counts failures:\n%s", body)
		}
	})

	t.Run("the collector only reads", func(t *testing.T) {
		if ops := good.NonReadOperations(); len(ops) != 0 {
			t.Fatalf("non-read operations attempted: %v", ops)
		}
	})
}

// A deployment with a targets file that does not parse must not start: it
// would discover nothing in silence.
func TestServerRefusesAnUnreadableTargetsFile(t *testing.T) {
	for name, content := range map[string]string{
		"unknown key":  "targets:\n  - kind: arista\n    name: sw-a1\n    addr_typo: x\n",
		"unknown kind": "targets:\n  - kind: nope\n    name: sw-a1\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := writeFile(t, "targets.yaml", content)
			err := tryStart(t, "collect: {targets_file: "+p+"}\n")
			if err == nil {
				t.Fatal("the server started with a broken targets file")
			}
		})
	}
}

// Only one replica collects: while another session holds the lock, nothing is
// collected; when it lets go, collection starts.
func TestOnlyOneReplicaCollects(t *testing.T) {
	// Restored after the rig has stopped the server (cleanups run last in,
	// first out), which still reads it until then.
	t.Cleanup(wiring.SetCollectRetry(100 * time.Millisecond))
	f := arista.NewFakeArista(arista.EOS414)
	defer f.Close()
	pw := writeFile(t, "arista-password", arista.FakePassword)
	targets := targetsYAML(t, pw, map[string]*arista.FakeArista{"sw-a1": f})

	r := newRig(t, "")
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, r.url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, wiring.CollectLockKey).Scan(&got); err != nil || !got {
		t.Fatalf("lock: %v %v", got, err)
	}

	r.start("collect: {targets_file: " + targets + "}\n")
	time.Sleep(1500 * time.Millisecond)
	if n := len(f.Requests()); n != 0 {
		t.Fatalf("a replica without the lock collected (%d requests)", n)
	}

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, wiring.CollectLockKey); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(patience)
	for len(f.Requests()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("collection never started after the lock was released")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// tryStart runs a server with extraYAML on a migrated database and returns
// what Run returned: an error when a component could not start.
func tryStart(t *testing.T, extraYAML string) error {
	t.Helper()
	r := newRig(t, "")
	_ = r.stop()
	cfg, err := server.LoadConfig(writeFile(t, "trica.yaml", fmt.Sprintf(`
database: {url_file: %s}
http: {addr: "127.0.0.1:0"}
%s
`, writeFile(t, "url", r.url), extraYAML)))
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)), "test", wiring.New(cfg, "test").Components()...)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return srv.Run(ctx)
}
