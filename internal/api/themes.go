package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ThemeAdmin is the console's theme, administered through the API (ADR 0054).
// The implementation is internal/theme behind an adapter, so that this package
// owns its own types and errors (ADR 0025).
type ThemeAdmin interface {
	Themes(ctx context.Context) (ThemeState, error)
	// Upload keeps an archive without selecting it; created is false when the
	// same bytes were already there. A theme that does not validate is a
	// *ThemeInvalidError.
	Upload(ctx context.Context, archive []byte, by string) (info StoredTheme, created bool, err error)
	// Activate selects a built-in theme or a stored one: ErrThemeLocked,
	// ErrThemeNotFound.
	Activate(ctx context.Context, sel ThemeSelection, by string) error
	// Reset drops the selection: ErrThemeLocked.
	Reset(ctx context.Context, by string) error
	// Delete removes a stored archive: ErrThemeActive, ErrThemeNotFound.
	Delete(ctx context.Context, sha256 string) error
}

// What a ThemeAdmin answers when it refuses.
var (
	ErrThemeLocked   = errors.New("api: the deployment configuration locks the theme")
	ErrThemeActive   = errors.New("api: the theme is in force")
	ErrThemeNotFound = errors.New("api: no such theme")
)

// ThemeInvalidError says why an archive is not a theme: Detail lists every
// reason found.
type ThemeInvalidError struct{ Detail string }

func (e *ThemeInvalidError) Error() string { return "api: invalid theme: " + e.Detail }

// MaxThemeBody bounds an uploaded archive (the archive limit of the theme
// format, with a little room for nothing: the validator checks the exact size).
const MaxThemeBody = 2<<20 + 1

// ThemeRef describes a theme.
type ThemeRef struct {
	// Source is "config" for the deployment default, "builtin" or "stored" for an
	// administrator's selection (on the active theme), "builtin" in the list of
	// built-in themes.
	Source  string `json:"source"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
	// SHA256 identifies a stored archive; empty for a built-in theme.
	SHA256 string `json:"sha256,omitempty"`
}

// StoredTheme is an archive kept in the database.
type StoredTheme struct {
	SHA256     string    `json:"sha256"`
	Name       string    `json:"name"`
	Title      string    `json:"title"`
	Version    string    `json:"version"`
	Size       int       `json:"size"`
	Origin     string    `json:"origin"`
	UploadedBy string    `json:"uploaded_by"`
	UploadedAt time.Time `json:"uploaded_at"`
}

// ThemeState is what the administrator sees.
type ThemeState struct {
	// Active is the theme in force now.
	Active ThemeRef `json:"active"`
	// DeploymentDefault is what the configuration says (the built-in default
	// when it says nothing): what Reset returns to.
	DeploymentDefault ThemeRef `json:"deployment_default"`
	// Locked says the configuration always wins: the selection cannot change.
	Locked   bool          `json:"locked"`
	Builtins []ThemeRef    `json:"builtins"`
	Themes   []StoredTheme `json:"themes"`
}

// ThemeSelection is a choice: exactly one of the two fields.
type ThemeSelection struct {
	Builtin string `json:"builtin,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

// listed makes the lists JSON arrays even when empty: the document says
// arrays, and a client iterates them.
func (st ThemeState) listed() ThemeState {
	if st.Builtins == nil {
		st.Builtins = []ThemeRef{}
	}
	if st.Themes == nil {
		st.Themes = []StoredTheme{}
	}
	return st
}

var themeSHARe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Server) themeAdmin() (ThemeAdmin, error) {
	if s.themes == nil {
		return nil, notFound("themes are not available on this server")
	}
	return s.themes, nil
}

// themeActor is who changed the theme, as the table records it.
func themeActor(c *call) string { return string(c.p.Kind) + ":" + c.p.Subject }

// themeProblem turns the refusals of a ThemeAdmin into problems.
func themeProblem(err error) error {
	var invalid *ThemeInvalidError
	switch {
	case errors.As(err, &invalid):
		he := newErr(http.StatusUnprocessableEntity, "invalid-theme", "%s", invalid.Detail)
		he.title = "Invalid theme"
		return he
	case errors.Is(err, ErrThemeLocked):
		return newErr(http.StatusConflict, "theme-locked", "the deployment configuration (web.theme.locked) decides the theme; the API cannot change it")
	case errors.Is(err, ErrThemeActive):
		return newErr(http.StatusConflict, "theme-active", "this theme is in force; select another one first")
	case errors.Is(err, ErrThemeNotFound):
		return notFound("no such theme")
	}
	return err
}

func (s *Server) hThemes(c *call) error {
	ta, err := s.themeAdmin()
	if err != nil {
		return err
	}
	st, err := ta.Themes(c.ctx())
	if err != nil {
		return themeProblem(err)
	}
	return c.json(http.StatusOK, st.listed())
}

func (s *Server) hThemeUpload(c *call) error {
	ta, err := s.themeAdmin()
	if err != nil {
		return err
	}
	c.audited("theme.upload", c.r.URL.Path, nil)
	body, err := io.ReadAll(c.r.Body)
	if err != nil {
		return err // a body over the limit is a 413 (see classify)
	}
	if len(body) == 0 {
		return badRequest("the request body is empty: send the zip archive itself")
	}
	info, created, err := ta.Upload(c.ctx(), body, themeActor(c))
	if err != nil {
		return themeProblem(err)
	}
	c.audited("theme.upload", info.SHA256, map[string]any{"name": info.Name, "version": info.Version, "size": info.Size, "created": created})
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return c.json(status, info)
}

func (s *Server) hThemeActivate(c *call) error {
	ta, err := s.themeAdmin()
	if err != nil {
		return err
	}
	var sel ThemeSelection
	if err := decodeStrict(c.r.Body, &sel); err != nil {
		return err
	}
	if (sel.Builtin == "") == (sel.SHA256 == "") {
		return badRequest("give exactly one of builtin and sha256")
	}
	if sel.SHA256 != "" && !themeSHARe.MatchString(sel.SHA256) {
		return badRequest("sha256 must be 64 lowercase hex digits")
	}
	target := sel.SHA256
	if target == "" {
		target = "builtin:" + strings.TrimSpace(sel.Builtin)
	}
	c.audited("theme.activate", target, nil)
	if err := ta.Activate(c.ctx(), sel, themeActor(c)); err != nil {
		return themeProblem(err)
	}
	st, err := ta.Themes(c.ctx())
	if err != nil {
		return themeProblem(err)
	}
	return c.json(http.StatusOK, st.listed())
}

func (s *Server) hThemeReset(c *call) error {
	ta, err := s.themeAdmin()
	if err != nil {
		return err
	}
	c.audited("theme.reset", "active", nil)
	if err := ta.Reset(c.ctx(), themeActor(c)); err != nil {
		return themeProblem(err)
	}
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) hThemeDelete(c *call) error {
	ta, err := s.themeAdmin()
	if err != nil {
		return err
	}
	sha := c.r.PathValue("sha256")
	c.audited("theme.delete", sha, nil)
	if !themeSHARe.MatchString(sha) {
		return badRequest("sha256 must be 64 lowercase hex digits")
	}
	if err := ta.Delete(c.ctx(), sha); err != nil {
		return themeProblem(err)
	}
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}
