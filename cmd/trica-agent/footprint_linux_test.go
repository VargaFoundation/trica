package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/pki"
	"github.com/VargaFoundation/trica/internal/transport"
)

// The footprint the agent promises (ADR-0015, ADR-0022): under 50 MB resident
// and under 1 % of one core. TestRealFootprint measures the real binary, on the
// real host, over a full collection cycle; the CI job `agent` runs it as root on
// the GitHub runner. The Go-heap figures over a fake root, in internal/agent,
// are the cheap early warning; this is the measurement.
const (
	maxRSSBytes = 50 << 20
	maxCPUShare = 0.01
	// clockTicks is USER_HZ, the unit of utime and stime in /proc/<pid>/stat:
	// 100 on every Linux the agent runs on.
	clockTicks = 100
)

// footprintError says which threshold was crossed, or "" when none was.
func footprintError(rssBytes int64, cpuShare float64) string {
	var over []string
	if rssBytes > maxRSSBytes {
		over = append(over, fmt.Sprintf("peak resident set %.1f MB > %d MB", float64(rssBytes)/(1<<20), maxRSSBytes>>20))
	}
	if cpuShare > maxCPUShare {
		over = append(over, fmt.Sprintf("CPU %.3f %% of a core > %.0f %%", cpuShare*100, maxCPUShare*100))
	}
	return strings.Join(over, "; ")
}

// The promise is 50 MB and 1 %: loosening the constants is a decision, so it
// has to touch this test too.
func TestFootprintPromise(t *testing.T) {
	if maxRSSBytes != 50<<20 || maxCPUShare != 0.01 {
		t.Errorf("thresholds are %d bytes and %v of a core, the promise is 50 MB and 1 %%", maxRSSBytes, maxCPUShare)
	}
}

func TestFootprintThresholds(t *testing.T) {
	for _, tc := range []struct {
		rss  int64
		cpu  float64
		fail bool
	}{
		{10 << 20, 0.0005, false},
		{maxRSSBytes, maxCPUShare, false},
		{maxRSSBytes + 1, 0, true},
		{0, maxCPUShare + 0.0001, true},
		{80 << 20, 0.05, true},
	} {
		if got := footprintError(tc.rss, tc.cpu) != ""; got != tc.fail {
			t.Errorf("rss=%d cpu=%v: failed=%v, want %v", tc.rss, tc.cpu, got, tc.fail)
		}
	}
}

// procCPUSeconds is utime+stime of a process, from /proc/<pid>/stat.
func procCPUSeconds(pid int) (float64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	// The command name may contain spaces and parentheses: fields start after the last ')'.
	s := string(b)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	if len(f) < 13 {
		return 0, fmt.Errorf("short /proc/%d/stat", pid)
	}
	ut, err1 := strconv.ParseFloat(f[11], 64)
	st, err2 := strconv.ParseFloat(f[12], 64)
	if err1 != nil || err2 != nil {
		return 0, fmt.Errorf("bad utime/stime in /proc/%d/stat", pid)
	}
	return (ut + st) / clockTicks, nil
}

// procPeakRSS is VmHWM, the peak resident set size, from /proc/<pid>/status.
func procPeakRSS(pid int) (int64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB")), 10, 64)
			return kb << 10, err
		}
	}
	return 0, fmt.Errorf("no VmHWM in /proc/%d/status", pid)
}

func TestProcParsers(t *testing.T) {
	self := os.Getpid()
	if rss, err := procPeakRSS(self); err != nil || rss <= 0 {
		t.Errorf("procPeakRSS(self) = %d, %v", rss, err)
	}
	if cpu, err := procCPUSeconds(self); err != nil || cpu < 0 {
		t.Errorf("procCPUSeconds(self) = %v, %v", cpu, err)
	}
}

