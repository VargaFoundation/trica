package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/VargaFoundation/trica/internal/agent/update"
	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/internal/transport"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// recorder is the server's sink: what reached the engine's door, in order.
type recorder struct {
	mu   sync.Mutex
	got  []string
	bind []string
}

func (r *recorder) Ingest(_ context.Context, b *sdk.Batch, id transport.Identity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, b.Source)
	r.bind = append(r.bind, id.Binding)
	return nil
}

func (r *recorder) sources() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

func (r *recorder) bindings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.bind)
}

// srvEnv is a real transport server (CA, registry on a test database, mTLS
// listener) that tests can stop and restart on the same address.
type srvEnv struct {
	t      *testing.T
	reg    *transport.Registry
	rec    *recorder
	srv    *transport.Server
	addr   string
	caFile string
	// hold keeps addr bound while the server is stopped, so that a test
	// process of another package cannot serve on it during the outage (#67).
	hold net.Listener
}

func newSrvEnv(t *testing.T) *srvEnv {
	t.Helper()
	st := testdb.New(t)
	ca, err := pki.NewCA("trica agent test CA", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e := &srvEnv{t: t, reg: transport.NewRegistry(st.Pool), rec: &recorder{}}
	cert, err := ca.IssueServer([]string{"127.0.0.1"}, 24*time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e.srv, err = transport.NewServer(transport.ServerConfig{
		CA: ca, Registry: e.reg, Sink: e.rec, ServerCert: cert,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.release)
	e.caFile = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(e.caFile, ca.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

// start serves on a free port the first time and on the same one afterwards,
// and returns the function that stops the server.
func (e *srvEnv) start() (stop func()) {
	e.t.Helper()
	e.release()
	listen := "127.0.0.1:0"
	if e.addr != "" {
		listen = e.addr
	}
	var ln net.Listener
	var err error
	for range 50 { // the port of a server stopped a moment ago can take a few ms to free
		if ln, err = net.Listen("tcp", listen); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		e.t.Fatal(err)
	}
	if e.addr == "" { // a restart keeps it: tests read url() from other goroutines
		e.addr = ln.Addr().String()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.srv.Serve(ctx, ln) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				e.t.Errorf("serve: %v", err)
			}
			e.holdPort()
		})
	}
	e.t.Cleanup(stop)
	return stop
}

// holdPort binds addr again once the server is down, with a listener that
// closes every connection.
func (e *srvEnv) holdPort() {
	ln, err := net.Listen("tcp", e.addr)
	if err != nil {
		e.t.Errorf("hold %s during the outage: %v", e.addr, err)
		return
	}
	e.hold = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
}

// release frees the port held by holdPort, if any.
func (e *srvEnv) release() {
	if e.hold != nil {
		_ = e.hold.Close()
		e.hold = nil
	}
}

func (e *srvEnv) url() string { return "https://" + e.addr }

// token creates an enrolment token for binding and writes it to a file.
func (e *srvEnv) token(binding string) string {
	e.t.Helper()
	tok, err := e.reg.NewEnrolmentToken(context.Background(), binding, time.Hour)
	if err != nil {
		e.t.Fatal(err)
	}
	path := filepath.Join(e.t.TempDir(), "enrol-token")
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return path
}

// agent runs trica-agent once against the server; extra flags come last.
func (e *srvEnv) agent(stateDir string, extra ...string) (code int, stdout, stderr string) {
	e.t.Helper()
	args := []string{"--root", fixture, "--once", "--server", e.url(), "--ca-file", e.caFile, "--state-dir", stateDir}
	return runArgs(e.t, append(args, extra...)...)
}

func spooled(dir string) int {
	ents, _ := os.ReadDir(filepath.Join(dir, "spool"))
	return len(ents)
}

func TestAgentEnrolsWithItsTokenAndItsBatchArrivesUnderItsHostSource(t *testing.T) {
	e := newSrvEnv(t)
	e.start()
	dir := t.TempDir()
	tokFile := e.token("agent:host-01")
	tok, _ := os.ReadFile(tokFile)

	code, out, errs := e.agent(dir, "--enrol-token-file", tokFile)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := e.rec.sources(); !slices.Equal(got, []string{"agent:host-01"}) {
		t.Fatalf("server got %v, want one batch from agent:host-01", got)
	}
	if got := e.rec.bindings(); !slices.Equal(got, []string{"agent:host-01"}) {
		t.Fatalf("batch bound to %v", got)
	}
	if out != "" {
		t.Fatalf("stdout %q: with --server batches do not go to stdout", out)
	}
	// Neither the token nor the key reaches the logs or stdout.
	for _, secret := range []string{strings.TrimSpace(string(tok)), "PRIVATE KEY"} {
		if strings.Contains(errs, secret) || strings.Contains(out, secret) {
			t.Fatalf("a secret leaked to the output: %q", secret)
		}
	}
	key, _ := os.ReadFile(filepath.Join(dir, "identity.pem"))
	if !strings.Contains(string(key), "PRIVATE KEY") {
		t.Fatal("no identity was kept in the state directory")
	}
	// What the agent keeps is private to it.
	for path, want := range map[string]os.FileMode{
		filepath.Join(dir, "identity.pem"): 0o600,
		filepath.Join(dir, "spool"):        0o700,
	} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s is %v, want %v", path, st.Mode().Perm(), want)
		}
	}
	// The token was one-time: another agent cannot enrol with it.
	code, _, _ = e.agent(t.TempDir(), "--enrol-token-file", tokFile)
	if code == 0 || len(e.rec.sources()) != 1 {
		t.Fatalf("a used token enrolled a second agent (exit %d, %v)", code, e.rec.sources())
	}
}

