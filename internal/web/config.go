package web

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Bearer values: which token of the authorisation-code exchange the page
// sends to the API. The server verifies either (ADR 0013); the choice depends
// on which one carries the audience the API is configured to accept.
const (
	BearerAccessToken = "access_token"
	BearerIDToken     = "id_token"
)

// Config is what the page learns at start-up from GET /ui-config.json.
// Nothing in it is secret: the browser is a public OIDC client (ADR 0019).
type Config struct {
	// OIDC enables "Sign in with SSO" (authorisation code with PKCE). Nil
	// leaves the pasted API token as the only way in.
	OIDC *OIDC `json:"oidc"`
	// Theme is the look in force (ADR 0054). Nil serves the application as
	// built: no link, no /theme. Never sent to the page.
	Theme ThemeProvider `json:"-"`
}

// OIDC describes the public client the page signs in with.
type OIDC struct {
	// Issuer is the exact `iss` of the provider, an https URL. The page reads
	// <Issuer>/.well-known/openid-configuration itself, and the
	// Content-Security-Policy lets it talk to this origin and no other.
	Issuer string `json:"issuer"`
	// ClientID is the id of the public client registered for the UI. There is
	// no client secret: a secret in a browser is not a secret.
	ClientID string `json:"client_id"`
	// Scopes requested; "openid profile email" when empty. "openid" is always
	// added.
	Scopes []string `json:"scopes"`
	// Bearer is the token sent to the API: BearerAccessToken (default) or
	// BearerIDToken.
	Bearer string `json:"bearer"`
}

// normalised validates the configuration and fills the defaults.
func (c Config) normalised() (Config, error) {
	if c.OIDC == nil {
		return c, nil
	}
	o := *c.OIDC
	u, err := url.Parse(o.Issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("web: oidc issuer %q must be an https URL without credentials, query or fragment", o.Issuer)
	}
	if strings.TrimSpace(o.ClientID) == "" {
		return c, errors.New("web: oidc client_id is required")
	}
	if len(o.Scopes) == 0 {
		o.Scopes = []string{"profile", "email"}
	}
	for _, s := range o.Scopes {
		if s == "" || strings.ContainsAny(s, " \t\r\n\"") {
			return c, fmt.Errorf("web: oidc scope %q is not a scope token", s)
		}
	}
	if !slices.Contains(o.Scopes, "openid") {
		o.Scopes = append([]string{"openid"}, o.Scopes...)
	}
	switch o.Bearer {
	case "":
		o.Bearer = BearerAccessToken
	case BearerAccessToken, BearerIDToken:
	default:
		return c, fmt.Errorf("web: oidc bearer %q must be %q or %q", o.Bearer, BearerAccessToken, BearerIDToken)
	}
	c.OIDC = &o
	return c, nil
}

// csp is the Content-Security-Policy for this configuration: the default
// policy, plus the issuer's origin in connect-src when single sign-on is on
// (the page fetches the discovery document and exchanges the code there).
func (c Config) csp() string {
	if c.OIDC == nil {
		return ContentSecurityPolicy
	}
	u, _ := url.Parse(c.OIDC.Issuer) // validated by normalised
	return strings.Replace(ContentSecurityPolicy, "connect-src 'self'", "connect-src 'self' "+u.Scheme+"://"+u.Host, 1)
}
