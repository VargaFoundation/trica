package uplink

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrSpoolFull is returned when a single batch is larger than the whole spool
// budget: it is counted as dropped, as spooling it would evict everything else
// for nothing.
var ErrSpoolFull = errors.New("transport: batch larger than the spool budget")

const (
	spoolSuffix = ".batch"
	spoolTmp    = ".tmp-"
)

// spool is a directory of pending batches, one file per batch (the gzip body
// exactly as it would be posted), named by an increasing sequence so that a
// lexical sort is the order of emission. Not safe for concurrent use: the
// Client serialises access.
type spool struct {
	dir     string
	max     int64
	now     func() time.Time
	last    int64
	dropped uint64
}

type spoolEntry struct {
	name string
	size int64
}

func newSpool(dir string, maxBytes int64, now func() time.Time) (*spool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("transport: spool directory: %w", err)
	}
	s := &spool{dir: dir, max: maxBytes, now: now}
	// A crash between the temporary write and the rename leaves a .tmp file
	// that was never a batch: remove it.
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("transport: spool directory: %w", err)
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), spoolTmp) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	list, err := s.list()
	if err != nil {
		return nil, err
	}
	if n := len(list); n > 0 {
		_, _ = fmt.Sscanf(list[n-1].name, "%d", &s.last)
	}
	return s, nil
}

// list returns the pending batches, oldest first.
func (s *spool) list() ([]spoolEntry, error) {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("transport: read spool: %w", err)
	}
	var out []spoolEntry
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), spoolSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, spoolEntry{name: e.Name(), size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// add writes body as a new pending batch, first evicting the oldest ones
// (counted in dropped) until it fits the budget. The write is atomic: the
// batch is complete in a temporary file, synced, then renamed.
func (s *spool) add(body []byte) error {
	size := int64(len(body))
	if size > s.max {
		s.dropped++
		return ErrSpoolFull
	}
	list, err := s.list()
	if err != nil {
		return err
	}
	var total int64
	for _, e := range list {
		total += e.size
	}
	for len(list) > 0 && total+size > s.max {
		if err := os.Remove(filepath.Join(s.dir, list[0].name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("transport: evict from spool: %w", err)
		}
		total -= list[0].size
		list = list[1:]
		s.dropped++
	}
	seq := s.now().UnixNano()
	if seq <= s.last {
		seq = s.last + 1
	}
	s.last = seq
	return atomicWrite(filepath.Join(s.dir, fmt.Sprintf("%020d%s", seq, spoolSuffix)), body, 0o600)
}

func (s *spool) read(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.dir, name)) // #nosec G304 -- name comes from our own listing
}

func (s *spool) remove(name string) error {
	err := os.Remove(filepath.Join(s.dir, name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// stats returns the number and total size of the pending batches.
func (s *spool) stats() (n int, bytes int64) {
	list, _ := s.list()
	for _, e := range list {
		bytes += e.size
	}
	return len(list), bytes
}

// atomicWrite writes data to path through a synced temporary file in the same
// directory and a rename: a reader sees the old content or the new, never half.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	tmp := filepath.Join(filepath.Dir(path), spoolTmp+hex.EncodeToString(rnd[:]))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) // #nosec G304 -- inside our own directory
	if err != nil {
		return fmt.Errorf("transport: write %s: %w", filepath.Base(path), err)
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("transport: write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("transport: write %s: %w", filepath.Base(path), err)
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil { // #nosec G304 -- our own directory
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
