package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/VargaFoundation/trica/internal/collect/arista"
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/internal/transport"
	"github.com/VargaFoundation/trica/internal/wiring"
)

const collectorPatience = 30 * time.Second

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// transportServer starts a real server with the transport enabled on a test
// database and returns what a remote collector needs: its address, the CA file
// and a pool to mint tokens and to look at the inventory.
func transportServer(t *testing.T) (addr, caFile string, pool *pgxpool.Pool) {
	t.Helper()
	if os.Getenv(server.EnvDatabaseURL) != "" {
		t.Skip(server.EnvDatabaseURL + " is set; it would override the test database")
	}
	url := testdb.NewURL(t)
	dir := t.TempDir()
	caDir := filepath.Join(dir, "ca")
	cfg, err := server.LoadConfig(writeTemp(t, dir, "trica.yaml", fmt.Sprintf(`
database: {url_file: %s}
http: {addr: "127.0.0.1:0"}
sweep_interval: 1h
deduce: {disabled: true}
mcp: {disabled: true}
web: {disabled: true}
transport:
  addr: "127.0.0.1:0"
  ca_dir: %s
  hosts: [localhost, 127.0.0.1]
`, writeTemp(t, dir, "url", url), caDir)))
	if err != nil {
		t.Fatal(err)
	}
	w := wiring.New(cfg, "test")
	srv := server.New(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)), "test", w.Components()...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("server did not stop")
		}
	})
	deadline := time.Now().Add(collectorPatience)
	for w.TransportAddr() == "" {
		select {
		case err := <-done:
			t.Fatalf("server exited: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("transport not listening")
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return w.TransportAddr(), filepath.Join(caDir, "ca.pem"), st.Pool
}

// startCollector runs `trica collector` on a configuration file and returns a
// function that stops it and waits.
func startCollector(t *testing.T, cfgPath string) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var out syncBuffer
	go func() { done <- run(ctx, []string{"collector", "--config", cfgPath}, nil, &out, &out, os.Getenv) }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("collector: %v\n%s", err, out.String())
			}
		case <-time.After(30 * time.Second):
			t.Error("collector did not stop")
		}
	}
	t.Cleanup(stop)
	return stop
}

type health struct {
	ok      bool
	attempt time.Time
}

