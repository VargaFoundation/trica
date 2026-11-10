package kubemetrics

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// response is the envelope of /api/v1/query. Only the fields the collector
// reads are decoded: the stats VictoriaMetrics adds are ignored.
type response struct {
	Status    string   `json:"status"`
	ErrorType string   `json:"errorType"`
	Error     string   `json:"error"`
	IsPartial bool     `json:"isPartial"`
	Warnings  []string `json:"warnings"`
	Data      struct {
		ResultType string   `json:"resultType"`
		Result     []sample `json:"result"`
	} `json:"data"`
}

// sample is one series of an instant vector.
type sample struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
}

// isOne reports whether the sample's value is 1: for kube_pod_status_phase,
// the series of the pod's current phase.
func (s sample) isOne() bool {
	if len(s.Value) != 2 {
		return false
	}
	var v string
	if err := json.Unmarshal(s.Value[1], &v); err != nil {
		return false
	}
	return strings.TrimSpace(v) == "1"
}

// number returns the sample's value as a number.
func (s sample) number() (float64, bool) {
	if len(s.Value) != 2 {
		return 0, false
	}
	var v string
	if err := json.Unmarshal(s.Value[1], &v); err != nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, false
	}
	return f, true
}

// label returns a label value, trimmed.
func (s sample) label(k string) string { return strings.TrimSpace(s.Metric[k]) }
