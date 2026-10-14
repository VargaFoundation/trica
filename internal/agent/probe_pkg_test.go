package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestProbePackagesDebian(t *testing.T) {
	run := newFakeRunner("debian12")
	inv, err := probeAfter(t, openRoot(t, "debian12"), run, nil, "packages")
	if err != nil {
		t.Fatal(err)
	}
	want := []Package{
		{"bash", "5.2.15-2+b7", "amd64"},
		{"libc6", "2.36-9+deb12u7", "amd64"},
		{"libc6", "2.36-9+deb12u7", "i386"},
		{"openssh-server", "1:9.2p1-2+deb12u3", "amd64"},
		{"postgresql-16", "16.4-1.pgdg120+1", "amd64"},
		{"tzdata", "2024a-0+deb12u1", "all"},
	}
	if !reflect.DeepEqual(inv.Packages, want) {
		t.Fatalf("packages\n got %+v\nwant %+v", inv.Packages, want)
	}
	if len(run.calls) != 0 {
		t.Fatalf("dpkg status is a plain file, but the probe ran %v", run.calls)
	}
}

func TestProbePackagesRPM(t *testing.T) {
	run := newFakeRunner("rhel9")
	inv, err := probeAfter(t, openRoot(t, "rhel9"), run, nil, "packages")
	if err != nil {
		t.Fatal(err)
	}
	want := []Package{
		{"bash", "5.1.8-9.el9", "x86_64"},
		{"bind-libs", "32:9.16.23-18.el9", "x86_64"},
		{"glibc", "2.34-100.el9", "i686"},
		{"glibc", "2.34-100.el9", "x86_64"},
		{"kernel-core", "5.14.0-427.el9", "x86_64"},
		{"openssh-server", "8.7p1-38.el9", "x86_64"},
		{"tzdata", "2024a-1.el9", "noarch"},
	}
	if !reflect.DeepEqual(inv.Packages, want) {
		t.Fatalf("packages\n got %+v\nwant %+v", inv.Packages, want)
	}
	if !run.called("rpm -qa") {
		t.Fatal("rpm was not asked")
	}
}

func TestDpkgStatusKeepsInstalledPackagesOnly(t *testing.T) {
	const status = `Package: a
Status: install ok installed
Architecture: amd64
Version: 1

Package: removed-with-config
Status: deinstall ok config-files
Architecture: amd64
Version: 1

Package: purged
Status: purge ok not-installed
Architecture: amd64
Version: 1

Package: half
Status: install ok half-installed
Architecture: amd64
Version: 1

Package: unpacked
Status: install ok unpacked
Architecture: amd64
Version: 1

Package: held
Status: hold ok installed
Architecture: amd64
Version: 2
Description: long
 continuation line: with colon
 .
 more

Package: no-version
Status: install ok installed
Architecture: amd64

Package: broken-status
Status: installed
Architecture: amd64
Version: 1
`
	got, err := parseDpkgStatus(strings.NewReader(status))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name+"="+p.Version)
	}
	if want := []string{"a=1", "held=2"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("kept %v, want %v", names, want)
	}
}

func TestDpkgStatusLastStanzaWithoutBlankLine(t *testing.T) {
	got, err := parseDpkgStatus(strings.NewReader("Package: z\nStatus: install ok installed\nVersion: 1\nArchitecture: all"))
	if err != nil || len(got) != 1 || got[0].Arch != "all" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestParseRPM(t *testing.T) {
	got := parseRPM("b\t1-1\tx86_64\n\ngpg-pubkey\t5a6340b3-6229229e\t(none)\nmalformed line\na\t2:1-1\tnoarch\n  \nextra\t1\tx\ty\n")
	if len(got) != 2 || got[0].Name != "a" || got[0].Version != "2:1-1" || got[1].Name != "b" {
		t.Fatalf("%+v", got)
	}
}

func TestProbePackagesWithoutADatabase(t *testing.T) {
	// No dpkg status, and rpm is not installed: skipped with a note, not failed.
	r := tmpRoot(t, map[string]string{"etc/": ""})
	inv := observe(t.Context(), r, newFakeRunner("debian12"), fsAddrs{path: "/addrs.txt"})
	if !containsSub(inv.Skipped, "packages: no package database") {
		t.Fatalf("skipped %v failed %v", inv.Skipped, inv.Failed)
	}
	if containsPrefix(inv.Failed, "packages") {
		t.Fatalf("failed %v", inv.Failed)
	}
}

func TestProbePackagesRPMFailure(t *testing.T) {
	r := tmpRoot(t, map[string]string{"etc/": ""})
	boom := runnerFunc(func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("rpmdb: locked") })
	_, err := probeAfter(t, r, boom, nil, "packages")
	if err == nil || !strings.Contains(err.Error(), "rpm -qa") {
		t.Fatalf("err %v", err)
	}
	// rpm exits non-zero on a warning yet prints the list: keep it.
	warn := runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return []byte("bash\t5-1\tx86_64\n"), errors.New("exit status 1")
	})
	inv, err := probeAfter(t, r, warn, nil, "packages")
	if err != nil || len(inv.Packages) != 1 {
		t.Fatalf("%+v %v", inv.Packages, err)
	}
}

func TestUniquePackageKeys(t *testing.T) {
	got := uniquePackageKeys([]Package{
		{"libc6", "2.36", "amd64"}, {"libc6", "2.36", "i386"}, {"libc6", "2.36", "i386"}, {"bash", "5", "amd64"},
	})
	var keys []string
	for _, k := range got {
		keys = append(keys, k.key)
	}
	want := []string{"package/libc6/2.36", "package/libc6/2.36:i386", "package/bash/5"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys %v, want %v", keys, want)
	}
}
