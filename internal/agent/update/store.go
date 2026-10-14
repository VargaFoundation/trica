package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// BinaryName is the file name of a staged binary: versions/<version>/trica-agent.
const BinaryName = "trica-agent"

// BootedEnv is set by Exec in the environment of the binary it starts: that
// binary was chosen by Boot already and must not choose again.
const BootedEnv = "TRICA_AGENT_BOOTED"

// MaxStarts is how many times a pending version may be started before it is
// given up: a binary that crashes at once never reaches the health timeout.
const MaxStarts = 3

// ErrPending means a version is already staged and has not proven itself yet.
var ErrPending = errors.New("an update is pending")

// Pending is a staged version that has not reported healthy yet.
type Pending struct {
	Version string `json:"version"`
	// Deadline is set at the first start: the version has until then to
	// confirm. Zero until it is started once.
	Deadline time.Time `json:"deadline"`
	// Starts counts the starts since staging.
	Starts int `json:"starts"`
}

// State is what the updater remembers on disk. It holds versions only, never a
// path: a state file that was tampered with can at most pick one of the
// directories this package staged itself, since each version is checked to be
// MAJOR.MINOR.PATCH before it becomes a path.
type State struct {
	// Current is the version to run; "" is the binary the package installed.
	Current string `json:"current"`
	// Previous is what to go back to; "" is the packaged binary.
	Previous string   `json:"previous"`
	Pending  *Pending `json:"pending,omitempty"`
	// Bad lists versions that failed to become healthy: never staged again.
	Bad []string `json:"bad,omitempty"`
}

// Store keeps the staged binaries and the state under one directory
// (/var/lib/trica-agent for the packages).
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore returns a Store rooted at dir, which must exist.
func NewStore(dir string) *Store { return &Store{dir: dir} }

func (s *Store) statePath() string { return filepath.Join(s.dir, "state.json") }

// BinaryPath is where a staged version lives.
func (s *Store) BinaryPath(version string) string {
	return filepath.Join(s.dir, "versions", version, BinaryName)
}

// Load reads the state; no file is the zero state.
func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() (State, error) {
	var st State
	b, err := os.ReadFile(s.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, fmt.Errorf("state file: %w", err)
	}
	for _, v := range []string{st.Current, st.Previous} {
		if v == "" {
			continue
		}
		if _, err := parseVersion(v); err != nil {
			return State{}, fmt.Errorf("state file: %w", err)
		}
	}
	return st, nil
}

// save replaces the state file atomically: a temporary file, synced, renamed.
func (s *Store) save(st State) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".state-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.statePath()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(s.dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // the agent's own state directory, from its flag
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// tempFile creates the file an artifact is downloaded into, in the store so
// that the final rename never crosses a file system.
func (s *Store) tempFile() (*os.File, error) {
	return os.CreateTemp(s.dir, ".download-*")
}

// Install makes tmp, a verified binary, the pending current version. The
// binary is renamed into versions/<version>/ (atomic), then the state is
// replaced (atomic): the switch is the state rename. Nothing is installed while
// another version is still pending.
func (s *Store) Install(version, tmp string) error {
	if _, err := parseVersion(version); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	if st.Pending != nil {
		return ErrPending
	}
	if err := os.Chmod(tmp, 0o755); err != nil { //nolint:gosec // an executable the agent user owns
		return err
	}
	final := s.BinaryPath(version)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil { //nolint:gosec // readable by the unit's user, which owns the directory
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(final)); err != nil {
		return err
	}
	st.Previous, st.Current = st.Current, version
	st.Pending = &Pending{Version: version}
	return s.save(st)
}

// Decision is what to run.
type Decision struct {
	// Exec is the binary to run instead of the one that started; "" means the
	// one that started, the packaged binary.
	Exec string
	// Version is the version Exec is ("" for the packaged binary).
	Version string
	// RolledBack is true when this boot gave up on a pending version.
	RolledBack bool
	// Reason says why, when RolledBack.
	Reason string
}

// Boot is called when the agent starts, before anything else. It decides which
// binary runs. A pending version is started while it has starts left and time
// before its deadline; past either it is declared bad, the previous version is
// restored, and that version is never staged again. The health timeout is
// counted from the first start of the pending version.
func (s *Store) Boot(now time.Time, healthTimeout time.Duration) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return Decision{}, err
	}
	if st.Pending == nil {
		return s.decision(st.Current), nil
	}
	p := st.Pending
	if p.Starts == 0 {
		p.Deadline = now.Add(healthTimeout)
	}
	p.Starts++
	reason := ""
	switch {
	case !executable(s.BinaryPath(st.Current)):
		reason = "staged binary is missing"
	case p.Starts > MaxStarts:
		reason = fmt.Sprintf("not healthy after %d starts", MaxStarts)
	case p.Starts > 1 && now.After(p.Deadline):
		reason = "not healthy before its deadline"
	}
	if reason == "" {
		if err := s.save(st); err != nil {
			return Decision{}, err
		}
		return s.decision(st.Current), nil
	}
	st.Bad = append(st.Bad, p.Version)
	st.Current, st.Previous, st.Pending = st.Previous, "", nil
	if st.Current != "" && !executable(s.BinaryPath(st.Current)) {
		st.Current = ""
	}
	if err := s.save(st); err != nil {
		return Decision{}, err
	}
	d := s.decision(st.Current)
	d.RolledBack, d.Reason = true, reason
	return d, nil
}

func (s *Store) decision(version string) Decision {
	if version == "" {
		return Decision{}
	}
	return Decision{Exec: s.BinaryPath(version), Version: version}
}

// Confirm is called by a running version once it is healthy. It ends the
// pending state if that version is the pending one, and reports whether it was.
// The previous version stays, as the way back, and staged versions older than
// it are removed.
func (s *Store) Confirm(version string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return false, err
	}
	if st.Pending == nil || st.Pending.Version != version {
		return false, nil
	}
	st.Pending = nil
	if err := s.save(st); err != nil {
		return false, err
	}
	s.prune(st)
	return true, nil
}

// prune removes staged versions that are neither current nor previous. Best
// effort: a leftover directory costs disk, nothing else.
func (s *Store) prune(st State) {
	ents, err := os.ReadDir(filepath.Join(s.dir, "versions"))
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.Name() != st.Current && e.Name() != st.Previous {
			_ = os.RemoveAll(filepath.Join(s.dir, "versions", e.Name()))
		}
	}
}

// IsBad reports whether version failed to become healthy before.
func (st State) IsBad(version string) bool { return slices.Contains(st.Bad, version) }

func executable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0
}