func sourceHealth(t *testing.T, pool *pgxpool.Pool, source string) (health, bool) {
	t.Helper()
	var h health
	err := pool.QueryRow(context.Background(), `SELECT ok, last_attempt FROM source_health WHERE source = $1`, source).Scan(&h.ok, &h.attempt)
	return h, err == nil
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(collectorPatience)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRemoteCollectorEnrolsAndDeliversThroughTheTransport(t *testing.T) {
	addr, caFile, pool := transportServer(t)
	ctx := context.Background()

	good := arista.NewFakeArista(arista.EOS414)
	defer good.Close()
	bad := arista.NewFakeArista(arista.EOS420)
	defer bad.Close()
	bad.RejectCredentials()

	dir := t.TempDir()
	pw := writeTemp(t, dir, "arista-password", arista.FakePassword+"\n")
	targets := writeTemp(t, dir, "targets.yaml", fmt.Sprintf(`
targets:
  - {kind: arista, name: sw-a1, address: %s, username: %s, password: "file:%s", interval: 200ms, timeout: 10s}
  - {kind: arista, name: sw-a2, address: %s, username: %s, password: "file:%s", interval: 200ms, timeout: 10s}
`, good.URL(), arista.FakeUsername, pw, bad.URL(), arista.FakeUsername, pw))

	// One enrolment token per source: a certificate is bound to one source.
	reg := transport.NewRegistry(pool)
	tokens := map[string]string{}
	for _, src := range []string{"arista:sw-a1", "arista:sw-a2"} {
		tok, err := reg.NewEnrolmentToken(ctx, src, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		tokens[src] = writeTemp(t, dir, strings.ReplaceAll(src, ":", "-")+".token", tok+"\n")
	}
	state := filepath.Join(dir, "state")
	cfg := writeTemp(t, dir, "collector.yaml", fmt.Sprintf(`
server_url: https://%s
server_name: localhost
ca_file: %s
state_dir: %s
targets_file: %s
flush_interval: 100ms
enrol_token_files:
  "arista:sw-a1": %s
  "arista:sw-a2": %s
`, addr, caFile, state, targets, tokens["arista:sw-a1"], tokens["arista:sw-a2"]))

	stop := startCollector(t, cfg)
	var first health
	waitUntil(t, "the good target's batch in the inventory", func() bool {
		h, ok := sourceHealth(t, pool, "arista:sw-a1")
		first = h
		return ok && h.ok
	})
	var objects int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM objects`).Scan(&objects); err != nil || objects == 0 {
		t.Fatalf("objects: %d %v", objects, err)
	}
	waitUntil(t, "the failing target's health record", func() bool {
		h, ok := sourceHealth(t, pool, "arista:sw-a2")
		return ok && !h.ok
	})
	stop()

	// The certificate and key are kept in the state directory, owner only.
	entries, err := os.ReadDir(filepath.Join(state, "arista_sw-a1"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("state dir: %v %v", entries, err)
	}

	// After a restart the token is no longer needed (it is single use, and
	// here the file is gone): the collector reuses its certificate.
	for _, p := range tokens {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	stop = startCollector(t, cfg)
	defer stop()
	waitUntil(t, "a new run after the restart without a token", func() bool {
		h, _ := sourceHealth(t, pool, "arista:sw-a1")
		return h.attempt.After(first.attempt)
	})
}

// A target with no token and no certificate delivers nothing: the server never
// hears from a collector it did not enrol.
func TestRemoteCollectorWithoutATokenDeliversNothing(t *testing.T) {
	addr, caFile, pool := transportServer(t)
	f := arista.NewFakeArista(arista.EOS414)
	defer f.Close()
	dir := t.TempDir()
	pw := writeTemp(t, dir, "pw", arista.FakePassword)
	targets := writeTemp(t, dir, "targets.yaml", fmt.Sprintf(
		"targets:\n  - {kind: arista, name: sw-a1, address: %s, username: %s, password: \"file:%s\", interval: 100ms}\n", f.URL(), arista.FakeUsername, pw))
	cfg := writeTemp(t, dir, "collector.yaml", fmt.Sprintf(
		"server_url: https://%s\nserver_name: localhost\nca_file: %s\nstate_dir: %s\ntargets_file: %s\nflush_interval: 100ms\n",
		addr, caFile, filepath.Join(dir, "state"), targets))
	stop := startCollector(t, cfg)
	waitUntil(t, "the fake to be queried", func() bool { return len(f.Requests()) > 0 })
	time.Sleep(500 * time.Millisecond)
	stop()
	if _, ok := sourceHealth(t, pool, "arista:sw-a1"); ok {
		t.Fatal("a collector that was never enrolled got a batch through")
	}
}

func TestCollectorConfigIsStrict(t *testing.T) {
	dir := t.TempDir()
	base := "server_url: https://trica.example.test:8443\nca_file: /ca.pem\nstate_dir: /s\ntargets_file: /t.yaml\n"
	for name, content := range map[string]string{
		"ok":                  base,
		"unknown key":         base + "serverurl: x\n",
		"missing state dir":   "server_url: https://x\nca_file: /ca.pem\ntargets_file: /t.yaml\n",
		"plain http":          strings.Replace(base, "https://", "http://", 1),
		"password as a value": base + "metrics_addr: 1\npassword: x\n",
	} {
		_, err := loadCollectorConfig(writeTemp(t, dir, "c.yaml", content))
		if (err == nil) != (name == "ok") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := runCmd(t, "collector"); err == nil {
		t.Error("collector without --config accepted")
	}
}
