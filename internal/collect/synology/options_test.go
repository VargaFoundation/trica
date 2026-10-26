package synology

import (
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/collect"
)

func TestOptionsValidation(t *testing.T) {
	f := newFake(t, SetRecorded)
	for _, tc := range []struct {
		name, doc, want string
	}{
		{"unknown option", "colour: blue", "colour"},
		{"bad role", "role: toaster", "not a device role"},
		{"bad skip entry", "skip: [disks]", "options.skip"},
		{"skip is a scalar", "skip: storage", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := collect.New(withOptions(t, f.Target(nas), tc.doc))
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
		})
	}
	for _, doc := range []string{"role: storage", "skip: [Storage, ' ups ']", "role: ''"} {
		if _, err := collect.New(withOptions(t, f.Target(nas), doc)); err != nil {
			t.Errorf("%q refused: %v", doc, err)
		}
	}
}

func TestRoleOption(t *testing.T) {
	f := newFake(t, SetRecorded)
	b := runTarget(t, f, withOptions(t, f.Target(nas), "role: storage"))
	attrs(t, obj(t, b, "device", "serial", "0000ABCDE0001"), map[string]any{"role": "storage"})
	if attrs := obj(t, b, "device", "name", "nas-a1/ups").Attributes; attrs["role"] != "ups" {
		t.Errorf("the role option must not reach the UPS: %v", attrs["role"])
	}
}

func TestTargetValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*collect.Target)
		want   string
	}{
		{"no address", func(tg *collect.Target) { tg.Address = "" }, "address is required"},
		{"http", func(tg *collect.Target) { tg.Address = "http://192.0.2.10:5000" }, "must be https"},
		{"a path", func(tg *collect.Target) { tg.Address = "https://192.0.2.10:5001/webapi" }, "has a path"},
		{"garbage", func(tg *collect.Target) { tg.Address = "https://" }, "invalid address"},
		{"no username", func(tg *collect.Target) { tg.Username = " " }, "username is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg := collectTarget("https://192.0.2.10:5001")
			tc.mutate(&tg)
			_, err := collect.New(tg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
	// An address with no scheme is https.
	if _, err := collect.New(collectTarget("192.0.2.10:5001")); err != nil {
		t.Error(err)
	}
}

func TestDescriptor(t *testing.T) {
	d, ok := collect.Lookup(Kind)
	if !ok {
		t.Fatal("not registered")
	}
	if d.Evidence != collect.EvidenceRecorded || d.Docs == "" || d.ReadOnly == "" {
		t.Errorf("descriptor %+v", d)
	}
}
