package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrNotFound means the command is not installed: the probe is skipped, not failed.
var ErrNotFound = errors.New("command not found")

// ErrNotAllowed means the command line is not on the allow-list.
var ErrNotAllowed = errors.New("command not allowed")

// Runner executes one read-only command and returns its standard output. A
// command that exits non-zero returns its output together with the error:
// smartctl, for one, encodes health findings in its exit status.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Command lines the agent may run. Everything else is refused by ExecRunner,
// whatever a probe asks for: the agent has no way to change the host through
// a command.
const (
	rpmQueryFormat = `%{NAME}\t%|EPOCH?{%{EPOCH}:}|%{VERSION}-%{RELEASE}\t%{ARCH}\n`
)

var smartDevice = regexp.MustCompile(`^/dev/[a-z][a-z0-9]*$`)

// allowedCommand reports whether name with args is one of the exact command
// lines of the allow-list.
func allowedCommand(name string, args []string) bool {
	join := strings.Join(args, " ")
	switch name {
	case "rpm":
		return len(args) == 3 && args[0] == "-qa" && args[1] == "--qf" && args[2] == rpmQueryFormat
	case "systemctl":
		return join == "list-units --type=service --output=json --no-pager"
	case "lldpctl":
		return join == "-f json"
	case "smartctl":
		return len(args) == 3 && args[0] == "--json" && args[1] == "-a" && smartDevice.MatchString(args[2])
	}
	return false
}

// ExecRunner runs allow-listed commands on the real host. The binary is
// looked up in fixed system directories, not through PATH, and runs with an
// empty environment: the agent's own environment may hold secrets and is
// never handed down.
type ExecRunner struct {
	// Dirs are searched in order; default: the standard system binary directories.
	Dirs []string
	// Timeout bounds one command; default 20 s.
	Timeout time.Duration
	// MaxOutput bounds the captured output; default 32 MiB.
	MaxOutput int64
}

var defaultBinDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin", "/usr/local/sbin", "/usr/local/bin"}

func (r ExecRunner) lookup(name string) (string, error) {
	dirs := r.Dirs
	if len(dirs) == 0 {
		dirs = defaultBinDirs
	}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: %w", name, ErrNotFound)
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if !allowedCommand(name, args) {
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), ErrNotAllowed)
	}
	bin, err := r.lookup(name)
	if err != nil {
		return nil, err
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	maxOut := r.MaxOutput
	if maxOut == 0 {
		maxOut = 32 << 20
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// The command line was checked against the allow-list above.
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // allow-listed command lines only, see allowedCommand
	cmd.Env = []string{"LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &out, left: maxOut}
	err = cmd.Run()
	return out.Bytes(), err
}

// limitedWriter keeps at most left bytes and silently drops the rest.
type limitedWriter struct {
	w    io.Writer
	left int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	l.left -= int64(len(p))
	if len(p) > 0 {
		if _, err := l.w.Write(p); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// noRunner refuses everything: used when the agent observes a fake root, so
// that a test or a forensic run never executes commands of the machine it
// runs on while describing another tree.
type noRunner struct{}

func (noRunner) Run(_ context.Context, name string, _ ...string) ([]byte, error) {
	return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
}
