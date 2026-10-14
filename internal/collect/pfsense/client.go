package pfsense

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxBody bounds a response. A firewall with thousands of rules stays far
// below it; anything larger is not an API answer.
const maxBody = 32 << 20

// envelope is the answer shape of every pfrest v2 endpoint.
type envelope struct {
	Code       int             `json:"code"`
	Status     string          `json:"status"`
	ResponseID string          `json:"response_id"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
}

// errKind classifies a failed request, because the collector reacts
// differently to each: auth refusals end the run, a 404 is a version gap.
type errKind int

const (
	errTransport errKind = iota // network, TLS, timeout, cancellation
	errAuth                     // 401: the key is unknown or auth method disabled
	errForbidden                // 403: the key's user lacks the privilege
	errNotFound                 // 404: the endpoint does not exist on this install
	errServer                   // any other non-2xx status or envelope code
	errMalformed                // the body is not a pfrest envelope
)

// requestError is the error of one GET. It never carries the API key.
type requestError struct {
	Kind   errKind
	Path   string
	Status int
	Msg    string
	Err    error
}

func (e *requestError) Error() string {
	var b strings.Builder
	b.WriteString("GET " + e.Path + ": ")
	switch e.Kind {
	case errTransport:
		b.WriteString("transport error")
	case errAuth:
		b.WriteString("authentication refused (HTTP 401)")
	case errForbidden:
		b.WriteString("forbidden (HTTP 403): the key's user lacks the read privilege for this endpoint")
	case errNotFound:
		b.WriteString("not found (HTTP 404): endpoint missing on this REST API version")
	case errServer:
		fmt.Fprintf(&b, "HTTP %d", e.Status)
	case errMalformed:
		b.WriteString("malformed response (is the REST API package installed and reachable at this path?)")
	}
	if e.Msg != "" {
		b.WriteString(": " + e.Msg)
	}
	if e.Err != nil {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

func (e *requestError) Unwrap() error { return e.Err }

// kindOf returns the classification of err, errTransport for foreign errors.
func kindOf(err error) errKind {
	var re *requestError
	if errors.As(err, &re) {
		return re.Kind
	}
	return errTransport
}

// client performs read-only calls on the REST API. It has no method that
// sends anything but GET: the collector cannot write to the firewall.
type client struct {
	hc   *http.Client
	base *url.URL // .../api/v2
	key  string
}

// newClient copies hc to refuse redirects: pfSense answers a missing API
// with a redirect to the login page, and the key must never follow a
// redirect to another origin.
func newClient(hc *http.Client, base *url.URL, key string) *client {
	cp := *hc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client{hc: &cp, base: base, key: key}
}

// get reads one endpoint and returns the envelope's data.
func (c *client) get(ctx context.Context, endpoint string) (json.RawMessage, error) {
	u := c.base.JoinPath(endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, &requestError{Kind: errTransport, Path: endpoint, Err: err}
	}
	req.Header.Set("X-API-Key", c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, &requestError{Kind: errTransport, Path: endpoint, Err: scrub(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, &requestError{Kind: errTransport, Path: endpoint, Err: err}
	}
	if len(body) > maxBody {
		return nil, &requestError{Kind: errMalformed, Path: endpoint, Msg: "response too large"}
	}
	var env envelope
	jsonErr := json.Unmarshal(body, &env)
	msg := ""
	if jsonErr == nil {
		msg = clip(env.Message)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, &requestError{Kind: errAuth, Path: endpoint, Status: resp.StatusCode, Msg: msg}
	case resp.StatusCode == http.StatusForbidden:
		return nil, &requestError{Kind: errForbidden, Path: endpoint, Status: resp.StatusCode, Msg: msg}
	case resp.StatusCode == http.StatusNotFound:
		return nil, &requestError{Kind: errNotFound, Path: endpoint, Status: resp.StatusCode, Msg: msg}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, &requestError{Kind: errServer, Path: endpoint, Status: resp.StatusCode, Msg: msg}
	}
	if jsonErr != nil {
		return nil, &requestError{Kind: errMalformed, Path: endpoint, Err: jsonErr}
	}
	if env.Code >= 400 {
		// A 200 transport status wrapping an error code: believe the code.
		return nil, &requestError{Kind: errServer, Path: endpoint, Status: env.Code, Msg: msg}
	}
	return env.Data, nil
}

// scrub removes the URL from a transport error, which could echo query
// strings; the endpoint is already in the message.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
