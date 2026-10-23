package theme

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config says where the deployment's theme comes from. At most one of Builtin,
// Path and URL is set; the zero Config is the built-in default.
type Config struct {
	Builtin string
	Path    string // a zip file or a directory
	URL     string // https, with SHA256; the archive is kept in the Store
	SHA256  string
	// Locked makes the configuration always win: an administrator's selection
	// is kept but ignored, and the API refuses to change it.
	Locked bool
	Reload time.Duration // how often Path is read again; 0 means DefaultReload

	// Store keeps uploaded archives and the administrator's selection. Nil: the
	// service serves the configuration and nothing else (no URL source, no
	// selection).
	Store *Store
	// Client downloads the URL source; nil means a default client. Tests pass
	// one that trusts their server.
	Client *http.Client
}

// DefaultReload is how often a path source is read again.
const DefaultReload = time.Minute

// keep is how many themes Lookup still serves after a change: a page loaded
// just before a swap still fetches the files of the stylesheet it holds.
const keep = 3

// onDemandMax bounds the themes read from the database only to answer a
// request for one of their files.
const onDemandMax = 8

// Service holds the theme in force. A theme that fails to load never stops the
// server: the previous one stays (the built-in default at start), the failure
// is logged and counted.
//
// The theme in force is the deployment default (the configuration, else the
// built-in default), unless an administrator selected another one and the
// configuration is not locked (ADR 0054).
type Service struct {
	cfg Config
	log *slog.Logger
	cur atomic.Pointer[Theme]

	mu         sync.Mutex
	dep        *Theme // the deployment default; never nil
	sel        *Theme // the administrator's selection; nil for none
	selBuiltin bool
	recent     []*Theme
	onDemand   map[string]*Theme

	// OnLoad is called each time a different theme becomes active; OnError each
	// time a read fails. Both are optional and set before Run.
	OnLoad  func(*Theme)
	OnError func(source string, err error)
}

// New builds the service and loads what can be loaded without waiting: the
// built-in themes, a path, and a URL source already kept in the database. A
// configuration that cannot be honoured at all (an unknown built-in name, a URL
// without its digest) is an error; a source that does not load is not: the
// previous theme serves until it does.
func New(cfg Config, log *slog.Logger) (*Service, error) {
	n := 0
	for _, v := range []string{cfg.Builtin, cfg.Path, cfg.URL} {
		if v != "" {
			n++
		}
	}
	switch {
	case n > 1:
		return nil, errors.New("theme: builtin, path and url are exclusive")
	case cfg.URL != "" && (cfg.Store == nil || !isSHA(cfg.SHA256)):
		return nil, errors.New("theme: a url needs its sha256 and a database to keep the archive in")
	case cfg.URL != "":
		if err := ValidateURL(cfg.URL); err != nil {
			return nil, fmt.Errorf("theme: url: %w", err)
		}
	case cfg.Locked && n == 0:
		return nil, errors.New("theme: locked needs a theme source to lock to")
	}
	if cfg.Reload <= 0 {
		cfg.Reload = DefaultReload
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Service{cfg: cfg, log: log, onDemand: map[string]*Theme{}}
	def, err := Builtin(DefaultName)
	if err != nil {
		return nil, err
	}
	if cfg.Builtin != "" {
		if def, err = Builtin(cfg.Builtin); err != nil {
			return nil, err
		}
	}
	s.dep = def
	s.apply()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch {
	case cfg.Path != "":
		s.reloadPath()
	case cfg.URL != "":
		s.loadKept(ctx)
	}
	s.Refresh(ctx)
	return s, nil
}

func isSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == strings.ToLower(s)
}

// Locked says whether the configuration always wins.
func (s *Service) Locked() bool { return s.cfg.Locked }

// Active is the theme in force.
func (s *Service) Active() *Theme { return s.cur.Load() }

// apply picks the theme in force from the deployment default and the selection,
// and announces it when it is another one.
func (s *Service) apply() {
	s.mu.Lock()
	eff := s.dep
	if s.sel != nil && !s.cfg.Locked {
		eff = s.sel
	}
	if s.cur.Load() == eff {
		s.mu.Unlock()
		return
	}
	s.recent = append([]*Theme{eff}, s.recent...)
	if len(s.recent) > keep {
		s.recent = s.recent[:keep]
	}
	s.cur.Store(eff)
	s.mu.Unlock()
	s.log.Info("theme in force", "source", "theme", "name", eff.Name, "version", eff.Version, "sha256", eff.SHA, "from", eff.Source)
	if s.OnLoad != nil {
		s.OnLoad(eff)
	}
}

// fail logs and counts a read that failed. what is the source of the theme:
// path, url, stored (an archive of the database) or selection.
func (s *Service) fail(what string, err error, msg string, args ...any) {
	s.log.Error(msg, append([]any{"source", "theme", "from", what, "error", err.Error()}, args...)...)
	if s.OnError != nil {
		s.OnError(what, err)
	}
}

