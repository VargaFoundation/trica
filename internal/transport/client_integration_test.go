package transport

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/transport/uplink"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The client (internal/transport/uplink) against the real server: enrolment,
// outages, restarts, the spool bound and renewal. They live on the server
// side because only this package links both halves; the agent links the
// client alone (#64).

var fixedTime = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// sized returns a batch of a fixed observation time, so that batches of
// sources of equal length have the same encoded size.
func sized(source string) *sdk.Batch {
	b := batch(source)
	b.ObservedAt = fixedTime
	return b
}

func TestSpoolSurvivesServerOutageAndFlushesInOrder(t *testing.T) {
	e := newEnv(t)
	stop := e.start()
	c := e.client("agent:host-01")
	ctx := context.Background()
	if err := c.Emit(ctx, batch("agent:host-01:1")); err != nil {
		t.Fatal(err)
	}

	stop() // the outage
	for _, src := range srcs("agent:host-01:", 2, 4) {
		if err := c.Emit(ctx, batch(src)); err != nil {
			t.Fatalf("an outage must not surface as an error: %v", err)
		}
	}
	if st := c.Stats(); st.Spooled != 3 || st.Sent != 1 || st.Dropped != 0 {
		t.Fatalf("stats during the outage = %+v", st)
	}
	if got := e.rec.sources(); len(got) != 1 {
		t.Fatalf("sink got %v during the outage", got)
	}

	e.start() // the server is back, on the same address
	// A new batch must queue behind the spooled ones, not overtake them.
	if err := c.Emit(ctx, batch("agent:host-01:5")); err != nil {
		t.Fatal(err)
	}
	if got, want := e.rec.sources(), srcs("agent:host-01:", 1, 5); !slices.Equal(got, want) {
		t.Fatalf("order at the server = %v, want %v", got, want)
	}
	if st := c.Stats(); st.Spooled != 0 || st.Sent != 5 {
		t.Fatalf("stats after = %+v", st)
	}
}

// TestOutageKeepsThePort guards the test environment itself: while the server
// is stopped, nobody else may listen on its address (#67).
func TestOutageKeepsThePort(t *testing.T) {
	e := newEnv(t)
	stop := e.start()
	stop()
	if ln, err := net.Listen("tcp", e.addr); err == nil {
		_ = ln.Close()
		t.Fatalf("%s was free during the outage: another process could serve on it", e.addr)
	}
	e.start()
	c := e.client("agent:host-01")
	if err := c.Emit(context.Background(), batch("agent:host-01:1")); err != nil {
		t.Fatal(err)
	}
	if st := c.Stats(); st.Sent != 1 {
		t.Fatalf("after the restart on the held port: %+v", st)
	}
}

func TestSpoolSurvivesClientRestartAndFlush(t *testing.T) {
	e := newEnv(t)
	stop := e.start()
	cfg := e.clientCfg("agent:host-01")
	c := e.clientFrom(cfg)
	ctx := context.Background()
	if err := c.Emit(ctx, batch("agent:host-01:1")); err != nil {
		t.Fatal(err)
	}
	stop()
	for _, src := range srcs("agent:host-01:", 2, 4) {
		if err := c.Emit(ctx, batch(src)); err != nil {
			t.Fatal(err)
		}
	}
	c.Close()

	// The process restarts: a new client on the same directories. It has its
	// identity (no token any more) and finds the spool.
	cfg.EnrolToken = ""
	again, err := uplink.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if st := again.Stats(); st.Spooled != 3 {
		t.Fatalf("spool after restart = %+v", st)
	}
	if err := again.Flush(ctx); err == nil {
		t.Fatal("Flush against a dead server must report the failure")
	}
	if st := again.Stats(); st.Spooled != 3 {
		t.Fatalf("a failed flush must keep the spool: %+v", st)
	}
	// A batch emitted now is spooled behind the others.
	if err := again.Emit(ctx, batch("agent:host-01:5")); err != nil {
		t.Fatal(err)
	}
	e.start()
	if err := again.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := e.rec.sources(), srcs("agent:host-01:", 1, 5); !slices.Equal(got, want) {
		t.Fatalf("order at the server = %v, want %v", got, want)
	}
	if st := again.Stats(); st.Spooled != 0 {
		t.Fatalf("spool after flush = %+v", st)
	}
}

