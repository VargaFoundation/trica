package synology

import (
	"context"
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

// webAPIPath is the one path the client posts to.
const webAPIPath = "/webapi/entry.cgi"

// sessionName is the DSM session the collector opens and closes every run.
const sessionName = "trica"

// maxBody bounds a response; the largest one (the storage) is tens of KiB.
const maxBody = 32 << 20

// logoutTimeout bounds the logout, which runs even when the run's own
// deadline has passed or the run failed.
const logoutTimeout = 10 * time.Second

// endpoint is one DSM Web API call: the API, its version and the method.
type endpoint struct {
	API     string
	Version int
	Method  string
}

func (e endpoint) String() string { return e.API + " " + e.Method }

// The calls of the collector. Nothing else can be sent: see allowed.
var (
	epLogin    = endpoint{"SYNO.API.Auth", 6, "login"}
	epLogout   = endpoint{"SYNO.API.Auth", 6, "logout"}
	epSystem   = endpoint{"SYNO.Core.System", 1, "info"}
	epDSM      = endpoint{"SYNO.DSM.Info", 2, "getinfo"}
	epStorage  = endpoint{"SYNO.Storage.CGI.Storage", 1, "load_info"}
	epIfaces   = endpoint{"SYNO.Core.Network.Interface", 1, "list"}
	epNetwork  = endpoint{"SYNO.Core.Network", 1, "get"}
	epPackages = endpoint{"SYNO.Core.Package", 2, "list"}
	epUPS      = endpoint{"SYNO.Core.ExternalDevice.UPS", 1, "get"}
	epDocker   = endpoint{"SYNO.Docker.Container", 1, "list"}
)

// allowed is the read-only allow-list: the session calls, and the read calls
// of the mapping. The client refuses, before sending, any other API, version
// or method: the collector cannot reboot, shut down or configure the NAS,
// whatever a future change to the callers asks for.
var allowed = []endpoint{epLogin, epLogout, epSystem, epDSM, epStorage, epIfaces, epNetwork, epPackages, epUPS, epDocker}

// readVerbs are the methods, besides the session ones, an allowed endpoint may
// have. A test checks the allow-list against it.
var readVerbs = map[string]bool{"info": true, "getinfo": true, "load_info": true, "get": true, "list": true, "query": true}

// errRefused is the error of a call that is not on the allow-list.
var errRefused = errors.New("refused: not on the read-only allow-list")

func isAllowed(e endpoint) bool {
	for _, a := range allowed {
		if a == e {
			return true
		}
	}
	return false
}

// apiError is a DSM error envelope ({"success":false,"error":{"code":N}}).
type apiError struct {
	Call endpoint
	Code int
}

func (e *apiError) Error() string {
	msg := fmt.Sprintf("%s: DSM error %d (%s)", e.Call, e.Code, codeText(e.Code))
	switch e.Code {
	case 403, 404:
		msg += ": the account must not require 2-step verification"
	case 105, 402:
		msg += ": DSM answers this API only to an account of the administrators group"
	}
	return msg
}

// codeText names the common DSM error codes.
func codeText(code int) string {
	switch code {
	case 100:
		return "unknown error"
	case 101:
		return "no parameter of API, method or version"
	case 102:
		return "the API does not exist"
	case 103:
		return "the method does not exist"
	case 104:
		return "this API version is not supported"
	case 105:
		return "permission denied"
	case 106:
		return "session timeout"
	case 107:
		return "session interrupted by a duplicate login"
	case 119:
		return "session id not found"
	case 400:
		return "wrong account or password"
	case 401:
		return "account disabled"
	case 402:
		return "permission denied"
	case 403:
		return "2-step verification code required"
	case 404:
		return "2-step verification failed"
	case 407:
		return "IP address blocked"
	}
	return "unrecognised code"
}

// optionalFailure reports whether err is the answer of a DSM that lacks the
// API or does not let the account call it (codes 102 to 105): an optional
// part is then missing, not the run failed.
func optionalFailure(err error) (*apiError, bool) {
	var ae *apiError
	if errors.As(err, &ae) && ae.Code >= 102 && ae.Code <= 105 {
		return ae, true
	}
	return nil, false
}

// client performs the DSM Web API calls of one run. Every call is a POST of a
// form to /webapi/entry.cgi: the account, the password and the session id are
// in the body and never in a URL, so they cannot land in an access log or in
// an error string.
type client struct {
	hc      *http.Client
	base    *url.URL
	account string
	passwd  string
	sid     string
}

// newClient copies hc to refuse redirects: the login body holds the password
// and must never follow a redirect to another origin.
func newClient(hc *http.Client, base *url.URL, account, passwd string) *client {
	cp := *hc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client{hc: &cp, base: base, account: account, passwd: passwd}
}

// call sends one allow-listed call and returns the data of a successful
// envelope. extra are the call's own parameters.
func (c *client) call(ctx context.Context, e endpoint, extra url.Values) (json.RawMessage, error) {
	if !isAllowed(e) {
		return nil, fmt.Errorf("%s: %w", e, errRefused)
	}
	form := url.Values{}
	for k, v := range extra {
		form[k] = v
	}
	form.Set("api", e.API)
	form.Set("version", strconv.Itoa(e.Version))
	form.Set("method", e.Method)
	if e != epLogin && c.sid != "" {
		form.Set("_sid", c.sid)
	}
	u := c.base.JoinPath(webAPIPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e, c.scrub(err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: transport error: %w", e, c.scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%s: transport error: %w", e, c.scrub(err))
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("%s: response too large", e)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s: HTTP %d (is this a DSM web service?)", e, resp.StatusCode)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%s: malformed response (is this a DSM web service?)", e)
	}
	if !env.Success {
		code := 0
		if env.Error != nil {
			if n, ok := env.Error.Code.Int(); ok {
				code = int(n)
			}
		}
		return nil, &apiError{Call: e, Code: code}
	}
	return env.Data, nil
}

// login opens the session. The session id is kept in the client only.
func (c *client) login(ctx context.Context) error {
	data, err := c.call(ctx, epLogin, url.Values{
		"account": {c.account},
		"passwd":  {c.passwd},
		"session": {sessionName},
		"format":  {"sid"},
	})
	if err != nil {
		return err
	}
	var ld loginData
	if err := json.Unmarshal(data, &ld); err != nil || ld.SID == "" {
		return fmt.Errorf("%s: the answer has no session id", epLogin)
	}
	c.sid = string(ld.SID)
	return nil
}

// logout closes the session, with its own short timeout: it must happen when
// the run failed or ran out of time. Nothing is sent without a session.
func (c *client) logout(ctx context.Context) error {
	if c.sid == "" {
		return nil
	}
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logoutTimeout)
	defer cancel()
	_, err := c.call(lctx, epLogout, url.Values{"session": {sessionName}})
	c.sid = ""
	return err
}

// get calls e and decodes its data into T.
func get[T any](ctx context.Context, c *client, e endpoint, extra url.Values) (T, error) {
	var out T
	data, err := c.call(ctx, e, extra)
	if err != nil {
		return out, err
	}
	if len(data) == 0 || string(data) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("%s: malformed data: not the expected shape", e)
	}
	return out, nil
}

// scrub removes the URL from a transport error and the password and session
// id from any text that might carry them.
func (c *client) scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	for _, secret := range []string{c.passwd, c.sid} {
		if secret != "" && strings.Contains(msg, secret) {
			msg = strings.ReplaceAll(msg, secret, "[redacted]")
		}
	}
	if msg != err.Error() {
		return errors.New(msg)
	}
	return err
}
