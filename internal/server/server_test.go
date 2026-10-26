package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// patience bounds every wait: generous, because CI runners and developer
// machines running several suites are slow, and a wait only ends early.
const patience = 90 * time.Second

func testConfig(t *testing.T) *Config {
	t.Helper()
	if os.Getenv(EnvDatabaseURL) != "" {
		t.Skip(EnvDatabaseURL + " is set; it would override the test database")
	}
	// The URL goes through a file, not the environment, so tests run in parallel.
	c := &Config{Database: DatabaseConfig{URLFile: writeFile(t, "url", testdb.NewURL(t))}}
	c.defaults()
	c.HTTP.Addr = "127.0.0.1:0"
	c.SweepInterval = 20 * time.Millisecond
	return c
}

// start runs the server and waits until it is ready; the returned function
// stops it and returns Run's error.
func start(t *testing.T, s *Server) (base string, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.Now().Add(patience)
	for !s.ready.Load() {
		select {
		case err := <-done:
			cancel()
			t.Fatalf("server exited before ready: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("server not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var once bool
	var err error
	return "http://" + s.Addr(), func() error {
		if !once {
			once = true
			cancel()
			err = <-done
		}
		return err
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestServerServesComponentsAndSweeps(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Policy.AbsentAfter = time.Millisecond
	applied := make(chan string, 1)
	feeder := Component{
		Name: "feeder",
		Mount: func(mux *http.ServeMux, d Deps) error {
			if d.Store == nil || d.Engine == nil || d.Schema() == nil || d.Log == nil || d.Metrics == nil {
				return errors.New("incomplete deps")
			}
			mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "pong") })
			return nil
		},
		Run: func(ctx context.Context, d Deps) error {
			b := sdk.NewBatch("test:feeder", sdk.ScopeFull)
			b.Object("sw", "device").Identify("serial", "SN-A1").Identify("name", "sw-a1")
			r, err := d.Engine.Apply(ctx, b, "", "")
			if err != nil {
				return err
			}
			applied <- r.Objects["sw"]
			<-ctx.Done()
			return nil
		},
	}
	s := New(cfg, nil, "1.2.3", feeder)
	base, stop := start(t, s)

	for path, want := range map[string]int{"/healthz": 200, "/readyz": 200, "/ping": 200, "/nowhere": 404} {
		if code, _ := get(t, base+path); code != want {
			t.Errorf("%s: %d, want %d", path, code, want)
		}
	}

	// The migrations ran on start, the component wrote through the engine and
	// the sweeper marked the object absent: it is still there, not deleted.
	var id string
	select {
	case id = <-applied:
	case <-time.After(patience):
		t.Fatal("component did not run")
	}
	url, _ := cfg.DatabaseURL()
	st, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	deadline := time.Now().Add(patience)
	for {
		o, err := st.Object(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if o.Status == "absent" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("object never swept: %s", o.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	_, metrics := get(t, base+"/metrics")
	for _, want := range []string{`trica_build_info{version="1.2.3"} 1`, "trica_absence_sweeps_total", "trica_objects_marked_absent_total 1", "go_goroutines"} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics lack %q", want)
		}
	}

	if err := stop(); err != nil {
		t.Fatalf("graceful stop returned %v", err)
	}
	if s.ready.Load() {
		t.Fatal("still ready after stop")
	}
	if _, err := http.Get(base + "/healthz"); err == nil {
		t.Fatal("still listening after stop")
	}
}

func TestServerWithoutMigrationsIsNotReadyToServeTheSchema(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	off := false
	cfg.MigrateOnStart = &off
	s := New(cfg, nil, "dev")
	_, stop := start(t, s)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	url, _ := cfg.DatabaseURL()
	st, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, err := st.SchemaVersion(context.Background()); err != nil || v != 0 {
		t.Fatalf("migrate_on_start=false still migrated: version %d, %v", v, err)
	}
}

func TestComponentFailureStopsTheServer(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	boom := errors.New("boom")
	s := New(cfg, nil, "dev", Component{Name: "broken", Run: func(context.Context, Deps) error { return boom }})
	err := s.Run(context.Background())
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("Run = %v, want the component's error, named", err)
	}
}

func TestMountFailureAbortsStart(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	s := New(cfg, nil, "dev", Component{Name: "api", Mount: func(*http.ServeMux, Deps) error { return errors.New("bad route") }})
	if err := s.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "mount api") {
		t.Fatalf("Run = %v", err)
	}
}

func TestRunFailsWithoutDatabase(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if err := New(cfg, nil, "dev").Run(context.Background()); err == nil {
		t.Fatal("ran without a database URL")
	}
	t.Setenv(EnvDatabaseURL, "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err := New(cfg, nil, "dev").Run(context.Background()); err == nil {
		t.Fatal("ran with an unreachable database")
	}
}

func TestListenFailure(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.HTTP.Addr = "256.0.0.1:1"
	if err := New(cfg, nil, "dev").Run(context.Background()); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("Run = %v", err)
	}
}

