package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// pngBytes is the start of a PNG: enough for a server that does not decode.
var pngBytes = []byte("\x89PNG\r\n\x1a\n-front-of-acme-sw48")

// imageMap is a DeviceTypeImages over a map: slug + "/" + face.
type imageMap map[string]api.DeviceImage

func (m imageMap) DeviceTypeImage(slug, face string) (api.DeviceImage, bool) {
	img, ok := m[slug+"/"+face]
	return img, ok
}

// testImages: the furnished switch model has a front image and no rear one.
var testImages = imageMap{"acme-sw48/front": {ContentType: "image/png", ETag: `"abc123"`, Data: pngBytes}}

func (e *env) raw(method, path, token string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

// The image is served as the bytes it is, with its type, cached privately
// (the request carries a credential) and revalidated by its ETag.
func TestDeviceTypeImageIsServedAndCached(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	w := e.raw("GET", "/api/v1/device-types/acme-sw48/front", tokReader, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), pngBytes) {
		t.Fatalf("status %d body %q, want the image", w.Code, w.Body.Bytes())
	}
	h := w.Header()
	for k, want := range map[string]string{
		"Content-Type": "image/png", "ETag": `"abc123"`, "Cache-Control": "private, max-age=3600",
		"X-Content-Type-Options": "nosniff", "Content-Length": "27",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("an image opened on its own must run nothing: CSP %q", csp)
	}
	for _, inm := range []string{`"abc123"`, `W/"abc123"`, `"old", "abc123"`, "*"} {
		w = e.raw("GET", "/api/v1/device-types/acme-sw48/front", tokReader, map[string]string{"If-None-Match": inm})
		if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
			t.Errorf("If-None-Match %s: %d with %d bytes, want 304 and no body", inm, w.Code, w.Body.Len())
		}
	}
	if w = e.raw("GET", "/api/v1/device-types/acme-sw48/front", tokReader, map[string]string{"If-None-Match": `"old"`}); w.Code != 200 {
		t.Errorf("a stale ETag got %d, want the image again", w.Code)
	}
}

func TestDeviceTypeImageRefusals(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	wantProblem(t, e.get("/api/v1/device-types/acme-sw48/rear", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/device-types/nobody/front", tokReader), 404, "not-found")
	wantProblem(t, e.get("/api/v1/device-types/acme-sw48/top", tokReader), 400, "invalid-request")
	wantProblem(t, e.get("/api/v1/device-types/acme-sw48/front", ""), 401, "unauthenticated")
	// A server built without a library serves no image and says so.
	srv, err := api.New(api.Config{Engine: e.eng, Authorizer: e.auth, Auditor: e.aud})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/device-types/acme-sw48/front", nil)
	r.Header.Set("Authorization", "Bearer "+tokReader)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("no library: %d, want 404", w.Code)
	}
}

type faceItem struct {
	Device struct {
		Display string `json:"display"`
	} `json:"device"`
	ModelName   string `json:"model_name"`
	Role        string `json:"role"`
	Health      string `json:"health"`
	PartsHealth string `json:"parts_health"`
	FrontImage  string `json:"front_image"`
	RearImage   string `json:"rear_image"`
}

// What a face is drawn from: the model's images when the library has them,
// else the role; and the health of the device and of its parts.
func TestRackContentsCarryFacesAndHealth(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	e.furnish()
	b := sdk.NewBatch("redfish:test", sdk.ScopePartial)
	b.Object("sw2", "device").Identify("name", "sw-a2").Set("health", "ok")
	b.Object("psu1", "psu").Identify("name", "PSU1").In("sw2").Set("health", "warning")
	b.Object("psu2", "psu").Identify("name", "PSU2").In("sw2").Set("health", "absent")
	b.Object("fan", "fan").Identify("name", "Fan1").In("psu1").Set("health", "critical")
	b.Object("fan2", "fan").Identify("name", "Fan2").In("sw2").Set("health", "unknown")
	e.apply(b)

	read := func(tok string) map[string]faceItem {
		t.Helper()
		out := map[string]faceItem{}
		for _, raw := range e.page("/api/v1/racks/"+e.ids["rack-p"]+"/contents", tok).Items {
			var it faceItem
			if err := json.Unmarshal(raw, &it); err != nil {
				t.Fatal(err)
			}
			out[it.Device.Display] = it
		}
		return out
	}
	items := read(tokReader)
	sw := items["sw-a2"]
	if sw.FrontImage != "/api/v1/device-types/acme-sw48/front" || sw.RearImage != "" {
		t.Fatalf("sw-a2 images %q / %q, want the front only", sw.FrontImage, sw.RearImage)
	}
	if sw.ModelName != "SW48" || sw.Role != "switch" || sw.Health != "ok" {
		t.Fatalf("sw-a2 %+v, want model SW48, role switch, health ok", sw)
	}
	// The fan of a power supply counts; an absent slot and an unknown fan do not.
	if sw.PartsHealth != "critical" {
		t.Fatalf("sw-a2 parts health %q, want critical (the fan in PSU1)", sw.PartsHealth)
	}
	if pp := items["pp-a1"]; pp.FrontImage != "" || pp.ModelName != "" || pp.PartsHealth != "" {
		t.Fatalf("pp-a1 %+v, want no model, no image, no parts", pp)
	}
	// A caller who may not read power supplies and fans does not learn their health.
	if got := read(tokRackView)["sw-a2"]; got.PartsHealth != "" || got.Health != "ok" {
		t.Fatalf("parts health %q shown to a caller who may not read the parts", got.PartsHealth)
	}
}