// Lookup returns a file of the theme with this identity: among the themes held,
// else from the database, so that a page served by a replica that has since
// moved on (or by another replica) still gets the files of the stylesheet it
// holds.
func (s *Service) Lookup(sha, file string) (Asset, bool) {
	s.mu.Lock()
	cands := append([]*Theme{s.dep, s.sel}, s.recent...)
	for _, t := range cands {
		if t != nil && t.SHA == sha {
			s.mu.Unlock()
			return t.Asset(file)
		}
	}
	t := s.onDemand[sha]
	s.mu.Unlock()
	if t == nil && s.cfg.Store != nil && isSHA(sha) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if t = s.readStored(ctx, sha); t != nil {
			s.mu.Lock()
			if len(s.onDemand) >= onDemandMax {
				clear(s.onDemand)
			}
			s.onDemand[sha] = t
			s.mu.Unlock()
		}
	}
	if t == nil {
		return Asset{}, false
	}
	return t.Asset(file)
}

// readStored reads and validates an archive of the database; nil when there is
// none or it no longer validates.
func (s *Service) readStored(ctx context.Context, sha string) *Theme {
	b, err := s.cfg.Store.Archive(ctx, sha)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.fail("stored", err, "stored theme not read")
		}
		return nil
	}
	t, err := ReadArchive(b)
	if err != nil {
		s.fail("stored", err, "stored theme refused, it no longer validates", "sha256", sha)
		return nil
	}
	t.Source = "stored"
	return t
}

// Reload reads the configured source again (a path, or a URL not yet kept) and
// the administrator's selection, and swaps the theme when one changed.
func (s *Service) Reload(ctx context.Context) {
	switch {
	case s.cfg.Path != "":
		s.reloadPath()
	case s.cfg.URL != "":
		if !s.loadKept(ctx) {
			s.fetchURL(ctx)
		}
	}
	s.Refresh(ctx)
}

func (s *Service) setDefault(t *Theme) {
	s.mu.Lock()
	s.dep = t
	s.mu.Unlock()
	s.apply()
}

func (s *Service) reloadPath() {
	t, err := ReadPath(s.cfg.Path)
	if err != nil {
		s.fail("path", err, "theme not loaded, keeping the previous one", "path", s.cfg.Path)
		return
	}
	s.mu.Lock()
	same := s.dep.SHA == t.SHA
	s.mu.Unlock()
	if same {
		return
	}
	t.Source = "path"
	s.setDefault(t)
}

// loadKept takes the URL source from the database, where a download left it.
func (s *Service) loadKept(ctx context.Context) bool {
	s.mu.Lock()
	have := s.dep.SHA == s.cfg.SHA256
	s.mu.Unlock()
	if have {
		return true
	}
	t := s.readStored(ctx, s.cfg.SHA256)
	if t == nil {
		return false
	}
	t.Source = "url"
	s.setDefault(t)
	return true
}

// fetchURL downloads the URL source once, checks it against the pinned digest
// and keeps it. Nothing is applied, nor stored, unless the digest matches.
func (s *Service) fetchURL(ctx context.Context) {
	b, err := Fetch(ctx, s.cfg.Client, s.cfg.URL, s.cfg.SHA256)
	if err != nil {
		s.fail("url", err, "theme not downloaded, keeping the previous one")
		return
	}
	t, err := ReadArchive(b)
	if err != nil {
		s.fail("url", err, "downloaded theme refused, keeping the previous one")
		return
	}
	if _, err := s.cfg.Store.Put(ctx, t, b, OriginURL, "config"); err != nil {
		s.fail("url", err, "downloaded theme not kept")
		return
	}
	t.Source = "url"
	s.setDefault(t)
}

// Refresh reads the administrator's selection and applies it. A selection that
// cannot be read leaves the previous one in force.
func (s *Service) Refresh(ctx context.Context) {
	if s.cfg.Store == nil {
		return
	}
	row, err := s.cfg.Store.Selected(ctx)
	if err != nil {
		s.fail("selection", err, "theme selection not read")
		return
	}
	var t *Theme
	builtin := false
	switch {
	case row == nil:
	case row.Builtin != "":
		if t, err = Builtin(row.Builtin); err != nil {
			s.fail("selection", err, "selected theme unknown to this version, keeping the previous one")
			return
		}
		builtin = true
	default:
		s.mu.Lock()
		held := s.sel
		s.mu.Unlock()
		if held != nil && held.SHA == row.SHA256 {
			t = held
		} else if t = s.readStored(ctx, row.SHA256); t == nil {
			return
		}
	}
	s.mu.Lock()
	s.sel, s.selBuiltin = t, builtin
	s.mu.Unlock()
	s.apply()
}

