package theme

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Config says where the theme comes from. Exactly one of Builtin and Path is
// set; the zero Config is the built-in default.
type Config struct {
	Builtin string
	Path    string        // a zip file or a directory
	Reload  time.Duration // how often Path is read again; 0 means DefaultReload
}

// DefaultReload is how often a path source is read again.
const DefaultReload = time.Minute

// keep is how many themes Lookup still serves after a change: a page loaded
// just before a swap still fetches the files of the stylesheet it holds.
const keep = 3

// Service holds the theme in force. A theme that fails to load never stops the
// server: the previous one stays (the built-in default at start), the failure
// is logged and counted.
type Service struct {
	cfg Config
	log *slog.Logger
	cur atomic.Pointer[Theme]

	mu     sync.Mutex
	recent []*Theme

	// OnLoad is called each time a different theme becomes active; OnError each
	// time a read fails. Both are optional and set before Run.
	OnLoad  func(*Theme)
	OnError func(error)
}

// New builds the service and loads the theme once. A configuration that cannot
// be honoured at all (an unknown built-in name) is an error; a path that does
// not load is not: the built-in default serves until it does.
func New(cfg Config, log *slog.Logger) (*Service, error) {
	if cfg.Builtin != "" && cfg.Path != "" {
		return nil, fmt.Errorf("theme: builtin and path are exclusive")
	}
	if cfg.Reload <= 0 {
		cfg.Reload = DefaultReload
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Service{cfg: cfg, log: log}
	def, err := Builtin(DefaultName)
	if err != nil {
		return nil, err
	}
	first := def
	if cfg.Builtin != "" {
		if first, err = Builtin(cfg.Builtin); err != nil {
			return nil, err
		}
	}
	s.set(first)
	if cfg.Path != "" {
		s.Reload()
	}
	return s, nil
}

// Active is the theme in force.
func (s *Service) Active() *Theme { return s.cur.Load() }

func (s *Service) set(t *Theme) {
	s.mu.Lock()
	s.recent = append([]*Theme{t}, s.recent...)
	if len(s.recent) > keep {
		s.recent = s.recent[:keep]
	}
	s.mu.Unlock()
	s.cur.Store(t)
	if s.OnLoad != nil {
		s.OnLoad(t)
	}
}

// Lookup returns a file of the theme with this identity, among the themes
// still held.
func (s *Service) Lookup(sha, file string) (Asset, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.recent {
		if t.SHA == sha {
			return t.Asset(file)
		}
	}
	return Asset{}, false
}

// Reload reads the path source again and swaps the theme when it changed.
func (s *Service) Reload() {
	if s.cfg.Path == "" {
		return
	}
	t, err := ReadPath(s.cfg.Path)
	if err != nil {
		s.log.Error("theme not loaded, keeping the previous one", "source", "theme", "path", s.cfg.Path, "error", err.Error())
		if s.OnError != nil {
			s.OnError(err)
		}
		return
	}
	if cur := s.cur.Load(); cur != nil && cur.SHA == t.SHA {
		return
	}
	t.Source = "path"
	s.log.Info("theme loaded", "source", "theme", "name", t.Name, "version", t.Version, "sha256", t.SHA)
	s.set(t)
}

// Run reads the path source again every Reload until ctx ends. A built-in
// source has nothing to read: Run only waits.
func (s *Service) Run(ctx context.Context) error {
	if s.cfg.Path == "" {
		<-ctx.Done()
		return nil
	}
	tick := time.NewTicker(s.cfg.Reload)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			s.Reload()
		}
	}
}