func TestSpoolBoundDropsOldestAndCounts(t *testing.T) {
	e := newEnv(t)
	// The size of one spooled batch, measured on a client that cannot reach
	// the server yet. Sources of equal length give batches of equal size.
	probe := e.client("agent:host-02")
	if err := probe.Emit(context.Background(), sized("agent:host-02:0")); err != nil {
		t.Fatal(err)
	}
	size := probe.Stats().SpoolBytes
	if size == 0 {
		t.Fatal("the probe batch was not spooled")
	}
	stop := e.start()
	// Room for three batches, not four.
	budget := 3*size + size/2
	c := e.client("agent:host-01", func(cfg *uplink.ClientConfig) { cfg.MaxSpoolBytes = budget })
	ctx := context.Background()
	if err := c.Emit(ctx, sized("agent:host-01:0")); err != nil { // enrols
		t.Fatal(err)
	}
	stop()
	for _, src := range srcs("agent:host-01:", 1, 6) {
		if err := c.Emit(ctx, sized(src)); err != nil {
			t.Fatal(err)
		}
	}
	st := c.Stats()
	if st.Spooled != 3 || st.Dropped != 3 || st.SpoolBytes > budget {
		t.Fatalf("stats = %+v (budget %d)", st, budget)
	}
	e.start()
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	// The oldest three (1, 2, 3) were dropped; the newest three survive, in order.
	want := []string{"agent:host-01:0", "agent:host-01:4", "agent:host-01:5", "agent:host-01:6"}
	if got := e.rec.sources(); !slices.Equal(got, want) {
		t.Fatalf("sink got %v, want %v", got, want)
	}
	if st := c.Stats(); st.Dropped != 3 {
		t.Fatalf("dropped = %d", st.Dropped)
	}
}

func TestRenewalHappensBeforeExpiry(t *testing.T) {
	e := newEnv(t)
	e.start()
	clk := newClock()
	cfg := e.clientCfg("agent:host-01")
	cfg.Now = clk.Now
	c := e.clientFrom(cfg)
	ctx := context.Background()
	emit := func() {
		t.Helper()
		if err := c.Emit(ctx, batch("agent:host-01")); err != nil {
			t.Fatal(err)
		}
	}
	advance := func(d time.Duration) { // client and server share the story's time
		clk.Advance(d)
		e.regTime.Advance(d)
	}
	emit() // enrols: a 7 day certificate
	first := c.Certificate()
	if got := first.NotAfter.Sub(first.NotBefore); got < 7*24*time.Hour {
		t.Fatalf("validity = %v", got)
	}

	advance(4 * 24 * time.Hour) // 3 days left of 7: more than a third
	emit()
	if st := c.Stats(); st.Renewals != 0 || c.Certificate().SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatalf("renewed too early: %+v", st)
	}

	advance(24 * time.Hour) // 2 days left: less than a third (2d8h)
	emit()
	renewed := c.Certificate()
	if st := c.Stats(); st.Renewals != 1 || renewed.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Fatalf("not renewed before expiry: %+v", st)
	}
	if !renewed.NotAfter.After(first.NotAfter) {
		t.Fatal("the renewed certificate must outlive the old one")
	}
	if b, _ := pki.BindingFromCert(renewed); b != "agent:host-01" {
		t.Fatalf("binding = %q", b)
	}
	// The new identity is on disk: a restart does not fall back to the old one.
	cfg.EnrolToken = ""
	again, err := uplink.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Certificate().SerialNumber.Cmp(renewed.SerialNumber) != 0 {
		t.Fatal("the renewed certificate was not persisted")
	}
	if got := e.rec.sources(); len(got) != 3 {
		t.Fatalf("batches at the server = %v", got)
	}
	if info, err := os.Stat(filepath.Join(cfg.Dir, uplink.IdentityFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity file: %v %v", info, err)
	}
}

