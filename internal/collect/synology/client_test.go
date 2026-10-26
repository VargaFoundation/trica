package synology

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/collecttest"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// failedRun runs the collector expecting a failed run: one batch, not healthy,
// partial, empty. It also checks that no session is left open.
func failedRun(t *testing.T, f *FakeDSM, tg collect.Target) *sdk.Batch {
	t.Helper()
	bs := collecttest.Run(t, newCollector(t, tg))
	if len(bs) != 1 {
		t.Fatalf("%d batches", len(bs))
	}
	b := bs[0]
	if b.Health.OK || b.Scope != sdk.ScopePartial || len(b.Objects) != 0 {
		t.Fatalf("scope %v, health %+v, %d objects: want a failed, partial, empty run", b.Scope, b.Health, len(b.Objects))
	}
	if f != nil {
		if err := f.OnlyAllowed(); err != nil {
			t.Error(err)
		}
		if n := f.OpenSessions(); n != 0 {
			t.Errorf("%d sessions left open", n)
		}
	}
	return b
}

func TestLoginFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(f *FakeDSM)
		want    []string
	}{
		{"wrong password", func(f *FakeDSM) { f.SetCredentials(Account, "another-password") }, []string{"DSM error 400", "wrong account or password"}},
		{"2-step verification required", func(f *FakeDSM) { f.RejectLogin(403) }, []string{"DSM error 403", "2-step verification"}},
		{"2-step verification failed", func(f *FakeDSM) { f.RejectLogin(404) }, []string{"DSM error 404", "must not require 2-step verification"}},
		{"account disabled", func(f *FakeDSM) { f.RejectLogin(401) }, []string{"account disabled"}},
		{"address blocked", func(f *FakeDSM) { f.RejectLogin(407) }, []string{"IP address blocked"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, SetRecorded)
			tc.prepare(f)
			b := failedRun(t, f, f.Target(nas))
			for _, w := range tc.want {
				if !strings.Contains(b.Health.Error, w) {
					t.Errorf("error %q lacks %q", b.Health.Error, w)
				}
			}
			if got := f.Calls(); len(got) != 1 {
				t.Errorf("calls after a refused login: %v", got) // nothing to read, nothing to log out of
			}
		})
	}
}

func TestErrorCodeText(t *testing.T) {
	for code, want := range map[int]string{
		100: "unknown error", 101: "no parameter", 102: "does not exist", 103: "method does not exist", 104: "version is not supported",
		105: "permission denied", 106: "session timeout", 107: "duplicate login", 119: "session id not found",
		400: "wrong account", 401: "disabled", 402: "permission denied", 403: "2-step", 404: "2-step", 407: "blocked", 999: "unrecognised",
	} {
		if got := (&apiError{Call: epSystem, Code: code}).Error(); !strings.Contains(got, want) || !strings.Contains(got, "DSM error") {
			t.Errorf("code %d: %q lacks %q", code, got, want)
		}
	}
	for _, code := range []int{105, 402} {
		if !strings.Contains((&apiError{Call: epSystem, Code: code}).Error(), "administrators group") {
			t.Errorf("code %d does not point at the administrators group", code)
		}
	}
	for _, code := range []int{403, 404} {
		if !strings.Contains((&apiError{Call: epSystem, Code: code}).Error(), "must not require 2-step verification") {
			t.Errorf("code %d does not say the account must not require 2-step verification", code)
		}
	}
}

// A failing core call fails the run, and the session is still closed.
func TestCoreFailureLogsOut(t *testing.T) {
	for _, tc := range []struct {
		name, api string
		code      int
	}{
		{"system information", "SYNO.Core.System", 105},
		{"DSM information", "SYNO.DSM.Info", 102},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, SetRecorded)
			f.Fail(tc.api, "", tc.code)
			b := failedRun(t, f, f.Target(nas))
			if !strings.Contains(b.Health.Error, tc.api) {
				t.Errorf("error %q does not name %s", b.Health.Error, tc.api)
			}
			calls := f.Calls()
			if calls[len(calls)-1] != "SYNO.API.Auth logout" {
				t.Errorf("the last call is %q, want the logout", calls[len(calls)-1])
			}
			if reqs := f.Requests(); !reqs[len(reqs)-1].SIDValid {
				t.Error("the logout carried no valid session id")
			}
		})
	}
}

// A run that runs out of time still logs out, with its own timeout.
func TestTimeoutLogsOut(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.Slow("SYNO.DSM.Info", "getinfo", 5*time.Second)
	tg := f.Target(nas)
	tg.Timeout = 300 * time.Millisecond
	b := failedRun(t, f, tg)
	if !strings.Contains(b.Health.Error, "transport error") && !strings.Contains(b.Health.Error, "deadline") {
		t.Errorf("error %q", b.Health.Error)
	}
	calls := f.Calls()
	if calls[len(calls)-1] != "SYNO.API.Auth logout" {
		t.Errorf("calls %v: no logout after the timeout", calls)
	}
}