func TestReadyzReportsTheDatabase(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	var store *store.Store
	grab := Component{Name: "grab", Mount: func(_ *http.ServeMux, d Deps) error { store = d.Store; return nil }}
	s := New(cfg, nil, "dev", grab)
	base, stop := start(t, s)
	defer func() { _ = stop() }()
	store.Pool.Close()
	if code, _ := get(t, base+"/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with the pool closed: %d", code)
	}
	if code, _ := get(t, base+"/healthz"); code != http.StatusOK {
		t.Fatalf("healthz must not depend on the database: %d", code)
	}
}

func TestLoadSchemaOverlays(t *testing.T) {
	t.Parallel()
	ok := writeFile(t, "ov.yaml", "types:\n  device:\n    attributes:\n      cost_center: {kind: string}\n")
	reg, err := LoadSchema([]string{ok})
	if err != nil {
		t.Fatal(err)
	}
	if a := reg.Types["device"].Attributes["cost_center"]; a == nil || !a.Custom {
		t.Fatal("overlay not merged")
	}
	for name, paths := range map[string][]string{
		"missing": {ok + ".missing"},
		"invalid": {writeFile(t, "bad.yaml", "types: [")},
		"merge":   {writeFile(t, "orphan.yaml", "types: {x: {parents: [nowhere]}}\n")},
	} {
		if _, err := LoadSchema(paths); err == nil {
			t.Errorf("%s overlay accepted", name)
		}
	}
	cfg := testConfig(t)
	cfg.SchemaOverlays = []string{ok + ".missing"}
	if err := New(cfg, nil, "dev").Run(context.Background()); err == nil {
		t.Fatal("server started with a broken overlay")
	}
}

// Display names stored under an older rule are rewritten at start (ADR 0065).
func TestStartRecomputesStoredDisplayNames(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	feeder := Component{Name: "feeder", Run: func(ctx context.Context, d Deps) error {
		b := sdk.NewBatch("test:feeder", sdk.ScopeFull)
		b.Object("srv", "device").Identify("serial", "SN-S1").Identify("name", "srv1")
		b.Object("cpu", "cpu").Identify("name", "Proc 1").In("srv").Set("model", "Intel(R) Xeon(R) CPU E5-2695 v4 @ 2.10GHz")
		_, err := d.Engine.Apply(ctx, b, "", "")
		<-ctx.Done()
		return err
	}}
	_, stop := start(t, New(cfg, nil, "dev", feeder))
	url, _ := cfg.DatabaseURL()
	st, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	display := func() (d string) {
		_ = st.Pool.QueryRow(context.Background(), `SELECT display FROM objects WHERE type = 'cpu'`).Scan(&d)
		return d
	}
	deadline := time.Now().Add(patience)
	for display() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	// Back to how the database was before templates existed.
	if _, err := st.Pool.Exec(context.Background(), `UPDATE objects SET display = 'Proc 1' WHERE type = 'cpu'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(context.Background(), `DELETE FROM display_rules`); err != nil {
		t.Fatal(err)
	}
	_, stop = start(t, New(cfg, nil, "dev"))
	defer func() { _ = stop() }()
	if got, want := display(), "Proc 1 · Intel Xeon E5-2695 v4 @ 2.10GHz"; got != want {
		t.Fatalf("display after restart: %q, want %q", got, want)
	}
}
