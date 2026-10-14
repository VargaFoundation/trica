package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllowedCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want bool
	}{
		{"rpm query", "rpm", []string{"-qa", "--qf", rpmQueryFormat}, true},
		{"rpm erase", "rpm", []string{"-e", "bash"}, false},
		{"rpm query other format", "rpm", []string{"-qa", "--qf", "%{NAME}"}, false},
		{"rpm install after query", "rpm", []string{"-qa", "--qf", rpmQueryFormat, "-i"}, false},
		{"systemctl list", "systemctl", []string{"list-units", "--type=service", "--output=json", "--no-pager"}, true},
		{"systemctl stop", "systemctl", []string{"stop", "sshd"}, false},
		{"systemctl list then restart", "systemctl", []string{"list-units", "--type=service", "--output=json", "--no-pager", "restart"}, false},
		{"lldpctl json", "lldpctl", []string{"-f", "json"}, true},
		{"lldpctl configure", "lldpctl", []string{"configure", "lldp", "portidsubtype", "ifname"}, false},
		{"smartctl read", "smartctl", []string{"--json", "-a", "/dev/sda"}, true},
		{"smartctl nvme", "smartctl", []string{"--json", "-a", "/dev/nvme0n1"}, true},
		{"smartctl self-test", "smartctl", []string{"--json", "-t", "long", "/dev/sda"}, false},
		{"smartctl path traversal", "smartctl", []string{"--json", "-a", "/dev/../etc/shadow"}, false},
		{"smartctl non-device path", "smartctl", []string{"--json", "-a", "/etc/shadow"}, false},
		{"smartctl option as device", "smartctl", []string{"--json", "-a", "--device=x"}, false},
		{"unknown binary", "rm", []string{"-rf", "/"}, false},
		{"shell", "sh", []string{"-c", "id"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := allowedCommand(tc.cmd, tc.args); got != tc.want {
				t.Fatalf("allowedCommand(%s %v) = %v, want %v", tc.cmd, tc.args, got, tc.want)
			}
		})
	}
}

func TestExecRunnerRefusesWhatIsNotAllowListed(t *testing.T) {
	// The directory holds a binary of the right name: only the allow-list stops it.
	dir := t.TempDir()
	script(t, dir, "systemctl", "#!/bin/sh\necho ran\n")
	r := ExecRunner{Dirs: []string{dir}}
	out, err := r.Run(context.Background(), "systemctl", "stop", "sshd")
	if !errors.Is(err, ErrNotAllowed) || len(out) != 0 {
		t.Fatalf("out %q err %v: a command off the allow-list ran", out, err)
	}
	out, err = r.Run(context.Background(), "systemctl", "list-units", "--type=service", "--output=json", "--no-pager")
	if err != nil || strings.TrimSpace(string(out)) != "ran" {
		t.Fatalf("allowed command: out %q err %v", out, err)
	}
}

func TestExecRunnerNotInstalled(t *testing.T) {
	r := ExecRunner{Dirs: []string{t.TempDir()}}
	_, err := r.Run(context.Background(), "lldpctl", "-f", "json")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestExecRunnerIgnoresPATH(t *testing.T) {
	// A binary of the right name found through PATH must not run: the lookup
	// is restricted to the fixed directories.
	pathDir := t.TempDir()
	script(t, pathDir, "lldpctl", "#!/bin/sh\necho from-path\n")
	t.Setenv("PATH", pathDir+":"+os.Getenv("PATH"))
	r := ExecRunner{Dirs: []string{t.TempDir()}}
	if out, err := r.Run(context.Background(), "lldpctl", "-f", "json"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("out %q err %v: PATH was searched", out, err)
	}
}

func TestExecRunnerHandsDownNoEnvironment(t *testing.T) {
	t.Setenv("TRICA_TEST_SECRET", "hunter2")
	dir := t.TempDir()
	script(t, dir, "lldpctl", "#!/bin/sh\n/usr/bin/env\n")
	out, err := ExecRunner{Dirs: []string{dir}}.Run(context.Background(), "lldpctl", "-f", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "hunter2") || strings.Contains(string(out), "TRICA_TEST_SECRET") {
		t.Fatalf("the agent's environment reached the command:\n%s", out)
	}
	if !strings.Contains(string(out), "LC_ALL=C") {
		t.Fatalf("expected the fixed environment, got:\n%s", out)
	}
}

func TestExecRunnerNonZeroExitKeepsOutput(t *testing.T) {
	// smartctl reports findings through its exit status and still prints JSON.
	dir := t.TempDir()
	script(t, dir, "smartctl", "#!/bin/sh\necho '{\"x\":1}'\nexit 4\n")
	out, err := ExecRunner{Dirs: []string{dir}}.Run(context.Background(), "smartctl", "--json", "-a", "/dev/sda")
	if err == nil {
		t.Fatal("a non-zero exit was not reported")
	}
	if strings.TrimSpace(string(out)) != `{"x":1}` {
		t.Fatalf("output lost on non-zero exit: %q", out)
	}
}

func TestExecRunnerBoundsTimeAndOutput(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "lldpctl", "#!/bin/sh\nexec sleep 30\n")
	start := time.Now()
	_, err := ExecRunner{Dirs: []string{dir}, Timeout: 200 * time.Millisecond}.Run(context.Background(), "lldpctl", "-f", "json")
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("a hanging command was not stopped (err %v after %v)", err, time.Since(start))
	}

	script(t, dir, "systemctl", "#!/bin/sh\nhead -c 100000 /dev/zero | tr '\\0' x\n")
	out, err := ExecRunner{Dirs: []string{dir}, MaxOutput: 1000}.Run(context.Background(), "systemctl", "list-units", "--type=service", "--output=json", "--no-pager")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1000 {
		t.Fatalf("captured %d bytes, the bound is 1000", len(out))
	}
}

func TestLimitedWriterReportsFullWrites(t *testing.T) {
	// io.Copy and exec treat a short write as an error: dropped bytes are not one.
	var sb strings.Builder
	w := &limitedWriter{w: &sb, left: 3}
	n, err := w.Write([]byte("abcdef"))
	if n != 6 || err != nil || sb.String() != "abc" {
		t.Fatalf("n=%d err=%v kept=%q", n, err, sb.String())
	}
}

func TestNoRunnerRunsNothing(t *testing.T) {
	_, err := noRunner{}.Run(context.Background(), "systemctl", "list-units", "--type=service", "--output=json", "--no-pager")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func script(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