// An optional API that DSM does not have, or does not let the account call,
// is a warning: the rest of the NAS is reported.
func TestOptionalFailures(t *testing.T) {
	for _, tc := range []struct {
		name, api string
		code      int
		part      string
		scope     sdk.Scope
		healthy   bool
		keepsTyp  string // an object type that must be there anyway
	}{
		{"no Container Manager", "SYNO.Docker.Container", 102, "containers", sdk.ScopeFull, true, "device"},
		{"no UPS API", "SYNO.Core.ExternalDevice.UPS", 103, "ups", sdk.ScopeFull, true, "disk"},
		{"packages refused", "SYNO.Core.Package", 105, "packages", sdk.ScopePartial, true, "disk"},
		{"storage version", "SYNO.Storage.CGI.Storage", 104, "storage", sdk.ScopePartial, true, "interface"},
		{"storage broken", "SYNO.Storage.CGI.Storage", 100, "storage", sdk.ScopePartial, false, "interface"},
		{"network session lost", "SYNO.Core.Network", 106, "network", sdk.ScopePartial, false, "disk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, SetRecorded)
			f.Fail(tc.api, "", tc.code)
			b := runOne(t, f)
			if b.Scope != tc.scope || b.Health.OK != tc.healthy {
				t.Fatalf("scope %v, health %+v", b.Scope, b.Health)
			}
			if tc.healthy {
				if !hasWarning(b, tc.part) {
					t.Errorf("no %s warning: %v", tc.part, b.Health.Warnings)
				}
			} else if !strings.Contains(b.Health.Error, "partial collection") || !strings.Contains(b.Health.Error, tc.part) {
				t.Errorf("error %q", b.Health.Error)
			}
			if count(b, tc.keepsTyp) == 0 {
				t.Errorf("no %s: the rest of the run must still be reported", tc.keepsTyp)
			}
			if count(b, "device") == 0 {
				t.Error("no device")
			}
		})
	}
}

// Without the storage there is nothing to say the NAS is healthy: unknown.
func TestHealthUnknownWithoutStorage(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.Fail("SYNO.Storage.CGI.Storage", "", 105)
	b := runOne(t, f)
	attrs(t, obj(t, b, "device", "serial", "0000ABCDE0001"), map[string]any{"health": "unknown"})
	if count(b, "disk") != 0 {
		t.Error("disks without the storage")
	}
}

func TestSkip(t *testing.T) {
	f := newFake(t, SetRecorded)
	b := runTarget(t, f, withOptions(t, f.Target(nas), "skip: [packages, containers, ups]"))
	if b.Scope != sdk.ScopePartial {
		t.Error("a run that skips a part must be partial: nothing is withdrawn")
	}
	if n := count(b, "software"); n != 1 {
		t.Errorf("%d software, want DSM only", n)
	}
	if count(b, "device") != 1 {
		t.Error("the UPS is skipped")
	}
	for _, c := range f.Calls() {
		for _, banned := range []string{"Package", "UPS", "Docker"} {
			if strings.Contains(c, banned) {
				t.Errorf("skipped part still called: %s", c)
			}
		}
	}
}

func TestSkipStorageAndNetwork(t *testing.T) {
	f := newFake(t, SetRecorded)
	b := runTarget(t, f, withOptions(t, f.Target(nas), "skip: [storage, network]"))
	if count(b, "disk") != 0 || count(b, "interface") != 0 {
		t.Error("skipped parts reported")
	}
	if got := f.Calls(); contains(got, "SYNO.Storage.CGI.Storage load_info") || contains(got, "SYNO.Core.Network get") {
		t.Errorf("calls %v", got)
	}
}

func TestMalformedAnswers(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":    `<html>nginx</html>`,
		"wrong shape": `{"success":true,"data":"text"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, SetRecorded)
			f.SetBody("SYNO.Core.System", "info", body)
			b := failedRun(t, f, f.Target(nas))
			if !strings.Contains(b.Health.Error, "SYNO.Core.System") {
				t.Errorf("error %q", b.Health.Error)
			}
		})
	}
}

func TestPasswordProblems(t *testing.T) {
	f := newFake(t, SetRecorded)
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, ref := range map[string]string{"empty": "file:" + empty, "missing": "file:" + empty + ".none"} {
		tg := f.Target(nas)
		tg.Password = collect.Secret(ref)
		b := failedRun(t, nil, tg)
		if !strings.Contains(b.Health.Error, "password") {
			t.Errorf("%s: error %q", name, b.Health.Error)
		}
	}
	if len(f.Requests()) != 0 {
		t.Error("a request was sent without a password")
	}
}

// DSM's login carries the password: it must never follow a redirect.
func TestRedirectIsRefused(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer other.Close()
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/webapi/entry.cgi", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	f := newFake(t, SetRecorded)
	tg := f.Target(nas)
	tg.Address = redirector.URL
	b := failedRun(t, nil, tg)
	if hits.Load() != 0 {
		t.Error("the redirect was followed: the password went to another origin")
	}
	if b.Health.Error == "" {
		t.Error("no error")
	}
}

func TestNotADSM(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusNotFound) }))
	defer srv.Close()
	f := newFake(t, SetRecorded)
	tg := f.Target(nas)
	tg.Address = srv.URL
	b := failedRun(t, nil, tg)
	if !strings.Contains(b.Health.Error, "HTTP 404") {
		t.Errorf("error %q", b.Health.Error)
	}
}

// A session that DSM no longer knows (119) is a failure, not an empty answer.
func TestSessionIDNotFound(t *testing.T) {
	f := newFake(t, SetRecorded)
	f.Fail("SYNO.Core.System", "", 119)
	b := failedRun(t, f, f.Target(nas))
	if !strings.Contains(b.Health.Error, "119") {
		t.Errorf("error %q", b.Health.Error)
	}
}
