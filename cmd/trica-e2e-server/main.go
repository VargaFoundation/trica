// Command trica-e2e-server is the test double the browser tests run against
// (web/e2e, Playwright). It is not part of the product and is never built into
// the release image.
//
// It serves three things over one self-signed TLS origin, because the page's
// OIDC code refuses anything but https:
//
//   - the real web UI, through the real internal/web handler (so the real
//     Content-Security-Policy, security headers, SPA fallback and
//     /ui-config.json), embedded with -tags webui;
//   - /api/v1, a mock of the trica API over a small anonymised inventory
//     (documentation addresses, invented names), answering 401 unless the
//     bearer token is one it issued; batches are previewed and "queued" but
//     never applied, so the inventory never changes;
//   - /idp, a fake OpenID provider: discovery, authorisation (redirects at
//     once, no login form), and a token endpoint that verifies the PKCE S256
//     verifier against the challenge it saw, as a real provider does.
//
// Nothing here writes anywhere: the mock API answers batches without keeping them.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/VargaFoundation/trica/internal/web"
)

const (
	pastedToken = "e2e-api-token" //nolint:gosec // a test double's fixed token, not a credential
	clientID    = "trica-ui-e2e"
	redirectTo  = "/oidc/callback"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18443", "listen address (https)")
	flag.Parse()
	if err := run(*addr); err != nil {
		slog.Error("e2e server", "error", err)
		os.Exit(1)
	}
}

func run(addr string) error {
	if !web.Built {
		return fmt.Errorf("the real UI is not embedded: build it (make web-embed) and run with -tags webui")
	}
	issuer := "https://" + addr + "/idp"
	ui, err := web.DefaultWithConfig(web.Config{OIDC: &web.OIDC{Issuer: issuer, ClientID: clientID}})
	if err != nil {
		return err
	}
	idp := newIDP(issuer)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", newAPI(idp.valid)))
	mux.Handle("/idp/", idp)
	mux.Handle("/", ui)

	cert, err := selfSigned()
	if err != nil {
		return err
	}
	ln, err := tls.Listen("tcp", addr, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	// The browser probes the self-signed certificate before it accepts it: those handshake errors are noise.
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	slog.Info("e2e server listening", "addr", addr, "issuer", issuer)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "trica-e2e"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// ---- fake identity provider ----------------------------------------------

type grant struct {
	challenge string
	nonce     string
	redirect  string
	client    string
}

type idp struct {
	issuer string
	mu     sync.Mutex
	codes  map[string]grant // authorisation codes, single use
	tokens map[string]bool  // access tokens issued
}

func newIDP(issuer string) *idp {
	return &idp{issuer: issuer, codes: map[string]grant{}, tokens: map[string]bool{}}
}

func randomString() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (p *idp) valid(token string) bool {
	if token == pastedToken {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tokens[token]
}

func (p *idp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.URL.Path, "/idp") {
	case "/.well-known/openid-configuration":
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                           p.issuer,
			"authorization_endpoint":           p.issuer + "/authorize",
			"token_endpoint":                   p.issuer + "/token",
			"code_challenge_methods_supported": []string{"S256"},
		})
	case "/authorize":
		p.authorize(w, r)
	case "/token":
		p.token(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (p *idp) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	u, err := url.Parse(redirect)
	if err != nil || q.Get("client_id") != clientID || u.Scheme != "https" || u.Host != r.Host || u.Path != redirectTo {
		http.Error(w, "unknown client or redirect_uri", http.StatusBadRequest)
		return
	}
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "PKCE S256 is required", http.StatusBadRequest)
		return
	}
	code := randomString()
	p.mu.Lock()
	p.codes[code] = grant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirect: redirect, client: clientID}
	p.mu.Unlock()
	back := url.Values{"code": {code}, "state": {q.Get("state")}}
	http.Redirect(w, r, redirect+"?"+back.Encode(), http.StatusFound) //nolint:gosec // redirect_uri is checked above: this origin, the one registered path
}

func oauthError(w http.ResponseWriter, desc string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": desc})
}

func (p *idp) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		oauthError(w, "unreadable form")
		return
	}
	if r.PostForm.Get("client_secret") != "" {
		oauthError(w, "a public client sends no client_secret")
		return
	}
	p.mu.Lock()
	g, ok := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	p.mu.Unlock()
	if !ok || r.PostForm.Get("grant_type") != "authorization_code" {
		oauthError(w, "unknown or used code")
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		oauthError(w, "PKCE verification failed")
		return
	}
	if r.PostForm.Get("redirect_uri") != g.redirect || r.PostForm.Get("client_id") != g.client {
		oauthError(w, "redirect_uri or client_id differ from the authorisation request")
		return
	}
	access := "at-" + randomString()
	p.mu.Lock()
	p.tokens[access] = true
	p.mu.Unlock()
	claims, _ := json.Marshal(map[string]any{"iss": p.issuer, "sub": "e2e-user", "aud": clientID, "nonce": g.nonce})
	idToken := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln" // unsigned: the API, not the page, verifies tokens
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "id_token": idToken, "token_type": "Bearer", "expires_in": 300})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