func TestSpooledBatchesAreOwnerOnly(t *testing.T) {
	e := newSrvEnv(t)
	stop := e.start()
	stop()
	dir := t.TempDir()
	if code, _, errs := e.agent(dir, "--enrol-token-file", e.token("agent:host-01")); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "spool"))
	if len(ents) != 1 {
		t.Fatalf("%d files in the spool", len(ents))
	}
	st, err := os.Stat(filepath.Join(dir, "spool", ents[0].Name()))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("spooled batch: %v, %v", st, err)
	}
}

func TestSecondStartReusesTheCertificateWithoutAToken(t *testing.T) {
	e := newSrvEnv(t)
	e.start()
	dir := t.TempDir()
	tokFile := e.token("agent:host-01")
	if code, _, errs := e.agent(dir, "--enrol-token-file", tokFile); code != 0 {
		t.Fatalf("first start: exit %d: %s", code, errs)
	}
	// Second start: no token at all, then a token file that no longer exists
	// (the operator removed it), then the used token still in place.
	for _, extra := range [][]string{nil, {"--enrol-token-file", filepath.Join(t.TempDir(), "gone")}, {"--enrol-token-file", tokFile}} {
		if code, _, errs := e.agent(dir, extra...); code != 0 {
			t.Fatalf("restart with %v: exit %d: %s", extra, code, errs)
		}
	}
	if got := len(e.rec.sources()); got != 4 {
		t.Fatalf("server got %d batches, want 4", got)
	}
	issued, err := e.reg.IssuedTo(context.Background(), "agent:host-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(issued) != 1 {
		t.Fatalf("%d certificates issued to the host, want the one from the first start", len(issued))
	}
}

