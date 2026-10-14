package maas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// apiPath is the root of the MAAS REST API 2.0 on a region controller.
const apiPath = "/MAAS/api/2.0/"

// maxBody bounds one response. A machine list of a few thousand machines is
// tens of megabytes, hence the generous limit.
const maxBody = 128 << 20

// ErrAuth is returned when MAAS refuses the API key (HTTP 401).
var ErrAuth = errors.New("maas: authentication refused")

// errTransport marks a failure to get any HTTP answer (connection refused,
// DNS, TLS, timeout).
var errTransport = errors.New("maas: request failed")

// HTTPError is a non-success answer from MAAS other than 401.
type HTTPError struct {
	Endpoint string
	Status   int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("maas: %s: HTTP %d", e.Endpoint, e.Status)
}

// credentials are the three parts of a MAAS API key. MAAS uses OAuth 1.0a with
// an empty consumer secret, so the key is "consumer_key:token_key:token_secret".
type credentials struct {
	consumerKey string
	tokenKey    string
	tokenSecret string
}

// parseAPIKey splits an API key. The error never contains the key itself.
func parseAPIKey(key string) (credentials, error) {
	parts := strings.Split(strings.TrimSpace(key), ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return credentials{}, errors.New("malformed MAAS API key (expected consumer_key:token_key:token_secret)")
	}
	return credentials{consumerKey: parts[0], tokenKey: parts[1], tokenSecret: parts[2]}, nil
}

// pctEncode is the RFC 3986 percent-encoding RFC 5849 asks for in the header.
func pctEncode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}

// authHeader builds the OAuth 1.0a Authorization header with the PLAINTEXT
// signature method: the signature is "<consumer secret>&<token secret>" and the
// consumer secret of MAAS is empty. The header holds the token secret: callers
// must never log it.
func (c credentials) authHeader(nonce string, now time.Time) string {
	return `OAuth oauth_version="1.0", oauth_signature_method="PLAINTEXT"` +
		`, oauth_consumer_key="` + pctEncode(c.consumerKey) + `"` +
		`, oauth_token="` + pctEncode(c.tokenKey) + `"` +
		`, oauth_signature="&` + pctEncode(c.tokenSecret) + `"` +
		`, oauth_nonce="` + pctEncode(nonce) + `"` +
		`, oauth_timestamp="` + strconv.FormatInt(now.Unix(), 10) + `"`
}

// newNonce returns a fresh random nonce. MAAS rejects a replayed nonce.
func newNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// client is a read-only MAAS API client: it only ever issues GET requests.
type client struct {
	base  string
	http  *http.Client
	creds credentials
	now   func() time.Time
}

func newClient(address string, hc *http.Client, creds credentials) *client {
	// A redirect would replay the same OAuth nonce, which MAAS refuses, and
	// could send the credentials elsewhere: report it instead of following it.
	cp := *hc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client{
		base:  strings.TrimRight(address, "/") + apiPath,
		http:  &cp,
		creds: creds,
		now:   time.Now,
	}
}

// get performs GET <base><endpoint>?<query> and decodes the JSON answer into
// out. endpoint is relative to the API root ("machines/").
func (c *client) get(ctx context.Context, endpoint string, query url.Values, out any) error {
	target := c.base + endpoint
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return fmt.Errorf("maas: %s: %w", endpoint, err)
	}
	nonce, err := newNonce()
	if err != nil {
		return fmt.Errorf("maas: nonce: %w", err)
	}
	req.Header.Set("Authorization", c.creds.authHeader(nonce, c.now()))
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// The *url.Error text holds the URL, which has no secret in it.
		return fmt.Errorf("%w: %s: %w", errTransport, endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%w (%s: HTTP 401)", ErrAuth, endpoint)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &HTTPError{Endpoint: endpoint, Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("%w: %s: reading body: %w", errTransport, endpoint, err)
	}
	if len(body) > maxBody {
		return fmt.Errorf("maas: %s: response larger than %d bytes", endpoint, maxBody)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("maas: %s: malformed response: %w", endpoint, err)
	}
	return nil
}

// fatal reports whether err means the whole run is pointless: no answer at all
// or credentials refused. Any other error leaves the other requests worth
// trying.
func fatal(err error) bool {
	return errors.Is(err, ErrAuth) || errors.Is(err, errTransport)
}
