// Package packaging holds the deb/rpm and systemd files of trica-agent and the
// tests that pin their security-relevant fields. The files are configuration:
// a loosened sandbox must turn a test red, not pass review unnoticed.
package packaging

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// capabilities the probes need (ADR-0022, and the unit's header comment).
var wantCaps = []string{"CAP_DAC_READ_SEARCH", "CAP_SYS_PTRACE", "CAP_SYS_RAWIO"}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// unit returns the key=value lines of the unit, repeated keys joined by spaces.
func unit(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, line := range strings.Split(read(t, "trica-agent.service"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("unit line %q", line)
		}
		out[k] = strings.TrimSpace(out[k] + " " + v)
	}
	return out
}

func TestUnitRunsUnprivilegedWithOnlyTheCapabilitiesTheProbesNeed(t *testing.T) {
	u := unit(t)
	if u["User"] == "" || u["User"] == "root" || u["Group"] == "" || u["Group"] == "root" {
		t.Errorf("User=%q Group=%q: the agent must not run as root", u["User"], u["Group"])
	}
	for _, key := range []string{"CapabilityBoundingSet", "AmbientCapabilities"} {
		got := strings.Fields(u[key])
		slices.Sort(got)
		if !slices.Equal(got, wantCaps) {
			t.Errorf("%s = %v, want exactly %v", key, got, wantCaps)
		}
	}
	// Every capability is justified in the unit's header, not just granted.
	header := read(t, "trica-agent.service")
	header, _, _ = strings.Cut(header, "[Unit]")
	for _, c := range wantCaps {
		if !strings.Contains(header, c) {
			t.Errorf("%s is granted but not justified in the unit's header", c)
		}
	}
}

func TestUnitSandbox(t *testing.T) {
	u := unit(t)
	for k, want := range map[string]string{
		"NoNewPrivileges":         "yes",
		"ProtectSystem":           "strict",
		"ProtectHome":             "yes",
		"PrivateTmp":              "yes",
		"ProtectKernelTunables":   "yes",
		"ProtectKernelModules":    "yes",
		"ProtectControlGroups":    "yes",
		"RestrictNamespaces":      "yes",
		"RestrictSUIDSGID":        "yes",
		"LockPersonality":         "yes",
		"MemoryDenyWriteExecute":  "yes",
		"RestrictAddressFamilies": "AF_UNIX AF_NETLINK AF_INET AF_INET6",
		"Restart":                 "always", // a rolled-back update needs the restart
		"StateDirectory":          "trica-agent",
	} {
		if u[k] != want {
			t.Errorf("%s = %q, want %q", k, u[k], want)
		}
	}
	// The probes need the devices (smartctl) and other users' processes.
	for _, k := range []string{"PrivateDevices", "PrivateUsers", "ProtectProc", "InaccessiblePaths"} {
		if v, ok := u[k]; ok {
			t.Errorf("%s=%s would hide something a probe reads", k, v)
		}
	}
	if !strings.Contains(u["ExecStart"], "--state-dir=/var/lib/trica-agent") {
		t.Errorf("ExecStart %q does not enable staged versions", u["ExecStart"])
	}
}

func TestNfpmShipsTheUnitTheUserAndTheScripts(t *testing.T) {
	var cfg struct {
		Contents []struct {
			Src      string `yaml:"src"`
			Dst      string `yaml:"dst"`
			FileInfo struct {
				Mode any `yaml:"mode"`
			} `yaml:"file_info"`
		} `yaml:"contents"`
		Scripts map[string]string `yaml:"scripts"`
	}
	if err := yaml.Unmarshal([]byte(read(t, "nfpm.yaml")), &cfg); err != nil {
		t.Fatal(err)
	}
	dsts := map[string]string{}
	for _, c := range cfg.Contents {
		dsts[c.Dst] = c.Src
		if !strings.HasPrefix(c.Src, "${") {
			// Paths are relative to the repository root, where nfpm runs.
			if _, err := os.Stat(filepath.Join("..", c.Src)); err != nil {
				t.Errorf("nfpm ships %s, which does not exist: %v", c.Src, err)
			}
		}
	}
	for dst, src := range map[string]string{
		"/usr/bin/trica-agent":                        "${BINARY}",
		"/usr/lib/systemd/system/trica-agent.service": "packaging/trica-agent.service",
		"/usr/lib/sysusers.d/trica-agent.conf":        "packaging/trica-agent.sysusers",
	} {
		if dsts[dst] != src {
			t.Errorf("%s comes from %q, want %q", dst, dsts[dst], src)
		}
	}
	for name, path := range cfg.Scripts {
		if _, err := os.Stat(filepath.Join("..", path)); err != nil {
			t.Errorf("script %s: %v", name, err)
		}
	}
	if !strings.Contains(read(t, "trica-agent.sysusers"), "trica-agent") || !strings.Contains(read(t, "trica-agent.sysusers"), "nologin") {
		t.Error("the sysusers entry does not create a nologin trica-agent user")
	}
	// Packages are signed by the release pipeline, never here.
	if strings.Contains(strings.ToLower(read(t, "nfpm.yaml")), "signature:") {
		t.Error("nfpm.yaml signs the packages: signing belongs to the release pipeline")
	}
}

