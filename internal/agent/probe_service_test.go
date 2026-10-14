package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestProbeServicesDebian(t *testing.T) {
	inv, err := probeAfter(t, openRoot(t, "debian12"), newFakeRunner("debian12"), nil, "services")
	if err != nil {
		t.Fatal(err)
	}
	want := []Service{
		{"chrony.service", "active", true},
		{"cron.service", "active", false},
		{"example-broken.service", "failed", false},
		{"postgresql.service", "active", true},
		{"ssh.service", "active", true},
	}
	// ghost.service (load=not-found), ssh.socket and apt-daily.timer are not services of this host.
	if !reflect.DeepEqual(inv.Services, want) {
		t.Fatalf("services\n got %+v\nwant %+v", inv.Services, want)
	}
}

func TestProbeServicesRHEL(t *testing.T) {
	inv, err := probeAfter(t, openRoot(t, "rhel9"), newFakeRunner("rhel9"), nil, "services")
	if err != nil {
		t.Fatal(err)
	}
	want := []Service{
		{"NetworkManager.service", "active", false},
		{"crond.service", "active", true},
		// enabled but not loaded: no state known, and it is still reported.
		{"firewalld.service", "", true},
		{"rsyslog.service", "active", true},
		{"sshd.service", "active", true},
	}
	if !reflect.DeepEqual(inv.Services, want) {
		t.Fatalf("services\n got %+v\nwant %+v", inv.Services, want)
	}
}

func TestServicesSkippedWithoutSystemd(t *testing.T) {
	r := tmpRoot(t, map[string]string{"etc/systemd/system/multi-user.target.wants/x.service": ""})
	run := newFakeRunner("debian12")
	inv := observe(t.Context(), r, run, fsAddrs{path: "/addrs.txt"})
	if !containsSub(inv.Skipped, "services: systemd is not running") || len(inv.Services) != 0 {
		t.Fatalf("skipped %v services %+v", inv.Skipped, inv.Services)
	}
	if run.called("systemctl") {
		t.Fatal("systemctl was asked on a host without systemd")
	}
}

func TestServicesWithoutSystemctl(t *testing.T) {
	// systemd runs but systemctl cannot be run (fake root, minimal container): the enabled units are still known.
	r := openRoot(t, "debian12")
	inv := observe(t.Context(), r, noRunner{}, fsAddrs{path: "/addrs.txt"})
	if !containsSub(inv.Skipped, "systemctl not available") {
		t.Fatalf("skipped %v", inv.Skipped)
	}
	if containsPrefix(inv.Failed, "services") {
		t.Fatalf("failed %v", inv.Failed)
	}
	if len(inv.Services) != 3 {
		t.Fatalf("services %+v", inv.Services)
	}
	for _, s := range inv.Services {
		if s.State != "" || !s.Enabled {
			t.Fatalf("%+v: a state without systemctl is invented", s)
		}
	}
}

func TestServicesSystemctlFailures(t *testing.T) {
	r := openRoot(t, "debian12")
	t.Run("error without output", func(t *testing.T) {
		run := runnerFunc(func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("exit status 1") })
		inv, err := probeAfter(t, r, run, nil, "services")
		if err == nil {
			t.Fatal("expected a failure")
		}
		if len(inv.Services) != 3 {
			t.Fatalf("the enabled units must still be reported: %+v", inv.Services)
		}
	})
	t.Run("not json", func(t *testing.T) {
		run := runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
			return []byte("Failed to connect to bus"), nil
		})
		if _, err := probeAfter(t, r, run, nil, "services"); err == nil {
			t.Fatal("expected a failure")
		}
	})
	t.Run("non-zero exit with output is used", func(t *testing.T) {
		run := runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
			return []byte(`[{"unit":"a.service","load":"loaded","active":"active"}]`), errors.New("exit status 1")
		})
		inv, err := probeAfter(t, r, run, nil, "services")
		if err != nil || len(inv.Services) != 4 {
			t.Fatalf("%+v %v", inv.Services, err)
		}
	})
}

func TestServicesIgnoreNonServiceUnits(t *testing.T) {
	r := tmpRoot(t, map[string]string{"run/systemd/system/": ""})
	run := runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`[{"unit":"x.socket","load":"loaded","active":"active"},{"unit":"y.service","load":"loaded","active":"inactive"}]`), nil
	})
	inv, err := probeAfter(t, r, run, nil, "services")
	if err != nil || len(inv.Services) != 1 || inv.Services[0].Name != "y.service" || inv.Services[0].State != "inactive" {
		t.Fatalf("%+v %v", inv.Services, err)
	}
}
