package arista

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// runWith runs a collector against a fake after setup, and returns the batch.
func runWith(t *testing.T, version string, setup func(*FakeArista), tweak func(*collect.Target)) (*sdk.Batch, *FakeArista) {
	t.Helper()
	t.Setenv(PasswordEnv, FakePassword)
	f := NewFakeArista(version)
	t.Cleanup(f.Close)
	if setup != nil {
		setup(f)
	}
	tg := f.Target("sw")
	if tweak != nil {
		tweak(&tg)
	}
	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	pinClock(c)
	batches := collecttest.Run(t, c)
	if len(batches) != 1 {
		t.Fatalf("got %d batches, want 1", len(batches))
	}
	return batches[0], f
}

// wantFailed asserts the run produced the health-only batch of a total failure.
func wantFailed(t *testing.T, b *sdk.Batch, errPart string) {
	t.Helper()
	if b.Health == nil || b.Health.OK {
		t.Fatalf("health = %+v, want a failure", b.Health)
	}
	if !strings.Contains(b.Health.Error, errPart) {
		t.Errorf("health error %q does not mention %q", b.Health.Error, errPart)
	}
	if b.Scope != sdk.ScopePartial || len(b.Objects) != 0 || len(b.Relations) != 0 {
		t.Errorf("a failed run must not claim anything: scope=%v objects=%d relations=%d", b.Scope, len(b.Objects), len(b.Relations))
	}
	if b.Source != "arista:sw" || b.Health.Collector != Kind {
		t.Errorf("source=%q collector=%q", b.Source, b.Health.Collector)
	}
}

func TestAuthRefused(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*FakeArista)
		tweak func(*collect.Target)
	}{
		{"switch refuses every login", func(f *FakeArista) { f.RejectCredentials() }, nil},
		{"wrong user", nil, func(tg *collect.Target) { tg.Username = "someone-else" }},
		{"wrong password", nil, func(tg *collect.Target) { tg.Password = "env:TRICA_FAKE_ARISTA_OTHER" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRICA_FAKE_ARISTA_OTHER", "not-the-password")
			b, f := runWith(t, EOS414, tc.setup, tc.tweak)
			wantFailed(t, b, "authentication refused")
			// Refused credentials are not retried command by command.
			if n := len(f.Requests()); n != 1 {
				t.Errorf("%d requests after a refused login, want 1", n)
			}
			if strings.Contains(b.Health.Error, FakePassword) {
				t.Error("the health error leaks the password")
			}
		})
	}
}

func TestUnresolvedSecret(t *testing.T) {
	t.Setenv(PasswordEnv, "")
	f := NewFakeArista(EOS414)
	defer f.Close()
	tg := f.Target("sw")
	tg.Password = "env:TRICA_FAKE_ARISTA_UNSET"
	c, err := collect.New(tg)
	if err != nil {
		t.Fatal(err)
	}
	b := collecttest.Run(t, c)[0]
	wantFailed(t, b, "credentials")
	if len(f.Requests()) != 0 {
		t.Error("the collector contacted the switch without credentials")
	}
}

func TestTotalFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*FakeArista)
		tweak func(*collect.Target)
		want  string
	}{
		{"unparsable body", func(f *FakeArista) { f.MalformBody() }, nil, "malformed response"},
		{"HTTP error", func(f *FakeArista) { f.HTTPStatus(http.StatusInternalServerError) }, nil, "HTTP 500"},
		{"result count mismatch", func(f *FakeArista) { f.ShortResult() }, nil, "results for"},
		{"show version refused", func(f *FakeArista) { f.FailCommand(cmdVersion) }, nil, cmdVersion},
		{"show version unreadable", func(f *FakeArista) { f.MalformCommand(cmdVersion) }, nil, "unexpected result"},
		{"switch too slow", func(f *FakeArista) { f.Delay(2 * time.Second) }, func(tg *collect.Target) { tg.Timeout = 150 * time.Millisecond }, "eAPI request"},
		{"nothing listens", nil, func(tg *collect.Target) { tg.Address = "http://127.0.0.1:1" }, "eAPI request"},
		{"https to a plain-HTTP switch", nil, func(tg *collect.Target) {
			tg.Address = strings.Replace(tg.Address, "http://", "https://", 1)
		}, "eAPI request"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			b, _ := runWith(t, EOS414, tc.setup, tc.tweak)
			wantFailed(t, b, tc.want)
			if time.Since(start) > 1500*time.Millisecond {
				t.Errorf("the run took %v: the timeout does not bound it", time.Since(start))
			}
		})
	}
}

