package kubemetrics

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

// queryPath is the one path the client calls, below the address's own path.
const queryPath = "api/v1/query"

// maxBody bounds a response: a cluster of a few thousand pods is a few MiB.
const maxBody = 64 << 20

// The metric families the collector reads, and no others (ADR 0070).
const (
	metricNodeInfo = "kube_node_info"
	metricPodInfo  = "kube_pod_info"
	metricPodOwner = "kube_pod_owner"
	metricRSOwner  = "kube_replicaset_owner"
	metricJobOwner = "kube_job_owner"
	metricPodPhase = "kube_pod_status_phase"
)

// allowedMetrics is the read allow-list: the client refuses, before sending,
// any other metric, whatever a future change to the callers asks for. These
// series carry placement (which pod on which node, owned by what) and no
// environment value, manifest or container argument.
var allowedMetrics = []string{metricNodeInfo, metricPodInfo, metricPodOwner, metricRSOwner, metricJobOwner, metricPodPhase}

// errRefused is the error of a query that is not on the allow-list.
var errRefused = errors.New("refused: not on the read-only allow-list")

func isAllowed(metric string) bool {
	for _, m := range allowedMetrics {
		if m == metric {
			return true
		}
	}
	return false
}

// client performs the instant queries of one run: GET <base>/api/v1/query,
// the PromQL text in the URL's query string (it holds no secret), the account
// in an Authorization header.
type client struct {
	hc       *http.Client
	base     *url.URL
	username string
	passwd   string
	selector string // canonical matchers, no braces
}

// newClient copies hc to refuse redirects: the Authorization header must never
// follow a redirect to another origin.
func newClient(hc *http.Client, base *url.URL, username, passwd, selector string) *client {
	cp := *hc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client{hc: &cp, base: base, username: username, passwd: passwd, selector: selector}
}

// promql is the expression sent for a metric.
func (c *client) promql(metric string) string {
	if c.selector == "" {
		return metric
	}
	return metric + "{" + c.selector + "}"
}

// result is the vector a query returned.
type result struct {
	Series  []sample
	Partial bool
}

// query sends one allow-listed instant query.
func (c *client) query(ctx context.Context, metric string) (*result, error) {
	if !isAllowed(metric) {
		return nil, fmt.Errorf("%s: %w", metric, errRefused)
	}
	u := c.base.JoinPath(queryPath)
	u.RawQuery = url.Values{"query": {c.promql(metric)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", metric, c.scrub(err))
	}
	req.SetBasicAuth(c.username, c.passwd)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: transport error: %w", metric, c.scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%s: transport error: %w", metric, c.scrub(err))
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("%s: response too large", metric)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("%s: HTTP 401: the username or password is refused", metric)
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%s: HTTP 403: the account may not run this query", metric)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%s: HTTP 429: the server is rate-limiting this account", metric)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("%s: HTTP %d (is this a Prometheus-compatible query API?)", metric, resp.StatusCode)
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("%s: malformed response (is this a Prometheus-compatible query API?)", metric)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("%s: query failed: %s: %s", metric, r.ErrorType, clip(r.Error))
	}
	if r.Data.ResultType != "vector" {
		return nil, fmt.Errorf("%s: result type %q, want vector", metric, r.Data.ResultType)
	}
	return &result{Series: r.Data.Result, Partial: r.IsPartial}, nil
}

// clip bounds a server-provided message.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// scrub removes the URL from a transport error and the password from any text
// that might carry it.
func (c *client) scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	if c.passwd != "" && strings.Contains(msg, c.passwd) {
		return errors.New(strings.ReplaceAll(msg, c.passwd, "[redacted]"))
	}
	return err
}
