package kubemetrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

func TestFailedRuns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(f *FakeKubeMetrics)
		want    string
	}{
		{"wrong password", func(f *FakeKubeMetrics) { f.SetCredentials(Account, "someone-elses-password") }, "HTTP 401"},
		{"forbidden", func(f *FakeKubeMetrics) { f.FailStatus(metricNodeInfo, http.StatusForbidden) }, "HTTP 403"},
		{"rate limited", func(f *FakeKubeMetrics) { f.FailStatus(metricPodInfo, http.StatusTooManyRequests) }, "HTTP 429"},
		{"server error", func(f *FakeKubeMetrics) { f.FailStatus("", http.StatusInternalServerError) }, "HTTP 500"},
		{"error status", func(f *FakeKubeMetrics) {
			f.SetBody(metricNodeInfo, `{"status":"error","errorType":"bad_data","error":"cannot parse"}`)
		}, "bad_data"},
		{"not a vector", func(f *FakeKubeMetrics) {
			f.SetBody(metricNodeInfo, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
		}, "want vector"},
		{"not json", func(f *FakeKubeMetrics) { f.SetBody(metricPodInfo, "<html>") }, "malformed"},
		{"no node at all", func(f *FakeKubeMetrics) {
			f.SetBody(metricNodeInfo, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
		}, "no series"},
		{"no pod at all", func(f *FakeKubeMetrics) {
			f.SetBody(metricPodInfo, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
		}, "no series"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			tc.prepare(f)
			b := collectRaw(t, target(t, f, ""))
			if b.Health.OK || b.Scope != sdk.ScopePartial || len(b.Objects) != 0 || !strings.Contains(b.Health.Error, tc.want) {
				t.Errorf("scope %v, objects %d, health %+v: want a failed, empty, partial run mentioning %q", b.Scope, len(b.Objects), b.Health, tc.want)
			}
		})
	}
}

// An empty core answer must not withdraw the whole cluster: it fails the run
// instead of reporting an empty full scope.
func TestEmptyAnswerIsNotAnEmptyCluster(t *testing.T) {
	f := newFake(t)
	b := collectRaw(t, target(t, f, `selector: job="nobody"`))
	if b.Health.OK || len(b.Objects) != 0 {
		t.Errorf("a selector that matches nothing gave %+v with %d objects", b.Health, len(b.Objects))
	}
}

// The four refining families are optional: a failure makes the run partial and
// unhealthy, drops what depended on it and never invents a workload.
func TestOptionalFamilyFailure(t *testing.T) {
	for _, tc := range []struct {
		metric   string
		pod      string // a pod whose workload depends on the family
		workload string // what it is served by when the family answers
	}{
		{metricPodOwner, "orion-6d788dccb8-xfrbl", "deployment/orion"},
		{metricRSOwner, "orion-6d788dccb8-xfrbl", "deployment/orion"},
		{metricJobOwner, "orion-boond-sync-29886630-tvbrq", "cronjob/orion-boond-sync"},
	} {
		t.Run(tc.metric, func(t *testing.T) {
			f := newFake(t)
			ok := runOne(t, f, "finished_pods: true")
			if w := workloadOfPod(ok, "pod/orion/"+tc.pod); w != tc.workload {
				t.Fatalf("with every family: workload %q, want %q", w, tc.workload)
			}
			f = newFake(t)
			f.FailStatus(tc.metric, http.StatusInternalServerError)
			b := collectRaw(t, target(t, f, "finished_pods: true"))
			if b.Scope != sdk.ScopePartial || b.Health.OK || !strings.Contains(b.Health.Error, tc.metric) {
				t.Errorf("scope %v, health %+v", b.Scope, b.Health)
			}
			if w := workloadOfPod(b, "pod/orion/"+tc.pod); w == tc.workload {
				t.Errorf("the pod is still served by %q without %s", w, tc.metric)
			}
			if count(b, "pod") != 71 || count(b, "k8s_node") != 3 {
				t.Errorf("the run lost nodes or pods: %d pods", count(b, "pod"))
			}
		})
	}
}

// Without the phase family the finished pods cannot be told apart: they are
// kept, with no phase, and the run is partial.
func TestPhaseFailureKeepsPods(t *testing.T) {
	f := newFake(t)
	f.FailStatus(metricPodPhase, http.StatusServiceUnavailable)
	b := collectRaw(t, target(t, f, ""))
	if count(b, "pod") != 71 || b.Scope != sdk.ScopePartial || b.Health.OK {
		t.Fatalf("%d pods, scope %v, health %+v", count(b, "pod"), b.Scope, b.Health)
	}
	for _, o := range b.Objects {
		if o.Type == "pod" && o.Attributes["phase"] != nil {
			t.Fatalf("pod %v has a phase", o.Identity)
		}
	}
}

// A partial answer (isPartial) is a warning and a partial scope: nothing is
// withdrawn on the strength of series the server could not fully read.
func TestPartialAnswer(t *testing.T) {
	f := newFake(t)
	f.Partial(metricPodInfo, true)
	b := runOne(t, f, "")
	if b.Scope != sdk.ScopePartial || !b.Health.OK || !hasWarning(b, "kube_pod_info: the server flagged the answer as partial") {
		t.Errorf("scope %v, health %+v", b.Scope, b.Health)
	}
	if count(b, "pod") != 47 {
		t.Errorf("%d pods: a partial answer is still mapped", count(b, "pod"))
	}
}

// A server that redirects is not followed: the Authorization header must not
// reach another origin.
func TestRedirectIsNotFollowed(t *testing.T) {
	f := newFake(t)
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.URL()+Prefix+"/"+queryPath+"?"+r.URL.RawQuery, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)
	tg := target(t, f, "")
	tg.Address = redirector.URL + Prefix
	b := collectRaw(t, tg)
	if b.Health.OK || !strings.Contains(b.Health.Error, "HTTP 302") {
		t.Errorf("health %+v", b.Health)
	}
	if n := len(f.Requests()); n != 0 {
		t.Errorf("the redirect was followed: %d requests reached the target", n)
	}
}

func TestTimeout(t *testing.T) {
	f := newFake(t)
	f.Slow(metricPodInfo, 2*time.Second)
	tg := target(t, f, "")
	tg.Timeout = 200 * time.Millisecond
	b := collectRaw(t, tg)
	if b.Health.OK || len(b.Objects) != 0 {
		t.Errorf("health %+v", b.Health)
	}
}

func TestSecretErrors(t *testing.T) {
	f := newFake(t)
	for name, secret := range map[string]string{"unreadable": "file:/nonexistent/password", "empty": "file:" + t.TempDir() + "/empty"} {
		t.Run(name, func(t *testing.T) {
			tg := target(t, f, "")
			tg.Password = collectSecret(secret)
			if name == "empty" {
				writeFile(t, strings.TrimPrefix(secret, "file:"), "\n")
			}
			b := collectRaw(t, tg)
			if b.Health.OK || !strings.Contains(b.Health.Error, "password") {
				t.Errorf("health %+v", b.Health)
			}
		})
	}
	if len(f.Requests()) != 0 {
		t.Error("a request was sent without a password")
	}
}