func TestPartialFailures(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*FakeArista)
		failed []string
		// check asserts what is lost and what is kept.
		check func(*testing.T, *sdk.Batch)
	}{
		{
			name:   "one command refused",
			setup:  func(f *FakeArista) { f.FailCommand(cmdMLAG) },
			failed: []string{cmdMLAG},
			check: func(t *testing.T, b *sdk.Batch) {
				dev := obj(t, b, "device", "serial", "FAKE7050A0001")
				checkAbsent(t, dev, "mlag_domain", "mlag_state", "mlag_peer")
				checkAttrs(t, dev, map[string]any{"os_version": "4.14.2F"})
				if len(collecttest.Objects([]*sdk.Batch{b}, "interface")) != 11 {
					t.Error("the interfaces were lost with the MLAG state")
				}
			},
		},
		{
			name:   "several commands refused",
			setup:  func(f *FakeArista) { f.FailCommand(cmdMACTable, cmdLLDPNeighbors, cmdTransceiver) },
			failed: []string{cmdMACTable, cmdLLDPNeighbors, cmdTransceiver},
			check: func(t *testing.T, b *sdk.Batch) {
				checkAbsent(t, ifObj(t, b, "Ethernet1"), "learned_macs")
				if n := len(collecttest.Relations([]*sdk.Batch{b}, "neighbor_of")); n != 0 {
					t.Errorf("%d neighbor_of relations without LLDP data", n)
				}
				// Without DOM the transceivers are still known from the inventory.
				x := obj(t, b, "transceiver", "serial", "FAKEXCVR0001")
				checkAbsent(t, x, "tx_power_dbm", "temperature_c")
				checkAttrs(t, x, map[string]any{"model": "SFP-10G-SR"})
			},
		},
		{
			name:   "unreadable result",
			setup:  func(f *FakeArista) { f.MalformCommand(cmdVLAN) },
			failed: []string{cmdVLAN},
			check: func(t *testing.T, b *sdk.Batch) {
				if collecttest.Find([]*sdk.Batch{b}, "vlan", "key", "default:100") != nil {
					t.Error("a VLAN was invented from an unreadable result")
				}
				if len(collecttest.Objects([]*sdk.Batch{b}, "interface")) != 11 {
					t.Error("the interfaces were lost with the VLANs")
				}
			},
		},
		{
			name:   "environment unsupported on this model",
			setup:  func(f *FakeArista) { f.FailCommand(cmdTemperature, cmdCooling, cmdPower) },
			failed: []string{cmdTemperature, cmdCooling, cmdPower},
			check: func(t *testing.T, b *sdk.Batch) {
				for _, typ := range []string{"fan", "sensor"} {
					if n := len(collecttest.Objects([]*sdk.Batch{b}, typ)); n != 0 {
						t.Errorf("%d %s objects without environment data", n, typ)
					}
				}
				// The inventory still names the PSUs, but nothing says how they are doing.
				psu := obj(t, b, "psu", "serial", "FAKEPSU0001")
				checkAttrs(t, psu, map[string]any{"model": "PWR-460AC-F"})
				checkAbsent(t, psu, "health", "input_w")
				checkAbsent(t, obj(t, b, "device", "serial", "FAKE7050A0001"), "power_w", "health")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, f := runWith(t, EOS414, tc.setup, nil)
			if b.Scope != sdk.ScopePartial {
				t.Errorf("scope = %v, want partial: a lost command must not close objects", b.Scope)
			}
			if b.Health.OK {
				t.Fatal("health is OK after a failed command")
			}
			for _, cmd := range tc.failed {
				if !strings.Contains(b.Health.Error, cmd) {
					t.Errorf("health error %q does not name %q", b.Health.Error, cmd)
				}
			}
			if !strings.Contains(b.Health.Error, "partial collection") {
				t.Errorf("health error = %q", b.Health.Error)
			}
			// What the release has no JSON for is not counted with the failures.
			if strings.Contains(b.Health.Error, cmdUptime) || strings.Contains(b.Health.Error, "unconverted") {
				t.Errorf("health error %q counts an unavailable command as failed", b.Health.Error)
			}
			obj(t, b, "device", "serial", "FAKE7050A0001") // the rest is kept
			tc.check(t, b)
			checkReadOnly2(t, f)
		})
	}
}