type container struct {
	Name            string   `yaml:"name"`
	Args            []string `yaml:"args"`
	SecurityContext struct {
		Privileged               *bool `yaml:"privileged"`
		AllowPrivilegeEscalation *bool `yaml:"allowPrivilegeEscalation"`
		ReadOnlyRootFilesystem   *bool `yaml:"readOnlyRootFilesystem"`
		Capabilities             struct {
			Drop []string `yaml:"drop"`
			Add  []string `yaml:"add"`
		} `yaml:"capabilities"`
	} `yaml:"securityContext"`
	VolumeMounts []struct {
		Name      string `yaml:"name"`
		MountPath string `yaml:"mountPath"`
		ReadOnly  bool   `yaml:"readOnly"`
	} `yaml:"volumeMounts"`
}

func daemonSet(t *testing.T) (hostPID, hostNetwork bool, c container, hostPaths map[string]string) {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader([]byte(read(t, "../deploy/kubernetes/trica-agent-daemonset.yaml"))))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Template struct {
					Spec struct {
						HostPID     bool        `yaml:"hostPID"`
						HostNetwork bool        `yaml:"hostNetwork"`
						Containers  []container `yaml:"containers"`
						Volumes     []struct {
							Name     string `yaml:"name"`
							HostPath *struct {
								Path string `yaml:"path"`
							} `yaml:"hostPath"`
						} `yaml:"volumes"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
			t.Fatal("no DaemonSet in the manifest")
		} else if err != nil {
			t.Fatal(err)
		}
		if doc.Kind != "DaemonSet" {
			continue
		}
		s := doc.Spec.Template.Spec
		if len(s.Containers) != 1 {
			t.Fatalf("%d containers", len(s.Containers))
		}
		hostPaths = map[string]string{}
		for _, v := range s.Volumes {
			if v.HostPath != nil {
				hostPaths[v.Name] = v.HostPath.Path
			}
		}
		return s.HostPID, s.HostNetwork, s.Containers[0], hostPaths
	}
}

func TestDaemonSetIsAsNarrowAsTheProbesAllow(t *testing.T) {
	hostPID, hostNet, c, hostPaths := daemonSet(t)
	// Both are needed by probes (see the manifest's header), and nothing else is.
	if !hostPID || !hostNet {
		t.Errorf("hostPID=%v hostNetwork=%v: the socket and address probes need both", hostPID, hostNet)
	}
	sc := c.SecurityContext
	if sc.Privileged == nil || *sc.Privileged {
		t.Error("the container must set privileged: false")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("the container must set allowPrivilegeEscalation: false")
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("the root file system must be read-only")
	}
	if !slices.Equal(sc.Capabilities.Drop, []string{"ALL"}) {
		t.Errorf("capabilities dropped: %v, want ALL", sc.Capabilities.Drop)
	}
	add := slices.Clone(sc.Capabilities.Add)
	slices.Sort(add)
	if !slices.Equal(add, []string{"DAC_READ_SEARCH", "SYS_PTRACE"}) {
		t.Errorf("capabilities added: %v (no SMART in the image, so no SYS_RAWIO)", add)
	}
	// Host files are an allow-list, and every mount of one is read-only.
	allowed := map[string]bool{"/etc/os-release": true, "/etc/machine-id": true, "/var/lib/dpkg": true}
	for name, p := range hostPaths {
		if !allowed[p] {
			t.Errorf("hostPath %s (%s) is not one the probes read", name, p)
		}
	}
	for _, m := range c.VolumeMounts {
		if _, host := hostPaths[m.Name]; host && !m.ReadOnly {
			t.Errorf("host path %s is mounted read-write", m.Name)
		}
	}
	for _, a := range c.Args {
		if strings.HasPrefix(a, "--state-dir") {
			t.Error("a DaemonSet updates by rollout of the image, not by staging binaries")
		}
	}
}

func TestEveryCapabilityIsDocumented(t *testing.T) {
	doc := read(t, "../docs/reference/agent.md")
	for _, c := range wantCaps {
		if !strings.Contains(doc, c) {
			t.Errorf("docs/reference/agent.md does not document %s", c)
		}
	}
}