func TestServerDownSpoolsTheBatchAndTheNextRunDeliversIt(t *testing.T) {
	e := newSrvEnv(t)
	stop := e.start()
	dir := t.TempDir()
	if code, _, errs := e.agent(dir, "--enrol-token-file", e.token("agent:host-01")); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	stop()
	for range 2 {
		if code, _, errs := e.agent(dir); code != 0 {
			t.Fatalf("an unreachable server must not fail the run: exit %d: %s", code, errs)
		}
	}
	if n := spooled(dir); n != 2 {
		t.Fatalf("%d batches in the spool, want 2", n)
	}
	if got := len(e.rec.sources()); got != 1 {
		t.Fatalf("server got %d batches while it was down", got)
	}
	e.start()
	if code, _, errs := e.agent(dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := e.rec.sources(); len(got) != 4 {
		t.Fatalf("server got %v, want the first batch, the two spooled and the new one", got)
	}
	if n := spooled(dir); n != 0 {
		t.Fatalf("%d batches still in the spool after delivery", n)
	}
}

// The very first start can find the server down: the batch is spooled, and the
// enrolment happens, with the token that is still there, when it comes back.
func TestFirstStartWithTheServerDownEnrolsLater(t *testing.T) {
	e := newSrvEnv(t)
	stop := e.start()
	stop()
	dir := t.TempDir()
	tokFile := e.token("agent:host-01")
	if code, _, errs := e.agent(dir, "--enrol-token-file", tokFile); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if n := spooled(dir); n != 1 {
		t.Fatalf("%d batches in the spool, want 1", n)
	}
	e.start()
	if code, _, errs := e.agent(dir, "--enrol-token-file", tokFile); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := e.rec.sources(); !slices.Equal(got, []string{"agent:host-01", "agent:host-01"}) {
		t.Fatalf("server got %v", got)
	}
}

// A long-running agent retries its spool by itself, between two collections.
func TestRunningAgentFlushesItsSpoolWhenTheServerComesBack(t *testing.T) {
	old := flushEvery
	flushEvery = 100 * time.Millisecond
	t.Cleanup(func() { flushEvery = old })

	e := newSrvEnv(t)
	stop := e.start()
	dir := t.TempDir()
	if code, _, errs := e.agent(dir, "--enrol-token-file", e.token("agent:host-01")); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		var out, errb strings.Builder
		done <- run(ctx, []string{"--root", fixture, "--interval", "1h", "--server", e.url(), "--ca-file", e.caFile, "--state-dir", dir}, &out, &errb)
	}()
	waitFor(t, "the batch to be spooled", func() bool { return spooled(dir) == 1 })
	e.start()
	waitFor(t, "the spool to be flushed by the running agent", func() bool { return len(e.rec.sources()) == 2 })
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not stop when cancelled")
	}
}

// The metrics file says what the spool holds and since when, so that an
// operator is alerted before it fills (#65); it follows the spool as it drains.
func TestMetricsFileShowsTheSpoolAndItsAge(t *testing.T) {
	old := flushEvery
	flushEvery = 100 * time.Millisecond
	t.Cleanup(func() { flushEvery = old })

	e := newSrvEnv(t)
	stop := e.start()
	dir := t.TempDir()
	if code, _, errs := e.agent(dir, "--enrol-token-file", e.token("agent:host-01")); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	stop()

	prom := filepath.Join(t.TempDir(), "trica-agent.prom")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		var out, errb strings.Builder
		done <- run(ctx, []string{"--root", fixture, "--interval", "1h", "--server", e.url(), "--ca-file", e.caFile,
			"--state-dir", dir, "--metrics-file", prom}, &out, &errb)
	}()
	waitFor(t, "the metrics to show a spooled batch growing old", func() bool {
		m := readMetrics(t, prom)
		return m["trica_agent_spooled_batches"] == 1 && m["trica_agent_spool_bytes"] > 0 &&
			m["trica_agent_spool_oldest_age_seconds"] > 0 && m["trica_agent_last_delivery_timestamp_seconds"] == 0
	})
	e.start()
	waitFor(t, "the metrics to show the spool delivered", func() bool {
		m := readMetrics(t, prom)
		return m["trica_agent_spooled_batches"] == 0 && m["trica_agent_spool_oldest_age_seconds"] == 0 &&
			m["trica_agent_sent_batches_total"] == 1 && m["trica_agent_last_delivery_timestamp_seconds"] > 0
	})
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not stop when cancelled")
	}
	if st, err := os.Stat(prom); err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("metrics file: %v, %v; the node exporter must be able to read it", st, err)
	}
	ents, _ := os.ReadDir(filepath.Dir(prom))
	if len(ents) != 1 {
		t.Fatalf("temporary files left next to the metrics file: %v", ents)
	}
}

// A run with --once writes the metrics after its collection.
func TestMetricsFileOfASingleRun(t *testing.T) {
	e := newSrvEnv(t)
	e.start()
	dir := t.TempDir()
	prom := filepath.Join(t.TempDir(), "trica-agent.prom")
	if code, _, errs := e.agent(dir, "--enrol-token-file", e.token("agent:host-01"), "--metrics-file", prom); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if m := readMetrics(t, prom); m["trica_agent_sent_batches_total"] != 1 || m["trica_agent_spooled_batches"] != 0 {
		t.Fatalf("metrics = %v", m)
	}
}

