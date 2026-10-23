package unifi

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

// maxBody bounds a response. A page of a large account stays far below it.
const maxBody = 32 << 20

// maxPages bounds the pages followed for one list: a server that never stops
// returning a next token must not keep a run alive.
const maxPages = 50

// envelope is the answer shape of every Site Manager endpoint, errors included.
type envelope struct {
	Data           json.RawMessage `json:"data"`
	HTTPStatusCode int             `json:"httpStatusCode"`
	NextToken      str             `json:"nextToken"`
	Code           str             `json:"code"`
	Message        str             `json:"message"`
}

// errKind classifies a failed request, because the collector reacts
// differently to each.
type errKind int

const (
	errTransport   errKind = iota // network, TLS, timeout, cancellation
	errAuth                       // 401: the key is unknown, revoked or expired
	errForbidden                  // 403: the key's account may not read this
	errNotFound                   // 404
	errRateLimited                // 429
	errServer                     // any other non-2xx status
	errMalformed                  // the body is not a Site Manager envelope
)

// requestError is the error of one GET. It never carries the API key.
type requestError struct {
	Kind   errKind
	Path   string
	Status int
	Msg    string
	Retry  string // Retry-After of a 429
	Err    error
}

func (e *requestError) Error() string {
	var b strings.Builder
	b.WriteString("GET " + e.Path + ": ")
	switch e.Kind {
	case errTransport:
		b.WriteString("transport error")
	case errAuth:
		b.WriteString("authentication refused (HTTP 401): the API key is unknown, revoked or expired")
	case errForbidden:
		b.WriteString("forbidden (HTTP 403): the key's account may not read this")
	case errNotFound:
		b.WriteString("not found (HTTP 404)")
	case errRateLimited:
		b.WriteString("rate limited (HTTP 429)")
		if e.Retry != "" {
			b.WriteString(", retry after " + e.Retry)
		}
	case errServer:
		fmt.Fprintf(&b, "HTTP %d", e.Status)
	case errMalformed:
		b.WriteString("malformed response (is this the Site Manager API?)")
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

// client performs read-only calls on the Site Manager API. It has no method
// that sends anything but GET: the collector cannot write to the account.
type client struct {
	hc   *http.Client
	base *url.URL
	key  string
}

// newClient copies hc to refuse redirects: the key is sent in a header and
// must never follow a redirect to another origin.
func newClient(hc *http.Client, base *url.URL, key string) *client {
	cp := *hc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client{hc: &cp, base: base, key: key}
}

// get performs one GET and returns the decoded envelope. It is the only place
// of the package that builds a request, and the key only ever goes in the
// X-API-KEY header.
func (c *client) get(ctx context.Context, endpoint string, q url.Values) (*envelope, error) {
	u := c.base.JoinPath(endpoint)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, &requestError{Kind: errTransport, Path: endpoint, Err: c.scrub(err)}
	}
	req.Header.Set("X-API-KEY", c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, &requestError{Kind: errTransport, Path: endpoint, Err: c.scrub(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, &requestError{Kind: errTransport, Path: endpoint, Err: c.scrub(err)}
	}
	if len(body) > maxBody {
		return nil, &requestError{Kind: errMalformed, Path: endpoint, Msg: "response too large"}
	}
	var env envelope
	jsonErr := json.Unmarshal(body, &env)
	msg := ""
	if jsonErr == nil {
		msg = c.clip(string(env.Message))
	}
	re := func(k errKind) error {
		return &requestError{Kind: k, Path: endpoint, Status: resp.StatusCode, Msg: msg, Retry: c.clip(resp.Header.Get("Retry-After"))}
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, re(errAuth)
	case resp.StatusCode == http.StatusForbidden:
		return nil, re(errForbidden)
	case resp.StatusCode == http.StatusNotFound:
		return nil, re(errNotFound)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, re(errRateLimited)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, re(errServer)
	}
	if jsonErr != nil {
		return nil, &requestError{Kind: errMalformed, Path: endpoint, Err: c.scrub(jsonErr)}
	}
	return &env, nil
}

// list reads every page of a list endpoint, following nextToken, and hands
// each page's data to each. A token seen twice, or more than maxPages pages,
// ends the call with an error: the server is not paging.
func (c *client) list(ctx context.Context, endpoint string, q url.Values, each func(json.RawMessage) error) error {
	q = cloneValues(q)
	seen := map[string]bool{}
	for page := 0; ; page++ {
		if page >= maxPages {
			return &requestError{Kind: errMalformed, Path: endpoint, Msg: fmt.Sprintf("more than %d pages", maxPages)}
		}
		env, err := c.get(ctx, endpoint, q)
		if err != nil {
			return err
		}
		if err := each(env.Data); err != nil {
			return &requestError{Kind: errMalformed, Path: endpoint, Err: c.scrub(err)}
		}
		tok := string(env.NextToken)
		if tok == "" {
			return nil
		}
		if seen[tok] {
			return &requestError{Kind: errMalformed, Path: endpoint, Msg: "the same nextToken came back: the server is not paging"}
		}
		seen[tok] = true
		q.Set("nextToken", tok)
	}
}

// listAll decodes every page of endpoint into one slice.
func listAll[T any](ctx context.Context, c *client, endpoint string, q url.Values) ([]T, error) {
	var out []T
	err := c.list(ctx, endpoint, q, func(data json.RawMessage) error {
		if len(data) == 0 || string(data) == "null" {
			return nil
		}
		var page []T
		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}
		out = append(out, page...)
		return nil
	})
	return out, err
}

func cloneValues(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// scrub removes the URL from a transport error (it could echo a query string)
// and the key from any text that might carry it.
func (c *client) scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if c.key != "" && strings.Contains(err.Error(), c.key) {
		return errors.New(strings.ReplaceAll(err.Error(), c.key, "[redacted]"))
	}
	return err
}

// clip shortens a server message for a health record and removes the key
// from it, should a server ever echo it.
func (c *client) clip(s string) string {
	s = strings.TrimSpace(s)
	if c.key != "" {
		s = strings.ReplaceAll(s, c.key, "[redacted]")
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