// checkReadOnly2 asserts only reads happened, without the clean-run request count.
func checkReadOnly2(t *testing.T, f *FakeArista) {
	t.Helper()
	if ops := f.NonReadOperations(); len(ops) != 0 {
		t.Errorf("non-read operations: %v", ops)
	}
}

// TestFallbackPerCommand pins the way one refused command is contained: the
// batched request of its group fails, then the group is replayed one command
// per request, so only that command's data is lost.
func TestFallbackPerCommand(t *testing.T) {
	_, f := runWith(t, EOS420, func(f *FakeArista) { f.ConvertAll(); f.FailCommand(cmdMLAG) }, nil)
	reqs := f.Requests()

	var batched, single int
	asked := map[string]int{}
	for _, r := range reqs {
		switch {
		case len(r.Commands) > 1:
			batched++
		case len(r.Commands) == 1:
			single++
		}
		for _, c := range r.Commands {
			asked[c]++
		}
	}
	if batched != len(groups) {
		t.Errorf("%d batched requests, want %d (one per group)", batched, len(groups))
	}
	// Only the group holding show mlag is replayed: 9 commands, one per request.
	if single != len(groups[1]) {
		t.Errorf("%d single-command requests, want %d", single, len(groups[1]))
	}
	if asked[cmdMLAG] != 2 || asked[cmdVersion] != 1 || asked[cmdTemperature] != 1 {
		t.Errorf("show mlag asked %d times (want 2: batched, then alone); version %d, temperature %d (want 1 each)",
			asked[cmdMLAG], asked[cmdVersion], asked[cmdTemperature])
	}
}

func TestTLS(t *testing.T) {
	t.Setenv(PasswordEnv, FakePassword)
	f := NewFakeAristaTLS(EOS420)
	defer f.Close()
	if !strings.HasPrefix(f.URL(), "https://") {
		t.Fatalf("URL = %s", f.URL())
	}

	t.Run("insecure accepts the self-signed certificate", func(t *testing.T) {
		c, err := collect.New(f.Target("sw"))
		if err != nil {
			t.Fatal(err)
		}
		b := collecttest.Run(t, c)[0]
		if !b.Health.OK || b.Scope != sdk.ScopeFull {
			t.Fatalf("health = %+v", b.Health)
		}
		obj(t, b, "device", "serial", "FAKE7280B0001")
	})

	t.Run("verification refuses it by default", func(t *testing.T) {
		tg := f.Target("sw")
		tg.TLS.Insecure = false
		c, err := collect.New(tg)
		if err != nil {
			t.Fatal(err)
		}
		wantFailed(t, collecttest.Run(t, c)[0], "certificate")
	})

	t.Run("a bare host gets https", func(t *testing.T) {
		got, err := endpointURL("192.0.2.11")
		if err != nil || got != "https://192.0.2.11/command-api" {
			t.Errorf("endpointURL = %q, %v", got, err)
		}
	})
}

func TestEndpointURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"192.0.2.11", "https://192.0.2.11/command-api"},
		{"sw-a1:8443", "https://sw-a1:8443/command-api"},
		{"http://192.0.2.11", "http://192.0.2.11/command-api"},
		{"http://192.0.2.11/", "http://192.0.2.11/command-api"},
		{"  https://sw-a1.example.net  ", "https://sw-a1.example.net/command-api"},
		{"http://192.0.2.11:8080/api?x=1#f", "http://192.0.2.11:8080/api"},
		{"http://user:pw@192.0.2.11", "http://192.0.2.11/command-api"},
	}
	for _, tc := range tests {
		got, err := endpointURL(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("endpointURL(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "  ", "ftp://sw", "http://", "http://[::1"} {
		if got, err := endpointURL(bad); err == nil {
			t.Errorf("endpointURL(%q) = %q, want an error", bad, got)
		}
	}
}

// TestFakeRefusesWrites proves the guard on both sides: the client will not
// send a command that is not "show ...", and a fake playing a switch that
// received one would refuse it and record it.
func TestFakeRefusesWrites(t *testing.T) {
	f := NewFakeArista(EOS414)
	defer f.Close()
	cl := &client{http: http.DefaultClient, endpoint: f.URL() + "/command-api", user: FakeUsername, pass: FakePassword}

	for _, cmd := range []string{"configure terminal", "write memory", "reload", "enable", "showversion", ""} {
		if _, err := cl.run(context.Background(), []string{"show version", cmd}); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("client.run(%q) error = %v, want a refusal", cmd, err)
		}
	}
	if n := len(f.Requests()); n != 0 {
		t.Fatalf("the client let %d requests out with a non-show command", n)
	}

	// Bypass the client: what does the fake do with a write?
	body, _ := json.Marshal(rpcRequest{
		JSONRPC: "2.0", Method: "runCmds", ID: "x",
		Params: rpcParams{Version: 1, Cmds: []string{"show version", "configure terminal", "interface Ethernet1"}, Format: "json"},
	})
	req, _ := http.NewRequest(http.MethodPost, f.URL()+"/command-api", bytes.NewReader(body))
	req.SetBasicAuth(FakeUsername, FakePassword)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Result []json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error == nil || out.Error.Code != 1002 || len(out.Result) != 0 || !strings.Contains(out.Error.Message, "configure terminal") {
		t.Errorf("the fake did not refuse the write: %+v", out)
	}
	ops := f.NonReadOperations()
	if len(ops) != 2 || ops[0] != "command configure terminal" || ops[1] != "command interface Ethernet1" {
		t.Errorf("NonReadOperations = %v", ops)
	}

	// Other verbs and paths are non-reads too, and get no answer.
	for _, tc := range []struct{ method, path string }{{http.MethodGet, "/command-api"}, {http.MethodPost, "/other"}} {
		req, _ := http.NewRequest(tc.method, f.URL()+tc.path, http.NoBody)
		req.SetBasicAuth(FakeUsername, FakePassword)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: status %d, want 404", tc.method, tc.path, r.StatusCode)
		}
	}
	if got := len(f.NonReadOperations()); got != 4 {
		t.Errorf("NonReadOperations = %v, want the 2 commands and the 2 odd requests", f.NonReadOperations())
	}
}

func TestFakeRejectsBadRequests(t *testing.T) {
	f := NewFakeArista(EOS414)
	defer f.Close()

	post := func(auth bool, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, f.URL()+"/command-api", strings.NewReader(body))
		if auth {
			req.SetBasicAuth(FakeUsername, FakePassword)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		return r
	}
	if r := post(false, "{}"); r.StatusCode != http.StatusUnauthorized || r.Header.Get("WWW-Authenticate") == "" {
		t.Errorf("anonymous: status %d, challenge %q", r.StatusCode, r.Header.Get("WWW-Authenticate"))
	}
	if r := post(true, "not json"); r.StatusCode != http.StatusBadRequest {
		t.Errorf("bad JSON: status %d", r.StatusCode)
	}
	// A command the fixtures do not know is an eAPI error, as on a switch.
	f2 := NewFakeArista(EOS414)
	defer f2.Close()
	cl := &client{http: http.DefaultClient, endpoint: f2.URL() + "/command-api", user: FakeUsername, pass: FakePassword}
	_, err := cl.run(context.Background(), []string{"show nonexistent"})
	var ee *eapiError
	if !errors.As(err, &ee) || ee.Code != 1002 {
		t.Errorf("unknown command error = %v, want an eAPI error 1002", err)
	}
}

// TestUnconvertedFallbacks pins what a release without JSON for a command
// costs: the brief LLDP table is asked once, only on the release that needs
// it, and a command that is refused for another reason still fails the run.
func TestUnconvertedFallbacks(t *testing.T) {
	_, f := runWith(t, EOS414, nil, nil)
	n := 0
	for _, c := range f.Commands() {
		if c == cmdLLDPBrief {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%s asked %d times on 4.14, want 1", cmdLLDPBrief, n)
	}

	// The brief table refused for a reason other than its release: a failure.
	b, _ := runWith(t, EOS414, func(f *FakeArista) { f.FailCommand(cmdLLDPBrief) }, nil)
	if b.Scope != sdk.ScopePartial || b.Health.OK || !strings.Contains(b.Health.Error, cmdLLDPBrief) {
		t.Errorf("scope=%v ok=%v err=%q, want partial naming %s", b.Scope, b.Health.OK, b.Health.Error, cmdLLDPBrief)
	}
	if n := len(collecttest.Relations([]*sdk.Batch{b}, "neighbor_of")); n != 0 {
		t.Errorf("%d neighbor_of relations without LLDP data", n)
	}
}