// TestRealFootprint starts the binary named by TRICA_FOOTPRINT_BIN at the
// shortest interval (30 s) against the real host, waits for the second batch,
// and measures peak resident memory and CPU over that window: start-up, two
// full collections and the idle time between them, which is one full cycle at
// the shortest interval, the worst case. Without the variable it is skipped,
// unless TRICA_REQUIRE_FOOTPRINT=1 (set in CI), which makes the skip a failure.
func TestRealFootprint(t *testing.T) {
	bin := os.Getenv("TRICA_FOOTPRINT_BIN")
	if bin == "" {
		if os.Getenv("TRICA_REQUIRE_FOOTPRINT") == "1" {
			t.Fatal("TRICA_FOOTPRINT_BIN is not set: the footprint is not measured")
		}
		t.Skip("TRICA_FOOTPRINT_BIN not set")
	}
	out := filepath.Join(t.TempDir(), "batch.json")
	// The window ends when the batch file has been replaced once.
	var first time.Time
	measureFootprint(t, bin, []string{"--interval", "30s", "--output", out}, func() bool {
		st, err := os.Stat(out)
		if err != nil {
			return false
		}
		if first.IsZero() {
			first = st.ModTime()
			return false
		}
		return !st.ModTime().Equal(first)
	})
}

// TestRealFootprintWithTransport is the same measurement with the transport
// on: the agent enrolled earlier (an identity is in its state directory), and
// every batch goes over mutual TLS and HTTP/2 to a local server, on top of the
// background flush loop. The window ends at the server's second batch. The
// server is a stand-in that checks the client certificate against the CA and
// answers 204: the registry and the database are not part of the agent's cost.
func TestRealFootprintWithTransport(t *testing.T) {
	bin := os.Getenv("TRICA_FOOTPRINT_BIN")
	if bin == "" {
		if os.Getenv("TRICA_REQUIRE_FOOTPRINT") == "1" {
			t.Fatal("TRICA_FOOTPRINT_BIN is not set: the footprint is not measured")
		}
		t.Skip("TRICA_FOOTPRINT_BIN not set")
	}
	now := time.Now()
	ca, err := pki.NewCA("footprint CA", now)
	if err != nil {
		t.Fatal(err)
	}
	srvCert, err := ca.IssueServer([]string{"127.0.0.1"}, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	var batches atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == transport.PathBatches && r.TLS != nil && len(r.TLS.VerifiedChains) > 0 {
			batches.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	srv.EnableHTTP2 = true
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{srvCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: ca.Pool()}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	key, err := pki.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, err := pki.NewCSR(key)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := pki.ParseCSRPEM(csrPEM)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.IssueClient(csr, "agent:footprint", 24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := pki.MarshalKeyPEM(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.pem"), slices.Concat(keyPEM, pki.CertPEM(cert)), 0o600); err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, ca.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	measureFootprint(t, bin, []string{"--interval", "30s", "--server", srv.URL, "--ca-file", caFile, "--state-dir", dir},
		func() bool { return batches.Load() >= 2 })
}

// measureFootprint starts bin with args against the real host, waits until
// ended returns true (the second batch), and checks peak resident memory and
// CPU over that window against the promise.
func measureFootprint(t *testing.T, bin string, args []string, ended func() bool) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM) })

	deadline := time.Now().Add(3 * time.Minute)
	for !ended() {
		select {
		case err := <-done:
			t.Fatalf("the agent exited during the window: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no second batch within 3 minutes")
		}
		time.Sleep(100 * time.Millisecond)
	}
	window := time.Since(start)
	cpu, err := procCPUSeconds(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	rss, err := procPeakRSS(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	share := cpu / window.Seconds()
	t.Logf("window %s, CPU %.3f s = %.3f %% of a core, peak RSS %.1f MB (uid %d)",
		window.Round(time.Second), cpu, share*100, float64(rss)/(1<<20), os.Getuid())
	if msg := footprintError(rss, share); msg != "" {
		t.Fatal(msg)
	}
}
