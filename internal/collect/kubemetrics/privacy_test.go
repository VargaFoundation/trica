package kubemetrics

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/runner"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The password never reaches a log line, a health record, a warning or a
// batch, whatever goes wrong: the run goes through the real runner, which logs.
func TestCredentialsNeverLeak(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(f *FakeKubeMetrics, tg *collect.Target)
	}{
		{"ok", func(*FakeKubeMetrics, *collect.Target) {}},
		{"wrong password", func(f *FakeKubeMetrics, _ *collect.Target) { f.SetCredentials(Account, "someone-elses-password") }},
		{"forbidden", func(f *FakeKubeMetrics, _ *collect.Target) { f.FailStatus("", http.StatusForbidden) }},
		{"optional failure", func(f *FakeKubeMetrics, _ *collect.Target) { f.FailStatus(metricPodOwner, 500) }},
		{"malformed", func(f *FakeKubeMetrics, _ *collect.Target) { f.SetBody(metricNodeInfo, "<html>") }},
		{"closed server", func(f *FakeKubeMetrics, _ *collect.Target) { f.server.Close() }},
		{"timeout", func(f *FakeKubeMetrics, tg *collect.Target) {
			f.Slow(metricPodInfo, 2*time.Second)
			tg.Timeout = 200 * time.Millisecond
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			tg := target(t, f, "")
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
			if len(batches) == 0 || !strings.Contains(logs.String(), `"source":"kubemetrics:prod"`) || !strings.Contains(logs.String(), `"collector":"kubemetrics"`) {
				t.Fatalf("the run left no trace (%d batches, logs %q): the check would be vacuous", len(batches), logs.String())
			}
			for what, text := range map[string]string{"log": logs.String(), "batches": string(doc)} {
				for _, s := range []string{Password, "someone-elses-password"} {
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

// The password travels in the Authorization header: never in the URL, and the
// fake never keeps it.
func TestCredentialsNotInURL(t *testing.T) {
	f := newFake(t)
	runOne(t, f, "")
	for _, r := range f.Requests() {
		if !r.HasAuth || !r.AuthValid {
			t.Errorf("request for %s carries no valid basic authentication", r.Metric)
		}
		for k, v := range r.Params {
			if strings.Contains(strings.Join(v, ""), Password) || strings.Contains(k, "pass") {
				t.Errorf("a credential is in the URL: %s", k)
			}
		}
		if strings.Contains(r.Path, Password) {
			t.Error("the password is in the path")
		}
	}
}

// What kube-state-metrics could expose that trica has no use for must not be
// mapped: the objects carry placement attributes only.
func TestOnlyPlacementLabelsAreRead(t *testing.T) {
	b := runOne(t, newFake(t), "finished_pods: true")
	for _, o := range b.Objects {
		for k := range o.Attributes {
			switch k {
			case "phase", "pod_ip", "node_name", "system_uuid", "kubelet_version", "os_image", "kernel", "internal_ip", "kind":
			default:
				t.Errorf("%s %v carries the attribute %q", o.Type, o.Identity, k)
			}
		}
	}
}
