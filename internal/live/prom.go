package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
)

// maxResponse bounds one query answer: a vector of a few thousand series is
// well under it.
const maxResponse = 32 << 20

// sample is one series of an instant vector.
type sample struct {
	labels map[string]string
	value  float64
}

// querier runs an instant query.
type querier interface {
	query(ctx context.Context, expr string) ([]sample, error)
}

// promClient speaks the Prometheus HTTP API (/api/v1/query), which
// VictoriaMetrics, Thanos and Mimir serve as well.
type promClient struct {
	endpoint string
	hc       *http.Client
	user     string
	password collect.Secret
	bearer   collect.Secret
}

func newPromClient(c *Config) (*promClient, error) {
	t := collect.Target{Kind: "live", Name: "endpoint", TLS: c.TLS, Timeout: c.Timeout}
	hc, err := t.HTTPClient()
	if err != nil {
		return nil, err
	}
	return &promClient{endpoint: strings.TrimRight(c.URL, "/") + "/api/v1/query", hc: hc, user: c.Username, password: c.Password, bearer: c.BearerToken}, nil
}

// errUpstream is an answer of the endpoint that is not a vector.
var errUpstream = errors.New("live: the query endpoint did not answer a vector")

type promAnswer struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func (p *promClient) query(ctx context.Context, expr string) ([]sample, error) {
	form := url.Values{"query": {expr}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	switch {
	case p.user != "":
		pw, err := p.password.Resolve()
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(p.user, pw)
	case p.bearer != "":
		tok, err := p.bearer.Resolve()
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponse {
		return nil, fmt.Errorf("%w: answer over %d bytes", errUpstream, maxResponse)
	}
	var a promAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("%w: HTTP %d, not JSON", errUpstream, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || a.Status != "success" {
		// The error text of the endpoint names the expression, never a
		// credential; it is logged, not returned to API callers.
		return nil, fmt.Errorf("%w: HTTP %d %s %s", errUpstream, resp.StatusCode, a.ErrorType, truncate(a.Error, 200))
	}
	if a.Data.ResultType != "vector" {
		return nil, fmt.Errorf("%w: result type %q", errUpstream, a.Data.ResultType)
	}
	out := make([]sample, 0, len(a.Data.Result))
	for _, r := range a.Data.Result {
		if len(r.Value) != 2 {
			continue
		}
		s, ok := r.Value[1].(string)
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		out = append(out, sample{labels: r.Metric, value: v})
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// selector writes metric{matchers}; names were validated, values are quoted.
func (s *source) selector(metric string) string {
	if len(s.matchers) == 0 {
		return metric
	}
	keys := make([]string, 0, len(s.matchers))
	for k := range s.matchers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + strconv.Quote(s.matchers[k])
	}
	return metric + "{" + strings.Join(parts, ",") + "}"
}

// rateExpr is the only shape of counter query trica sends.
func (s *source) rateExpr(metric string, window time.Duration) string {
	return fmt.Sprintf("rate(%s[%ds])", s.selector(metric), int64(window/time.Second))
}