// Ref describes a theme, for the administrator.
type Ref struct {
	// Source is "config" for the deployment default, "builtin" or "stored" for
	// an administrator's selection.
	Source  string
	Name    string
	Title   string
	Version string
	// SHA256 is empty for a built-in theme.
	SHA256 string
}

func ref(t *Theme, source string) Ref {
	r := Ref{Source: source, Name: t.Name, Title: t.Title, Version: t.Version}
	if isSHA(t.SHA) {
		r.SHA256 = t.SHA
	}
	return r
}

// State is what the administrator sees.
type State struct {
	Active   Ref
	Default  Ref
	Locked   bool
	Builtins []Ref
	Themes   []Info
}

// State describes the theme in force, the deployment default, the built-in
// themes and the stored archives.
func (s *Service) State(ctx context.Context) (State, error) {
	s.mu.Lock()
	dep, sel, selBuiltin := s.dep, s.sel, s.selBuiltin
	s.mu.Unlock()
	st := State{Default: ref(dep, "config"), Locked: s.cfg.Locked, Builtins: []Ref{}, Themes: []Info{}}
	st.Active = st.Default
	if sel != nil && !s.cfg.Locked {
		src := "stored"
		if selBuiltin {
			src = "builtin"
		}
		st.Active = ref(sel, src)
	}
	for _, n := range BuiltinNames() {
		if t, err := Builtin(n); err == nil {
			st.Builtins = append(st.Builtins, ref(t, "builtin"))
		}
	}
	if s.cfg.Store != nil {
		var err error
		if st.Themes, err = s.cfg.Store.List(ctx); err != nil {
			return st, err
		}
	}
	return st, nil
}

var errNoStore = errors.New("theme: no database to keep themes in")

// Upload validates an archive and keeps it, without selecting it. The same
// bytes uploaded again are the same theme: created is false.
func (s *Service) Upload(ctx context.Context, archive []byte, by string) (info Info, created bool, err error) {
	if s.cfg.Store == nil {
		return Info{}, false, errNoStore
	}
	t, err := ReadArchive(archive)
	if err != nil {
		return Info{}, false, err
	}
	if created, err = s.cfg.Store.Put(ctx, t, archive, OriginUpload, by); err != nil {
		return Info{}, false, err
	}
	info, err = s.cfg.Store.Info(ctx, t.SHA)
	return info, created, err
}

// Activate selects a built-in theme (sel.Builtin) or a stored one (sel.SHA256)
// for every replica. ErrLocked when the configuration always wins, ErrNotFound
// for a theme nobody has.
func (s *Service) Activate(ctx context.Context, sel Selection) error {
	if s.cfg.Store == nil {
		return errNoStore
	}
	if s.cfg.Locked {
		return ErrLocked
	}
	if sel.Builtin != "" {
		if _, err := Builtin(sel.Builtin); err != nil {
			return ErrNotFound
		}
	}
	if err := s.cfg.Store.Select(ctx, sel); err != nil {
		return err
	}
	s.Refresh(ctx)
	return nil
}

// Reset drops the administrator's selection: the deployment default is back.
func (s *Service) Reset(ctx context.Context) error {
	if s.cfg.Store == nil {
		return errNoStore
	}
	if s.cfg.Locked {
		return ErrLocked
	}
	if err := s.cfg.Store.Clear(ctx); err != nil {
		return err
	}
	s.Refresh(ctx)
	return nil
}

// Delete removes a stored archive. The one in force, the one selected and the
// deployment default are refused: ErrActive.
func (s *Service) Delete(ctx context.Context, sha string) error {
	if s.cfg.Store == nil {
		return errNoStore
	}
	s.mu.Lock()
	inForce := sha == s.dep.SHA || (s.sel != nil && sha == s.sel.SHA) || sha == s.cur.Load().SHA
	s.mu.Unlock()
	if inForce {
		return ErrActive
	}
	return s.cfg.Store.Delete(ctx, sha)
}

// Run keeps the theme current until ctx ends: a path is read again every
// Reload, a URL is downloaded until it has been, and the selection follows the
// database, at once when another replica announces a change and at every
// Reload in case an announcement was missed.
func (s *Service) Run(ctx context.Context) error {
	wake := make(chan struct{}, 1)
	var listening sync.WaitGroup
	if s.cfg.Store != nil {
		listening.Add(1)
		go func() {
			defer listening.Done()
			_ = s.cfg.Store.DB.Listen(ctx, Channel, func() {
				select {
				case wake <- struct{}{}:
				default:
				}
			}, s.log)
		}()
	}
	defer listening.Wait()
	s.Reload(ctx)
	tick := time.NewTicker(s.cfg.Reload)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			s.Reload(ctx)
		case <-wake:
			s.Refresh(ctx)
		}
	}
}
