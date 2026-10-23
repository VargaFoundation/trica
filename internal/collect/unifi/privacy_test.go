package unifi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/collect/runner"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// What the recorded answers carry that must never reach the inventory: the
// account behind the key (e-mail, name, permissions, ids) and where the
// console is. They are kept in the anonymised fixtures as sentinels, so that
// these tests mean something.
var personal = []string{
	"operator@example.test", "Example Operator", "Example City", "network.management",
	"00000000-0000-4000-8000-0000000000aa", "00000000-0000-4000-8000-0000000000ab",
	"Europe/Paris", "0000000000000000.id.ui.direct", "AAAAAA",
	"000000000000000000000000000000000000000000000000000000000001:1234567890", // the host id
}

func TestPersonalDataNeverEmitted(t *testing.T) {
	// The sentinels are really in the answers the collector reads.
	raw := ""
	for _, name := range []string{"hosts", "sites", "devices", "isp-metrics/5m"} {
		b, err := os.ReadFile("testdata/" + SetRecorded + "/v1/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		raw += string(b)
	}
	for _, s := range personal {
		if !strings.Contains(raw, s) {
			t.Fatalf("sentinel %q is not in the recorded fixtures: the test would prove nothing", s)
		}
	}
	for _, set := range []string{SetRecorded, SetSynthesised} {
		f := newFake(t, set)
		f.ReplayMetricsUntil(time.Now()) // the metrics go through the mapping too
		b := runOne(t, f)
		doc, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range personal {
			if strings.Contains(string(doc), s) {
				t.Errorf("%s: %q reached the batch", set, s)
			}
		}
		for _, o := range b.Objects {
			if o.Type == "person" || o.Type == "team" || o.Type == "site" {
				t.Errorf("%s: a %s object was made", set, o.Type)
			}
		}
		if strings.Contains(string(doc), "@") {
			t.Errorf("%s: an '@' in the batch (an e-mail?)", set)
		}
	}
}

// personalFields lists the fields of the decode structs rooted at roots whose
// JSON name is personal data or a location.
func personalFields(roots ...any) (found []string, walked int) {
	forbidden := map[string]bool{
		"userdata": true, "email": true, "fullname": true, "permissions": true, "location": true, "timezone": true,
		"anonid": true, "controller_uuid": true, "directconnectdomain": true, "uuid": true, "cpu.id": true,
		"qrid": true, "uidb": true, "localid": true, "roleid": true, "consolegroupmembers": true, "adoptiontime": true,
		"lat": true, "long": true, "radius": true, "text": true, "hardwareid": true, "owner": true,
	}
	seen := map[reflect.Type]bool{}
	var walk func(ty reflect.Type, path string)
	walk = func(ty reflect.Type, path string) {
		for ty.Kind() == reflect.Pointer || ty.Kind() == reflect.Slice || ty.Kind() == reflect.Map {
			ty = ty.Elem()
		}
		if ty.Kind() != reflect.Struct || seen[ty] {
			return
		}
		seen[ty] = true
		for i := range ty.NumField() {
			f := ty.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if forbidden[strings.ToLower(name)] {
				found = append(found, path+"."+f.Name+" ("+name+")")
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	for _, v := range roots {
		walk(reflect.TypeOf(v), reflect.TypeOf(v).Name())
	}
	return found, len(seen)
}

// The decode structs are an allow-list: nothing personal is declared, so
// nothing personal can be read by a later change that maps "one more field".
func TestDecodeStructsHaveNoPersonalFields(t *testing.T) {
	found, walked := personalFields(host{}, site{}, deviceGroup{}, metricSite{}, envelope{})
	for _, f := range found {
		t.Errorf("%s is decoded: personal data or location must not be read", f)
	}
	if walked < 10 {
		t.Fatalf("walked only %d struct types: the scan is broken", walked)
	}
	// The scan reports what it is meant to report.
	type account struct {
		UserData struct {
			Email string `json:"email"`
		} `json:"userData"`
	}
	if found, _ := personalFields(account{}); len(found) != 2 {
		t.Errorf("control: %v", found)
	}
}

// The key never reaches a log line, a health record, a warning or a batch,
// whatever goes wrong: it is run through the real runner, which logs.
func TestKeyNeverLeaks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(f *FakeSiteManager, tg *collect.Target)
	}{
		{"ok", func(*FakeSiteManager, *collect.Target) {}},
		{"401", func(f *FakeSiteManager, _ *collect.Target) { f.SetKey("someone-elses-key") }},
		{"403 hosts", func(f *FakeSiteManager, _ *collect.Target) { f.Fail("/v1/hosts", 403) }},
		{"429 sites", func(f *FakeSiteManager, _ *collect.Target) { f.Fail("/v1/sites", 429) }},
		{"500 devices", func(f *FakeSiteManager, _ *collect.Target) { f.Fail("/v1/devices", 500) }},
		{"malformed", func(f *FakeSiteManager, _ *collect.Target) { f.Malformed("") }},
		{"closed server", func(f *FakeSiteManager, _ *collect.Target) { f.server.Close() }},
		{"timeout", func(f *FakeSiteManager, tg *collect.Target) {
			f.Slow("", 2*time.Second)
			tg.Timeout = 200 * time.Millisecond
		}},
		{"redirect", func(f *FakeSiteManager, _ *collect.Target) { f.Redirect("/v1/hosts", "https://127.0.0.1:1/v1/hosts") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, SetRecorded)
			tg := f.Target(office)
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
			if len(batches) == 0 || !strings.Contains(logs.String(), `"source":"unifi:office"`) {
				t.Fatalf("the run left no trace (%d batches, logs %q): the check would be vacuous", len(batches), logs.String())
			}
			for what, text := range map[string]string{"log": logs.String(), "batches": string(doc)} {
				if strings.Contains(text, APIKey) {
					t.Errorf("the API key is in the %s: %s", what, text)
				}
			}
			if err := f.OnlyReads(); err != nil {
				t.Error(err)
			}
		})
	}
}
