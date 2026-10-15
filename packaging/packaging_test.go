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
	Env []struct {
		Name      string `yaml:"name"`
		ValueFrom struct {
			ConfigMapKeyRef *struct {
				Name     string `yaml:"name"`
				Key      string `yaml:"key"`
				Optional *bool  `yaml:"optional"`
			} `yaml:"configMapKeyRef"`
		} `yaml:"valueFrom"`
	} `yaml:"env"`
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
	// Host files are an allow-list, and every mount of one is read-only, except
	// the agent's own state directory (client certificate and spool: it must
	// outlive the pod, or each restart would need a new enrolment token).
	const stateDir = "/var/lib/trica-agent"
	allowed := map[string]bool{"/etc/os-release": true, "/etc/machine-id": true, "/var/lib/dpkg": true, stateDir: true}
	for name, p := range hostPaths {
		if !allowed[p] {
			t.Errorf("hostPath %s (%s) is not one the probes read", name, p)
		}
	}
	for _, m := range c.VolumeMounts {
		if p, host := hostPaths[m.Name]; host && !m.ReadOnly && p != stateDir {
			t.Errorf("host path %s is mounted read-write", m.Name)
		}
	}
	// The only writable host path is the state directory, and the agent is told
	// to use exactly that one.
	for _, a := range c.Args {
		if v, ok := strings.CutPrefix(a, "--state-dir="); ok && v != stateDir {
			t.Errorf("--state-dir=%s: a DaemonSet updates by rollout of the image, and keeps only %s", v, stateDir)
		}
		if strings.HasPrefix(a, "--output") {
			t.Error("--output would write the inventory to the node's disk for nothing: the batches go to the server")
		}
	}
	for _, want := range []string{"--ca-file=/etc/trica-agent/ca.pem", "--enrol-token-file=/etc/trica-agent-enrol/token", "--state-dir=" + stateDir} {
		if !slices.Contains(c.Args, want) {
			t.Errorf("the DaemonSet does not pass %s", want)
		}
	}
}

// The fleet enrols with one class token from a Secret (ADR 0035). The volume is
// optional, so that deleting the Secret after the rollout does not stop the pods
// from starting, and the token file the agent reads is in it.
// The routing domain of a cluster's nodes (ADR 0036) is an optional key of the
// ConfigMap: a cluster without one stays in the global domain and still starts.
func TestDaemonSetTakesAnOptionalDomain(t *testing.T) {
	_, _, c, _ := daemonSet(t)
	for _, e := range c.Env {
		if e.Name != "TRICA_AGENT_DOMAIN" {
			continue
		}
		ref := e.ValueFrom.ConfigMapKeyRef
		if ref == nil || ref.Name != "trica-agent" || ref.Key != "domain" || ref.Optional == nil || !*ref.Optional {
			t.Fatalf("TRICA_AGENT_DOMAIN = %+v, want the optional key domain of the trica-agent ConfigMap", ref)
		}
		return
	}
	t.Fatal("the DaemonSet does not pass TRICA_AGENT_DOMAIN")
}

func TestDaemonSetEnrolsFromAnOptionalSecret(t *testing.T) {
	dec := yaml.NewDecoder(bytes.NewReader([]byte(read(t, "../deploy/kubernetes/trica-agent-daemonset.yaml"))))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Template struct {
					Spec struct {
						Volumes []struct {
							Name   string `yaml:"name"`
							Secret *struct {
								SecretName string `yaml:"secretName"`
								Optional   *bool  `yaml:"optional"`
							} `yaml:"secret"`
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
		var secrets []string
		for _, v := range doc.Spec.Template.Spec.Volumes {
			if v.Secret == nil {
				continue
			}
			secrets = append(secrets, v.Secret.SecretName)
			if v.Secret.Optional == nil || !*v.Secret.Optional {
				t.Errorf("secret volume %s is not optional: the pods would not start once the token is deleted", v.Name)
			}
		}
		if !slices.Equal(secrets, []string{"trica-agent-enrol"}) {
			t.Errorf("secret volumes %v, want the enrolment token's only", secrets)
		}
		_, _, c, _ := daemonSet(t)
		mounted := false
		for _, m := range c.VolumeMounts {
			mounted = mounted || (m.MountPath == "/etc/trica-agent-enrol" && m.ReadOnly)
		}
		if !mounted {
			t.Error("the enrolment Secret is not mounted read-only at /etc/trica-agent-enrol")
		}
		return
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

func TestUnitSendsToTheServerFromItsEnvironmentFile(t *testing.T) {
	u := unit(t)
	if !strings.Contains(u["EnvironmentFile"], "/etc/trica-agent/agent.env") {
		t.Errorf("EnvironmentFile = %q, want the packaged /etc/trica-agent/agent.env", u["EnvironmentFile"])
	}
	// A token is a reference to a file, never a value of the configuration.
	env := read(t, "agent.env")
	for _, want := range []string{"TRICA_AGENT_SERVER", "TRICA_AGENT_CA_FILE", "TRICA_AGENT_ENROL_TOKEN_FILE", "TRICA_AGENT_DOMAIN"} {
		if !strings.Contains(env, want) {
			t.Errorf("agent.env does not document %s", want)
		}
	}
	if strings.Contains(env, "TRICA_AGENT_ENROL_TOKEN=") {
		t.Error("agent.env takes a token value: only a file reference is allowed")
	}
	var cfg struct {
		Contents []struct {
			Src  string `yaml:"src"`
			Dst  string `yaml:"dst"`
			Type string `yaml:"type"`
		} `yaml:"contents"`
	}
	if err := yaml.Unmarshal([]byte(read(t, "nfpm.yaml")), &cfg); err != nil {
		t.Fatal(err)
	}
	for _, c := range cfg.Contents {
		if c.Dst == "/etc/trica-agent/agent.env" {
			if c.Src != "packaging/agent.env" || c.Type != "config|noreplace" {
				t.Errorf("agent.env is shipped as %+v: it must be config|noreplace so that an upgrade keeps the operator's edits", c)
			}
			return
		}
	}
	t.Error("nfpm does not ship /etc/trica-agent/agent.env")
}
