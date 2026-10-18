package wiring_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/devicetypes"
	"github.com/VargaFoundation/trica/internal/wiring"
)

// A started server serves the embedded images, and the operator's
// directory (devicetypes.extra_dir) over them without a rebuild: a
// photograph of a model the library has no image of, and one that replaces
// an embedded drawing.
func TestServerServesDeviceTypeImages(t *testing.T) {
	dir := t.TempDir()
	photo := []byte("\xff\xd8\xff\xe0an operator photograph")
	for _, p := range []string{"HPE/hpe-proliant-dl380-gen9.front.jpg", "HPE/hpe-proliant-dl360-gen9.front.jpg"} {
		full := filepath.Join(dir, devicetypes.ImageDir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, photo, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := newRig(t, "devicetypes: {extra_dir: "+dir+"}")
	for path, want := range map[string]string{
		"/api/v1/device-types/hpe-proliant-dl380-gen10/front": "\x89PNG", // embedded
		"/api/v1/device-types/hpe-proliant-dl380-gen9/front":  string(photo),
		"/api/v1/device-types/hpe-proliant-dl360-gen9/front":  string(photo),
		"/api/v1/device-types/hpe-proliant-dl360-gen9/rear":   "\xff\xd8\xff", // embedded, not replaced
	} {
		code, body := r.do("GET", path, r.readToken, "")
		if code != 200 || !strings.HasPrefix(body, want) {
			t.Errorf("%s: %d, %.12q, want %.12q", path, code, body, want)
		}
	}
	if code, _ := r.do("GET", "/api/v1/device-types/hpe-proliant-dl380-gen9/rear", r.readToken, ""); code != 404 {
		t.Errorf("a face nobody has an image of: %d, want 404", code)
	}
	if code, _ := r.do("GET", "/api/v1/device-types/hpe-proliant-dl380-gen10/front", "", ""); code != 401 {
		t.Errorf("without a credential: %d, want 401", code)
	}
}

func TestImagesAdapter(t *testing.T) {
	if _, ok := (wiring.Images{}).DeviceTypeImage("hpe-proliant-dl360-gen9", "front"); ok {
		t.Fatal("no library, no image")
	}
	models, err := devicetypes.Default()
	if err != nil {
		t.Fatal(err)
	}
	im := wiring.Images{Index: devicetypes.NewIndex(models)}
	if img, ok := im.DeviceTypeImage("hpe-proliant-dl360-gen9", "rear"); !ok || img.ContentType != "image/jpeg" || img.ETag == "" {
		t.Fatalf("%+v %v", img, ok)
	}
	for _, c := range [][2]string{{"hpe-proliant-dl380-gen9", "front"}, {"nobody", "front"}, {"hpe-proliant-dl360-gen9", "top"}} {
		if _, ok := im.DeviceTypeImage(c[0], c[1]); ok {
			t.Errorf("%v: an image", c)
		}
	}
}
