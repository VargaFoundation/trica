package synology

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/runner"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The password and the session id never reach a log line, a health record, a
// warning or a batch, whatever goes wrong: the run goes through the real
// runner, which logs.
func TestCredentialsNeverLeak(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(f *FakeDSM, tg *collect.Target)
	}{
		{"ok", func(*FakeDSM, *collect.Target) {}},
		{"wrong password", func(f *FakeDSM, _ *collect.Target) { f.SetCredentials(Account, "someone-elses-password") }},
		{"2-step verification", func(f *FakeDSM, _ *collect.Target) { f.RejectLogin(403) }},
		{"core failure", func(f *FakeDSM, _ *collect.Target) { f.Fail("SYNO.Core.System", "", 105) }},
		{"session lost", func(f *FakeDSM, _ *collect.Target) { f.Fail("SYNO.DSM.Info", "", 119) }},
		{"optional failure", func(f *FakeDSM, _ *collect.Target) { f.Fail("SYNO.Core.Package", "", 100) }},
		{"malformed", func(f *FakeDSM, _ *collect.Target) { f.SetBody("SYNO.Core.System", "info", "<html>") }},
		{"closed server", func(f *FakeDSM, _ *collect.Target) { f.server.Close() }},
		{"timeout", func(f *FakeDSM, tg *collect.Target) {
			f.Slow("SYNO.DSM.Info", "getinfo", 2*time.Second)
			tg.Timeout = 200 * time.Millisecond
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, SetRecorded)
			tg := f.Target(nas)
			tc.prepare(f, &tg)
			var logs bytes.Buffer
			var mu sync.Mutex
			var batches []*sdk.Batch
			r, err := runner.New(runner.Config{
				Targets: []collect.Target{tg},
				Emitter: sdk.EmitterFunc(func(_ context.Context, b *sdk.Batch) error {
					mu.Lock()
					defer mu.Unlock()
					batches = append(batches, b)
					return nil
				}),
				Log:    slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
				Jitter: -1,
			})
			if err != nil {
				t.Fatal(err)
			}
			r.RunOnce(context.Background())
			doc, err := json.Marshal(batches)
			if err != nil {
				t.Fatal(err)
			}
			if len(batches) == 0 || !strings.Contains(logs.String(), `"source":"synology:nas"`) || !strings.Contains(logs.String(), `"collector":"synology"`) {
				t.Fatalf("the run left no trace (%d batches, logs %q): the check would be vacuous", len(batches), logs.String())
			}
			secrets := append(f.IssuedSIDs(), Password, "someone-elses-password")
			if tc.name != "closed server" && tc.name != "wrong password" && tc.name != "2-step verification" && len(f.IssuedSIDs()) == 0 {
				t.Fatal("no session was opened: the check would be vacuous")
			}
			for what, text := range map[string]string{"log": logs.String(), "batches": string(doc)} {
				for _, s := range secrets {
					if strings.Contains(text, s) {
						t.Errorf("%q is in the %s: %s", s, what, text)
					}
				}
			}
			if err := f.OnlyAllowed(); err != nil {
				t.Error(err)
			}
		})
	}
}

// The password and the session id travel in the request body: the fake never
// sees one in a URL.
func TestCredentialsNotInURL(t *testing.T) {
	f := newFake(t, SetRecorded)
	runOne(t, f)
	if len(f.IssuedSIDs()) == 0 {
		t.Fatal("no session")
	}
	for _, r := range f.Requests() {
		if r.Query != "" || strings.Contains(r.Path, "FAKESID") || strings.Contains(r.Path, Password) {
			t.Errorf("request %s %s has credentials or parameters in its URL", r.API, r.Call)
		}
		for k, v := range r.Params {
			if strings.Contains(strings.ToLower(k), "pass") || strings.Contains(v, Password) {
				t.Errorf("the fake kept a credential parameter %s", k)
			}
		}
	}
}

// What the NAS answers that trica has no use for must not be decoded: the
// SNMP community of the UPS, the NTP and DNS servers, share and account names.
func TestDecodeStructsDeclareNothingSensitive(t *testing.T) {
	forbidden := []string{"snmp", "community", "ntp", "dns", "passw", "secret", "token", "key", "acl", "workgroup", "wins", "gateway"}
	src := declaredJSONNames(t)
	if len(src) < 40 {
		t.Fatalf("only %d JSON names found: the scan is broken", len(src))
	}
	for _, n := range src {
		for _, f := range forbidden {
			if strings.Contains(strings.ToLower(n), f) && n != "gateway_info" {
				t.Errorf("the decode structs declare %q", n)
			}
		}
	}
}