// readMetrics parses the metrics file as the node exporter would, and returns
// the value of each unlabelled sample; a missing file is an empty map.
func readMetrics(t *testing.T, path string) map[string]float64 {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- a test file
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p := expfmt.NewTextParser(model.UTF8Validation)
	fams, err := p.TextToMetricFamilies(f)
	if err != nil {
		t.Fatalf("the metrics file does not parse: %v", err)
	}
	out := map[string]float64{}
	for name, fam := range fams {
		for _, m := range fam.GetMetric() {
			switch {
			case m.GetGauge() != nil:
				out[name] = m.GetGauge().GetValue()
			case m.GetCounter() != nil:
				out[name] = m.GetCounter().GetValue()
			}
		}
	}
	return out
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The enrolment token grants one binding. An agent whose host is another one
// enrols, but its batches are refused, and refused for good: not spooled.
func TestTokenForAnotherBindingIsRefused(t *testing.T) {
	e := newSrvEnv(t)
	e.start()
	dir := t.TempDir()
	code, _, errs := e.agent(dir, "--enrol-token-file", e.token("agent:host-02"))
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errs)
	}
	if !strings.Contains(errs, "403") {
		t.Fatalf("stderr does not say the server refused the batch: %s", errs)
	}
	if got := e.rec.sources(); len(got) != 0 {
		t.Fatalf("a batch of agent:host-01 reached the engine with a host-02 certificate: %v", got)
	}
	if n := spooled(dir); n != 0 {
		t.Fatalf("a refused batch was spooled: %d files", n)
	}
}

// Probation: the new version is healthy when its batch was accepted, or only
// spooled when the server was already down before the update (a spool was
// waiting); never when the server refused it (#65).
func TestProbationCountsAcceptedAndSpooledBatchesAsHealthy(t *testing.T) {
	for _, tc := range []struct {
		name        string
		downBefore  bool // the server was down before the update: the old version left a spool
		serverUp    bool
		binding     string
		confirmed   bool
		wantSpooled int
	}{
		{"accepted", false, true, "agent:host-01", true, 0},
		{"spooled while the server was reachable before", false, false, "agent:host-01", false, 1},
		{"spooled while the server was already down", true, false, "agent:host-01", true, 2},
		{"refused", false, true, "agent:host-02", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newSrvEnv(t)
			stop := e.start()
			dir, s := stage(t, "2.0.0")
			tok := e.token(tc.binding)
			if tc.downBefore {
				stop()
				setVersion(t, "1.0.0") // the old version runs and leaves its batch in the spool
				e.agent(dir, "--enrol-token-file", tok)
			} else if !tc.serverUp {
				stop()
			}
			setVersion(t, "2.0.0")
			t.Setenv(update.BootedEnv, "1")
			e.agent(dir, "--enrol-token-file", tok)
			st, _ := s.Load()
			if confirmed := st.Pending == nil; confirmed != tc.confirmed {
				t.Fatalf("confirmed=%v, want %v (state %+v)", confirmed, tc.confirmed, st)
			}
			if n := spooled(dir); n != tc.wantSpooled {
				t.Fatalf("%d batches in the spool, want %d", n, tc.wantSpooled)
			}
		})
	}
}

