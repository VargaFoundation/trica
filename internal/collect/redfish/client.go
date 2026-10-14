package redfish

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

// maxBody bounds what one response may weigh. An IML page or a firmware
// inventory is a few hundred kilobytes; anything past this is not Redfish.
const maxBody = 16 << 20

// client performs the GET requests of one run. It is deliberately minimal:
// there is no method parameter, so no code path can send anything but a read.
// Credentials travel as HTTP Basic on every request; a Redfish session would
// need a POST, and collectors never write to the equipment.
type client struct {
	base string
	http *http.Client
	user string
	pass string
}

// httpError is a non-2xx answer. Auth reports a refused credential, the
// distinction the collector uses to tell "wrong account" from "a resource that
// is not there".
type httpError struct {
	Path    string
	Status  int
	Message string
}

func (e *httpError) Error() string {
	msg := fmt.Sprintf("GET %s: HTTP %d", e.Path, e.Status)
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Auth reports whether the equipment refused the credentials or the
// privileges of the account.
func (e *httpError) Auth() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// baseURL turns the target address into a scheme://host[:port] prefix. A bare
// host or host:port means HTTPS, the only transport BMCs offer for Redfish.
func baseURL(address string) (string, error) {
	a := strings.TrimSpace(address)
	if a == "" {
		return "", errors.New("target has no address")
	}
	if !strings.Contains(a, "://") {
		a = "https://" + a
	}
	u, err := url.Parse(a)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("target address %q is not a host or URL", address)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("target address %q: scheme must be https", address)
	}
	return u.Scheme + "://" + u.Host, nil
}

// get reads path (a Redfish @odata.id, with its query when it is a paging
// link) and decodes the JSON body into out.
func (c *client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+normalizePath(path), nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OData-Version", "4.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, redactURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("GET %s: reading body: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &httpError{Path: path, Status: resp.StatusCode, Message: errorMessage(body)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: malformed JSON: %w", path, err)
	}
	return nil
}

// redactURL keeps the transport error but drops the URL net/http wraps it in:
// the path is already named by the caller.
func redactURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// normalizePath accepts an @odata.id that some firmware returns as an
// absolute URL and keeps only the path and query.
func normalizePath(p string) string {
	if strings.Contains(p, "://") {
		if u, err := url.Parse(p); err == nil {
			p = u.RequestURI()
		}
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// errorMessage extracts the human message of a Redfish error body, which the
// operator needs to tell "invalid credentials" from "privilege missing".
func errorMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Info    []struct {
				Message string `json:"Message"`
			} `json:"@Message.ExtendedInfo"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	msg := strings.TrimSpace(e.Error.Message)
	if len(e.Error.Info) > 0 && strings.TrimSpace(e.Error.Info[0].Message) != "" {
		msg = strings.TrimSpace(e.Error.Info[0].Message)
	}
	return truncate(msg, 200)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
