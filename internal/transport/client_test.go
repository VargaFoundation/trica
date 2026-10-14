package transport

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

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

func TestSpoolSurvivesClientRestartAndFlush(t *testing.T) {
	e := newEnv(t)
	stop := e.start()
	c := e.client("agent:host-01")
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
	cfg := c.cfg
	cfg.EnrolToken = ""
	again, err := NewClient(cfg)
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
	stop := e.start()
	size := func() int64 {
		b, err := encodeBatch(sized("agent:host-01:0"))
		if err != nil {
			t.Fatal(err)
		}
		return int64(len(b))
	}()
	// Room for three batches, not four.
	c := e.client("agent:host-01", func(cfg *ClientConfig) { cfg.MaxSpoolBytes = 3*size + size/2 })
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
	if st.Spooled != 3 || st.Dropped != 3 || st.SpoolBytes > c.cfg.MaxSpoolBytes {
		t.Fatalf("stats = %+v (budget %d)", st, c.cfg.MaxSpoolBytes)
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

func TestBatchLargerThanTheSpoolIsDroppedAndReported(t *testing.T) {
	ca, _ := pki.NewCA("test", time.Now())
	dir := t.TempDir()
	provision(t, ca, dir, "agent:host-01", time.Hour, time.Now())
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, ca.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(ClientConfig{ServerURL: "https://127.0.0.1:1", CAFile: caFile, Dir: dir, MaxSpoolBytes: 64, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	big := batch("agent:host-01")
	big.Object("pad", "device").Identify("name", "pad").Set("blob", randomHex(t, 500))
	err = c.Emit(context.Background(), big)
	if !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("err = %v, want ErrSpoolFull", err)
	}
	if st := c.Stats(); st.Dropped != 1 || st.Spooled != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hexOf(b)
}

func TestRenewalHappensBeforeExpiry(t *testing.T) {
	e := newEnv(t)
	e.start()
	clk := newClock()
	c := e.client("agent:host-01", func(cfg *ClientConfig) { cfg.Now = clk.Now })
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
	first := c.currentCert()
	if got := first.NotAfter.Sub(first.NotBefore); got < 7*24*time.Hour {
		t.Fatalf("validity = %v", got)
	}

	advance(4 * 24 * time.Hour) // 3 days left of 7: more than a third
	emit()
	if st := c.Stats(); st.Renewals != 0 || c.currentCert().SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatalf("renewed too early: %+v", st)
	}

	advance(24 * time.Hour) // 2 days left: less than a third (2d8h)
	emit()
	renewed := c.currentCert()
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
	cfg := c.cfg
	cfg.EnrolToken = ""
	again, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.currentCert().SerialNumber.Cmp(renewed.SerialNumber) != 0 {
		t.Fatal("the renewed certificate was not persisted")
	}
	if got := e.rec.sources(); len(got) != 3 {
		t.Fatalf("batches at the server = %v", got)
	}
	if info, err := os.Stat(filepath.Join(c.cfg.Dir, identityFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity file: %v %v", info, err)
	}
}

// prog is a server whose answers the test scripts.
type prog struct {
	mu          sync.Mutex
	batchStatus int
	renewStatus int
	enrolFn     http.HandlerFunc
	got         []string
	renews      int
}

func (p *prog) set(batch, renew int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.batchStatus, p.renewStatus = batch, renew
}

func (p *prog) sources() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.got...)
}

func (p *prog) handler(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.URL.Path {
	case PathBatches:
		if p.batchStatus >= 200 && p.batchStatus < 300 {
			zr, err := gzip.NewReader(r.Body)
			if err == nil {
				var b sdk.Batch
				if json.NewDecoder(zr).Decode(&b) == nil {
					p.got = append(p.got, b.Source)
				}
			}
		}
		w.WriteHeader(p.batchStatus)
	case PathRenew:
		p.renews++
		w.WriteHeader(p.renewStatus)
	case PathEnrol:
		p.enrolFn(w, r)
	}
}

// fakeClient is a client with an identity already on disk (no enrolment) talking to a scripted server.
func fakeClient(t *testing.T, p *prog, binding string, ttl time.Duration, mut ...func(*ClientConfig)) (*Client, *pki.CA) {
	t.Helper()
	ca, err := pki.NewCA("test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	url, caFile := fakeServer(t, ca, p.handler)
	dir := t.TempDir()
	provision(t, ca, dir, binding, ttl, time.Now())
	cfg := ClientConfig{ServerURL: url, CAFile: caFile, Dir: dir, Timeout: 5 * time.Second}
	for _, m := range mut {
		m(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, ca
}

func TestFailureClassification(t *testing.T) {
	cases := []struct {
		status  int
		spooled bool
	}{
		{http.StatusBadRequest, false},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusRequestEntityTooLarge, false},
		{http.StatusUnprocessableEntity, false},
		{http.StatusRequestTimeout, true},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			p := &prog{}
			p.set(tc.status, 200)
			c, _ := fakeClient(t, p, "agent:host-01", time.Hour)
			err := c.Emit(context.Background(), batch("agent:host-01"))
			st := c.Stats()
			if tc.spooled {
				if err != nil || st.Spooled != 1 {
					t.Fatalf("%d must be spooled and not returned: err=%v stats=%+v", tc.status, err, st)
				}
				return
			}
			if code := statusOf(t, err); code != tc.status {
				t.Fatalf("err = %v", err)
			}
			if st.Spooled != 0 {
				t.Fatalf("%d must not be spooled: %+v", tc.status, st)
			}
			if !strings.Contains(err.Error(), "server answered") {
				t.Fatalf("error text = %q", err)
			}
		})
	}
}

func TestFlushOutcomes(t *testing.T) {
	p := &prog{}
	p.set(http.StatusServiceUnavailable, 200)
	c, _ := fakeClient(t, p, "agent:host-01", time.Hour)
	ctx := context.Background()
	for _, src := range srcs("agent:host-01:", 1, 3) {
		if err := c.Emit(ctx, batch(src)); err != nil {
			t.Fatal(err)
		}
	}
	if st := c.Stats(); st.Spooled != 3 {
		t.Fatalf("stats = %+v", st)
	}
	// 503 on resend: the spool is kept untouched.
	if err := c.Flush(ctx); statusOf(t, err) != http.StatusServiceUnavailable {
		t.Fatalf("flush err = %v", err)
	}
	// 401 on resend: credentials problem, the spool is kept.
	p.set(http.StatusUnauthorized, 200)
	if err := c.Flush(ctx); statusOf(t, err) != http.StatusUnauthorized {
		t.Fatalf("flush err = %v", err)
	}
	if st := c.Stats(); st.Spooled != 3 || st.Rejected != 0 {
		t.Fatalf("a 401 must keep the spool: %+v", st)
	}
	// 400 on resend: these batches will never be accepted, they are discarded
	// instead of blocking the queue forever.
	p.set(http.StatusBadRequest, 200)
	if err := c.Flush(ctx); err != nil {
		t.Fatalf("flush err = %v", err)
	}
	if st := c.Stats(); st.Spooled != 0 || st.Rejected != 3 {
		t.Fatalf("stats = %+v", st)
	}
	// An unreadable file is discarded too, and counted.
	p.set(http.StatusServiceUnavailable, 200)
	if err := c.Emit(ctx, batch("agent:host-01:4")); err != nil {
		t.Fatal(err)
	}
	files := spoolFiles(t, c.cfg.SpoolDir)
	if len(files) != 1 {
		t.Fatalf("spool = %v", files)
	}
	if err := os.Chmod(filepath.Join(c.cfg.SpoolDir, files[0]), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(filepath.Join(c.cfg.SpoolDir, files[0])); err == nil {
		t.Skip("file permissions are not enforced here (running as root?)")
	}
	p.set(http.StatusNoContent, 200)
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if st := c.Stats(); st.Spooled != 0 || st.Rejected != 4 {
		t.Fatalf("stats = %+v", st)
	}
	if got := p.sources(); len(got) != 0 {
		t.Fatalf("server got %v", got)
	}
}

func TestInvalidBatchIsNotSentNorSpooled(t *testing.T) {
	p := &prog{}
	p.set(http.StatusNoContent, 200)
	c, _ := fakeClient(t, p, "agent:host-01", time.Hour)
	bad := batch("agent:host-01")
	bad.Confidence = "maybe"
	if err := c.Emit(context.Background(), bad); err == nil {
		t.Fatal("an invalid batch must be refused at the source")
	}
	if st := c.Stats(); st.Spooled != 0 || st.Sent != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if got := p.sources(); len(got) != 0 {
		t.Fatalf("server got %v", got)
	}
}

func TestRenewalFailure(t *testing.T) {
	// A failing renewal is harmless while the certificate is still valid...
	p := &prog{}
	p.set(http.StatusNoContent, http.StatusInternalServerError)
	clk := newClock()
	c, _ := fakeClient(t, p, "agent:host-01", 24*time.Hour, func(cfg *ClientConfig) { cfg.Now = clk.Now })
	ctx := context.Background()
	clk.Advance(20 * time.Hour) // 4h left of 24: inside the renewal window
	if err := c.Emit(ctx, batch("agent:host-01")); err != nil {
		t.Fatalf("a failed renewal must not stop a valid certificate from sending: %v", err)
	}
	if st := c.Stats(); st.Renewals != 0 || st.Sent != 1 {
		t.Fatalf("stats = %+v", st)
	}
	p.mu.Lock()
	renews := p.renews
	p.mu.Unlock()
	if renews == 0 {
		t.Fatal("a renewal must have been attempted")
	}
	// ... and fatal once the certificate is expired and cannot be renewed:
	// transient (5xx) means the batch waits in the spool...
	clk.Advance(5 * time.Hour)
	if err := c.Emit(ctx, batch("agent:host-01")); err != nil {
		t.Fatal(err)
	}
	if st := c.Stats(); st.Spooled != 1 || st.Sent != 1 {
		t.Fatalf("stats = %+v", st)
	}
	// ... while a refusal (403) is an error for the caller.
	p.set(http.StatusNoContent, http.StatusForbidden)
	err := c.Emit(ctx, batch("agent:host-01"))
	if code := statusOf(t, err); code != http.StatusForbidden {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnrolmentErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("no token", func(t *testing.T) {
		e := newEnv(t)
		e.start()
		c := e.client("agent:host-01", func(cfg *ClientConfig) { cfg.EnrolToken = "" })
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
		c := e.client("agent:host-01", func(cfg *ClientConfig) {
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
		c := e.client("agent:host-01", func(cfg *ClientConfig) {
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
	t.Run("certificate from another CA", func(t *testing.T) {
		p := &prog{}
		ca, _ := pki.NewCA("trusted", time.Now())
		rogue, _ := pki.NewCA("rogue", time.Now())
		p.enrolFn = func(w http.ResponseWriter, r *http.Request) {
			var req enrolRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			csr, _ := pki.ParseCSRPEM([]byte(req.CSR))
			cert, err := rogue.IssueClient(csr, "agent:host-01", time.Hour, time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			_ = json.NewEncoder(w).Encode(certResponse{Certificate: string(pki.CertPEM(cert))})
		}
		url, caFile := fakeServer(t, ca, p.handler)
		c, err := NewClient(ClientConfig{ServerURL: url, CAFile: caFile, Dir: t.TempDir(), EnrolToken: "tok", Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Emit(ctx, batch("agent:host-01")); err == nil || !strings.Contains(err.Error(), "cannot verify") {
			t.Fatalf("a certificate not signed by our CA must be refused: %v", err)
		}
		if c.currentCert() != nil {
			t.Fatal("an unverifiable certificate must not be installed")
		}
	})
	t.Run("certificate for another key", func(t *testing.T) {
		p := &prog{}
		ca, _ := pki.NewCA("trusted", time.Now())
		p.enrolFn = func(w http.ResponseWriter, _ *http.Request) {
			other := issueIdentity(t, ca, "agent:host-01", time.Hour, time.Now())
			_ = json.NewEncoder(w).Encode(certResponse{Certificate: string(pki.CertPEM(other.Leaf))})
		}
		url, caFile := fakeServer(t, ca, p.handler)
		c, err := NewClient(ClientConfig{ServerURL: url, CAFile: caFile, Dir: t.TempDir(), EnrolToken: "tok", Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Emit(ctx, batch("agent:host-01")); err == nil || !strings.Contains(err.Error(), "another key") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("garbage certificate", func(t *testing.T) {
		p := &prog{}
		ca, _ := pki.NewCA("trusted", time.Now())
		p.enrolFn = func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(certResponse{Certificate: "junk"})
		}
		url, caFile := fakeServer(t, ca, p.handler)
		c, err := NewClient(ClientConfig{ServerURL: url, CAFile: caFile, Dir: t.TempDir(), EnrolToken: "tok", Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Emit(ctx, batch("agent:host-01")); err == nil {
			t.Fatal("garbage must be refused")
		}
	})
}

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

func TestNewClientValidation(t *testing.T) {
	ca, _ := pki.NewCA("test", time.Now())
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, ca.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	ok := ClientConfig{ServerURL: "https://127.0.0.1:1", CAFile: caFile, Dir: t.TempDir()}
	if c, err := NewClient(ok); err != nil {
		t.Fatal(err)
	} else {
		c.Close()
		if c.cfg.MaxSpoolBytes != DefaultMaxSpoolBytes || c.cfg.SpoolDir != filepath.Join(ok.Dir, "spool") {
			t.Errorf("defaults not applied: %+v", c.cfg)
		}
	}
	for name, mut := range map[string]func(*ClientConfig){
		"no url": func(c *ClientConfig) { c.ServerURL = "" },
		"no ca":  func(c *ClientConfig) { c.CAFile = "" },
		"no dir": func(c *ClientConfig) { c.Dir = "" },
		"bad ca": func(c *ClientConfig) { c.CAFile = filepath.Join(t.TempDir(), "missing") },
		"junk ca": func(c *ClientConfig) {
			p := filepath.Join(t.TempDir(), "junk")
			_ = os.WriteFile(p, []byte("junk"), 0o600)
			c.CAFile = p
		},
	} {
		cfg := ok
		cfg.Dir = t.TempDir()
		mut(&cfg)
		if _, err := NewClient(cfg); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	// A damaged identity file is an error, not a silent re-enrolment.
	for name, content := range map[string]string{"junk": "junk", "key only": "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"} {
		cfg := ok
		cfg.Dir = t.TempDir()
		if err := os.WriteFile(filepath.Join(cfg.Dir, identityFile), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewClient(cfg); err == nil {
			t.Errorf("identity %s: expected an error", name)
		}
	}
	// A key that does not match its certificate.
	a := issueIdentity(t, ca, "agent:host-01", time.Hour, time.Now())
	b := issueIdentity(t, ca, "agent:host-01", time.Hour, time.Now())
	cfg := ok
	cfg.Dir = t.TempDir()
	provisionMismatch(t, cfg.Dir, a, b)
	if _, err := NewClient(cfg); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Errorf("mismatched identity: %v", err)
	}
}
