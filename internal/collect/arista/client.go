package arista

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxBody bounds what one eAPI response may weigh. A MAC table of a large
// switch is a few megabytes; anything beyond this is not a switch answering.
const maxBody = 64 << 20

// errAuth is returned when the switch refuses the credentials. It is the one
// failure that is never retried command by command.
var errAuth = errors.New("authentication refused by the switch")

// eapiError is a JSON-RPC error answered by eAPI: the request reached the
// switch and was understood, but a command was refused (unknown on this EOS
// train, not allowed by the role, ...). eAPI fails a whole request on the
// first bad command, which is why the client falls back to one command per
// request.
type eapiError struct {
	Code    int
	Message string
}

func (e *eapiError) Error() string { return fmt.Sprintf("eAPI error %d: %s", e.Code, e.Message) }

type rpcRequest struct {
	JSONRPC string    `json:"jsonrpc"`
	Method  string    `json:"method"`
	Params  rpcParams `json:"params"`
	ID      string    `json:"id"`
}

type rpcParams struct {
	Version int      `json:"version"`
	Cmds    []string `json:"cmds"`
	Format  string   `json:"format"`
}

type rpcResponse struct {
	Result []json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// client speaks eAPI (JSON-RPC 2.0 over HTTP POST on /command-api). Every
// command it sends must be a "show" command: run refuses anything else before
// it leaves the process, and the network-operator role on the switch refuses
// it again.
type client struct {
	http     *http.Client
	endpoint string
	user     string
	pass     string
}

// endpointURL builds the eAPI URL from a target address. "192.0.2.11" and
// "sw-a1:8443" get https (credentials never travel in clear by default);
// "http://192.0.2.11" is honoured for old EOS that cannot negotiate modern
// TLS. A path in the address replaces /command-api.
func endpointURL(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", errors.New("target has no address")
	}
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	u, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("address %q: %w", address, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("address %q: scheme must be http or https", address)
	}
	if u.Host == "" {
		return "", fmt.Errorf("address %q: no host", address)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/command-api"
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u.String(), nil
}

// isShow reports whether cmd is a read-only show command.
func isShow(cmd string) bool { return strings.HasPrefix(cmd, "show ") }

// run sends cmds in one request and returns one raw result per command.
func (c *client) run(ctx context.Context, cmds []string) ([]json.RawMessage, error) {
	for _, cmd := range cmds {
		if !isShow(cmd) {
			return nil, fmt.Errorf("refusing to send %q: a collector only reads", cmd)
		}
	}
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0", Method: "runCmds", ID: "trica",
		Params: rpcParams{Version: 1, Cmds: cmds, Format: "json"},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.pass)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eAPI request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, errAuth
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("eAPI: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("eAPI response: %w", err)
	}
	var out rpcResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("eAPI: malformed response: %w", err)
	}
	if out.Error != nil {
		return nil, &eapiError{Code: out.Error.Code, Message: out.Error.Message}
	}
	if len(out.Result) != len(cmds) {
		return nil, fmt.Errorf("eAPI: %d results for %d commands", len(out.Result), len(cmds))
	}
	return out.Result, nil
}

// runGroup runs cmds as one request. When eAPI refuses the request because of
// one command, it retries them one per request, so that one unsupported
// command loses only its own data. It returns the results that were obtained
// and the error of each command that was not. A non-nil fatal error means the
// switch cannot be used at all (credentials refused).
func (c *client) runGroup(ctx context.Context, cmds []string) (results map[string]json.RawMessage, failed map[string]error, fatal error) {
	results = map[string]json.RawMessage{}
	failed = map[string]error{}
	res, err := c.run(ctx, cmds)
	if err == nil {
		for i, cmd := range cmds {
			results[cmd] = res[i]
		}
		return results, failed, nil
	}
	if errors.Is(err, errAuth) {
		return nil, nil, err
	}
	var ee *eapiError
	if !errors.As(err, &ee) {
		// Transport trouble or an unreadable answer: asking again command by
		// command would only multiply the wait.
		for _, cmd := range cmds {
			failed[cmd] = err
		}
		return results, failed, nil
	}
	for _, cmd := range cmds {
		res, err := c.run(ctx, []string{cmd})
		if errors.Is(err, errAuth) {
			return nil, nil, err
		}
		if err != nil {
			failed[cmd] = err
			continue
		}
		results[cmd] = res[0]
	}
	return results, failed, nil
}