// A new version that keeps the server unreachable, when the old one reached it,
// is stopped at the health timeout, so that the next start rolls back.
func TestProbationStopsAVersionThatNoLongerDelivers(t *testing.T) {
	e := newSrvEnv(t)
	stop := e.start()
	stop()
	dir, s := stage(t, "2.0.0")
	setVersion(t, "2.0.0")
	t.Setenv(update.BootedEnv, "1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out, errb strings.Builder
	code := run(ctx, []string{"--root", fixture, "--interval", "1h", "--health-timeout", "300ms",
		"--server", e.url(), "--ca-file", e.caFile, "--state-dir", dir, "--enrol-token-file", e.token("agent:host-01")}, &out, &errb)
	if ctx.Err() != nil {
		t.Fatal("still running long after the health timeout: the spooled batch passed probation")
	}
	if code != 1 || !strings.Contains(errb.String(), "health timeout") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if st, _ := s.Load(); st.Pending == nil {
		t.Fatal("confirmed a version whose batches were only spooled")
	}
}

// The delivery can come after the emit: a running agent whose spool is
// flushed before the health timeout is confirmed, and keeps running.
func TestProbationIsConfirmedByADeliveryFromTheSpool(t *testing.T) {
	old := flushEvery
	flushEvery = 100 * time.Millisecond
	t.Cleanup(func() { flushEvery = old })

	e := newSrvEnv(t)
	stop := e.start()
	stop()
	dir, s := stage(t, "2.0.0")
	setVersion(t, "2.0.0")
	t.Setenv(update.BootedEnv, "1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	var errb strings.Builder
	go func() {
		var out strings.Builder
		done <- run(ctx, []string{"--root", fixture, "--interval", "1h", "--health-timeout", "3s",
			"--server", e.url(), "--ca-file", e.caFile, "--state-dir", dir, "--enrol-token-file", e.token("agent:host-01")}, &out, &errb)
	}()
	waitFor(t, "the batch to be spooled", func() bool { return spooled(dir) == 1 })
	if st, _ := s.Load(); st.Pending == nil {
		t.Fatal("confirmed by a spooled batch")
	}
	e.start()
	waitFor(t, "the spool to be delivered", func() bool { return len(e.rec.sources()) == 1 })
	waitFor(t, "the version to be confirmed", func() bool { st, _ := s.Load(); return st.Pending == nil })
	select {
	case code := <-done:
		t.Fatalf("a confirmed version stopped: exit %d", code)
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not stop when cancelled")
	}
}

func TestOutputIsALocalCopyWhenServing(t *testing.T) {
	e := newSrvEnv(t)
	e.start()
	dir := t.TempDir()
	copyPath := filepath.Join(t.TempDir(), "copy.json")
	if code, _, errs := e.agent(dir, "--enrol-token-file", e.token("agent:host-01"), "--output", copyPath); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("no local copy: %v", err)
	}
	if len(e.rec.sources()) != 1 {
		t.Fatalf("server got %v", e.rec.sources())
	}
}

func TestTransportFlagsComeFromTheEnvironment(t *testing.T) {
	e := newSrvEnv(t)
	e.start()
	dir := t.TempDir()
	t.Setenv("TRICA_AGENT_SERVER", e.url())
	t.Setenv("TRICA_AGENT_CA_FILE", e.caFile)
	t.Setenv("TRICA_AGENT_ENROL_TOKEN_FILE", e.token("agent:host-01"))
	if code, _, errs := runArgs(t, "--root", fixture, "--once", "--state-dir", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := e.rec.sources(); !slices.Equal(got, []string{"agent:host-01"}) {
		t.Fatalf("server got %v", got)
	}
}

func TestBadTransportFlags(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	for name, args := range map[string][]string{
		"no CA":                    {"--server", "https://127.0.0.1:1", "--state-dir", dir},
		"no state dir":             {"--server", "https://127.0.0.1:1", "--ca-file", ca},
		"plain http":               {"--server", "http://127.0.0.1:1", "--ca-file", ca, "--state-dir", dir},
		"no host":                  {"--server", "https://", "--ca-file", ca, "--state-dir", dir},
		"not a url":                {"--server", "127.0.0.1:1", "--ca-file", ca, "--state-dir", dir},
		"metrics without a server": {"--metrics-file", filepath.Join(dir, "trica-agent.prom")},
	} {
		if code, _, errs := runArgs(t, append([]string{"--root", fixture, "--once"}, args...)...); code != 2 {
			t.Errorf("%s: exit %d, want 2: %s", name, code, errs)
		}
	}
	// A CA file that cannot be read is a start-up failure, not a silent skip.
	if code, _, _ := runArgs(t, "--root", fixture, "--once", "--server", "https://127.0.0.1:1", "--ca-file", ca, "--state-dir", dir); code != 1 {
		t.Errorf("missing CA file: exit %d, want 1", code)
	}
}