// prog is a server whose answers the test scripts.

func TestRunFlushesInTheBackground(t *testing.T) {
	e := newEnv(t)
	stop := e.start()
	c := e.client("agent:host-01")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Emit(ctx, batch("agent:host-01:1")); err != nil {
		t.Fatal(err)
	}
	stop()
	for _, src := range srcs("agent:host-01:", 2, 3) {
		if err := c.Emit(ctx, batch(src)); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	go func() { c.Run(ctx, 20*time.Millisecond); close(done) }()
	time.Sleep(100 * time.Millisecond) // a few failed ticks while the server is down
	e.start()
	deadline := time.Now().Add(60 * time.Second)
	for len(e.rec.sources()) < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got, want := e.rec.sources(), srcs("agent:host-01:", 1, 3); !slices.Equal(got, want) {
		t.Fatalf("sink got %v, want %v", got, want)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// TestEnrolmentAgainstTheServer covers enrolment against the real server;
// the client's handling of a misbehaving server is in uplink.
func TestEnrolmentAgainstTheServer(t *testing.T) {
	ctx := context.Background()
	t.Run("no token", func(t *testing.T) {
		e := newEnv(t)
		e.start()
		c := e.client("agent:host-01", func(cfg *uplink.ClientConfig) { cfg.EnrolToken = "" })
		err := c.Emit(ctx, batch("agent:host-01"))
		if err == nil || !strings.Contains(err.Error(), "no enrolment token") {
			t.Fatalf("err = %v", err)
		}
		if st := c.Stats(); st.Spooled != 0 {
			t.Fatalf("a missing token cannot be fixed by retrying: %+v", st)
		}
	})
	t.Run("token file", func(t *testing.T) {
		e := newEnv(t)
		e.start()
		tf := filepath.Join(t.TempDir(), "token")
		c := e.client("agent:host-01", func(cfg *uplink.ClientConfig) {
			if err := os.WriteFile(tf, []byte(cfg.EnrolToken+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg.EnrolToken, cfg.EnrolTokenFile = "", tf
		})
		if err := c.Emit(ctx, batch("agent:host-01")); err != nil {
			t.Fatal(err)
		}
		if st := c.Stats(); st.Enrolments != 1 || st.Sent != 1 {
			t.Fatalf("stats = %+v", st)
		}
	})
	t.Run("missing token file", func(t *testing.T) {
		e := newEnv(t)
		c := e.client("agent:host-01", func(cfg *uplink.ClientConfig) {
			cfg.EnrolToken, cfg.EnrolTokenFile = "", filepath.Join(t.TempDir(), "absent")
		})
		if err := c.Emit(ctx, batch("agent:host-01")); err == nil {
			t.Fatal("a missing token file must be an error")
		}
	})
	t.Run("enrol after an outage", func(t *testing.T) {
		e := newEnv(t)
		stop := e.start()
		stop()
		c := e.client("agent:host-01")
		// Never enrolled, server down: the batch waits.
		if err := c.Emit(ctx, batch("agent:host-01:1")); err != nil {
			t.Fatal(err)
		}
		if st := c.Stats(); st.Spooled != 1 || st.Enrolments != 0 {
			t.Fatalf("stats = %+v", st)
		}
		e.start()
		if err := c.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		if st := c.Stats(); st.Spooled != 0 || st.Enrolments != 1 || st.Sent != 1 {
			t.Fatalf("stats = %+v", st)
		}
		if got := e.rec.sources(); !slices.Equal(got, []string{"agent:host-01:1"}) {
			t.Fatalf("sink got %v", got)
		}
	})
}
