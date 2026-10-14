package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/tql"
)

func TestVersionKey(t *testing.T) {
	for in, want := range map[string][]uint64{
		"4.20.7M":                   {4, 20, 7},
		"6.1.0":                     {6, 1, 0},
		"v2.0-rc1":                  {2, 0, 1},
		"":                          {},
		"unknown":                   {},
		"4.9":                       {4, 9},
		"99999999999999999999999.1": {^uint64(0), 1}, // a run too long to fit saturates instead of failing
	} {
		if got := versionKey(in); !reflect.DeepEqual(got, want) {
			t.Errorf("versionKey(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	ref := versionKey("4.30.2")
	for _, tc := range []struct {
		name         string
		current      string
		minor, patch int
		status       string
		level        string
		by           int
	}{
		{"equal", "4.30.2", 0, 0, statusCompliant, "", 0},
		{"ahead in patch", "4.30.5", 0, 0, statusCompliant, "", 0},
		{"ahead in minor", "4.31.0", 0, 0, statusCompliant, "", 0},
		{"ahead in major", "5.0.0", 0, 0, statusCompliant, "", 0},
		{"zero padding", "4.30.2.0", 0, 0, statusCompliant, "", 0},
		{"missing", "", 0, 0, statusUnknown, "", 0},
		{"no digit", "n/a", 0, 0, statusUnknown, "", 0},
		{"major behind", "3.40.9", 5, 5, statusBehind, "major", 1},
		{"minor behind, strict", "4.20.7M", 0, 0, statusBehind, "minor", 10},
		{"minor lag tolerated", "4.29.9", 1, 0, statusWithinTolerance, "minor", 1},
		{"minor lag over tolerance", "4.28.0", 1, 0, statusBehind, "minor", 2},
		{"patch behind, strict", "4.30.1", 0, 0, statusBehind, "patch", 1},
		{"patch lag tolerated", "4.30.1", 0, 1, statusWithinTolerance, "patch", 1},
		{"patch lag over tolerance", "4.30.0", 0, 1, statusBehind, "patch", 2},
		{"a build below the patch counts as one patch", "4.30.2", 0, 0, statusCompliant, "", 0},
		{"the compared version decides, not the text", "4.9", 0, 0, statusBehind, "minor", 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, lag := classify(versionKey(tc.current), ref, tc.minor, tc.patch)
			if status != tc.status {
				t.Fatalf("status %s, want %s", status, tc.status)
			}
			switch {
			case tc.level == "" && lag != nil:
				t.Fatalf("lag %+v, want none", lag)
			case tc.level != "" && (lag == nil || lag.Level != tc.level || lag.By != tc.by):
				t.Fatalf("lag %+v, want %s by %d", lag, tc.level, tc.by)
			}
		})
	}
	// A deeper component below the reference counts as one patch.
	if status, lag := classify(versionKey("1.2.3.4"), versionKey("1.2.3.5"), 0, 0); status != statusBehind || lag.Level != "patch" || lag.By != 1 {
		t.Errorf("1.2.3.4 vs 1.2.3.5: %s %+v", status, lag)
	}
	if status, _ := classify(versionKey("1.2.3.4"), versionKey("1.2.3.5"), 0, 1); status != statusWithinTolerance {
		t.Errorf("1.2.3.4 vs 1.2.3.5 with one patch tolerated: %s", status)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"4.9", "4.20", -1}, {"4.20", "4.9", 1}, {"4.20", "4.20.0", 0}, {"4.20.1", "4.20", 1}, {"1", "1.0.0.0", 0}, {"2", "10", -1},
	} {
		if got := compareVersions(versionKey(tc.a), versionKey(tc.b)); got != tc.want {
			t.Errorf("compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestFilterNode(t *testing.T) {
	ok := func(f Filter) tql.Node {
		t.Helper()
		n, err := f.node()
		if err != nil {
			t.Fatalf("%+v: %v", f, err)
		}
		return n
	}
	if got := ok(Filter{Attribute: "airflow", Op: "eq", Value: "rear_to_front"}); !reflect.DeepEqual(got, tql.Pred{Field: "airflow", Op: tql.OpEq, Values: []string{"rear_to_front"}}) {
		t.Errorf("eq: %#v", got)
	}
	if got := ok(Filter{Attribute: "role", Op: "in", Value: "server, switch ,"}); !reflect.DeepEqual(got, tql.Pred{Field: "role", Op: tql.OpEq, Values: []string{"server", "switch"}}) {
		t.Errorf("in: %#v", got)
	}
	if got := ok(Filter{Attribute: "serial", Op: "missing"}); !reflect.DeepEqual(got, tql.Not{Term: tql.Pred{Field: "has", Op: tql.OpEq, Values: []string{"serial"}}}) {
		t.Errorf("missing: %#v", got)
	}
	// A value is data: it never turns into another condition.
	if got := ok(Filter{Attribute: "name", Op: "eq", Value: `x) OR type:site (`}); !reflect.DeepEqual(got, tql.Pred{Field: "name", Op: tql.OpEq, Values: []string{`x) OR type:site (`}}) {
		t.Errorf("a value was reinterpreted: %#v", got)
	}
	for name, f := range map[string]Filter{
		"secret attribute":   {Attribute: "snmp_community", Op: "eq", Value: "x"},
		"bad attribute name": {Attribute: "name:foo OR type", Op: "eq", Value: "x"},
		"unknown operator":   {Attribute: "role", Op: "like", Value: "x"},
		"missing value":      {Attribute: "role", Op: "eq"},
	} {
		if _, err := f.node(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := (Filter{Attribute: "snmp_community", Op: "exists"}).node(); err == nil {
		t.Error("exists on a secret attribute was accepted")
	}
}

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"24h":                       now.Add(-24 * time.Hour),
		"30m":                       now.Add(-30 * time.Minute),
		"-7d":                       now.Add(-7 * 24 * time.Hour),
		"2w":                        now.Add(-14 * 24 * time.Hour),
		"2026-03-01":                time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		"2026-03-01T10:00:00Z":      time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		"2026-03-01T11:00:00+01:00": time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
	} {
		got, err := parseWhen(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseWhen(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "yesterday", "10 days", "2026-13-01", "999999d", "1y"} {
		if _, err := parseWhen(bad, now); err == nil {
			t.Errorf("parseWhen(%q) was accepted", bad)
		}
	}
}

func TestProposalSource(t *testing.T) {
	for name, tc := range map[string]struct {
		p    api.Principal
		want string
	}{
		"a token bound to an mcp source keeps it":    {api.Principal{Subject: "tok", Kind: api.KindToken, Source: "mcp:claude-code"}, "mcp:claude-code"},
		"a person speaks as mcp:subject":             {api.Principal{Subject: "alice", Kind: api.KindUser}, "mcp:alice"},
		"a collector's token never speaks as itself": {api.Principal{Subject: "col", Kind: api.KindToken, Source: "arista:sw-a1"}, "mcp:col"},
		"a token without source":                     {api.Principal{Subject: "orphan", Kind: api.KindToken}, "mcp:orphan"},
		"a bare mcp: is not a source":                {api.Principal{Subject: "x", Kind: api.KindToken, Source: "mcp:"}, "mcp:x"},
	} {
		if got := proposalSource(tc.p); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestSummarizeArgs(t *testing.T) {
	// A proposal's batch may hold values nobody asked the audit log to keep: only its size is recorded.
	got := summarizeArgs("propose_change", ProposeInput{Reason: "r", Objects: []*ProposedObject{{Ref: "a", Type: "device", Attributes: map[string]any{"role": "server"}}}})
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "server") || !strings.Contains(string(raw), `"objects":1`) {
		t.Errorf("propose_change args audited as %s", raw)
	}
	long := summarizeArgs("search", SearchInput{Query: strings.Repeat("a", 2000)})
	if s, _ := long.(string); len(s) > maxArgsAudited+3 || !strings.HasSuffix(s, "...") {
		t.Errorf("long arguments are not cut: %d bytes", len(s))
	}
}

func TestNewSummary(t *testing.T) {
	if s := newSummary(25, 0, 10, nil); !s.Truncated || s.NextOffset == nil || *s.NextOffset != 10 {
		t.Errorf("first page of 25: %+v", s)
	}
	if s := newSummary(25, 20, 5, nil); s.Truncated || s.NextOffset != nil {
		t.Errorf("last page: %+v", s)
	}
	if s := newSummary(0, 0, 0, nil); s.Truncated {
		t.Errorf("empty: %+v", s)
	}
}

func TestAnd(t *testing.T) {
	q := &tql.Query{}
	and(q)
	if q.Where != nil {
		t.Fatalf("and of nothing: %#v", q.Where)
	}
	a, b := tql.Pred{Field: "role", Op: tql.OpEq, Values: []string{"x"}}, tql.Pred{Field: "vendor", Op: tql.OpEq, Values: []string{"y"}}
	and(q, a)
	if !reflect.DeepEqual(q.Where, a) {
		t.Fatalf("single: %#v", q.Where)
	}
	and(q, b)
	if got, ok := q.Where.(tql.And); !ok || len(got.Terms) != 2 {
		t.Fatalf("both: %#v", q.Where)
	}
}

func TestLagScore(t *testing.T) {
	order := []*Lag{nil, {Level: "patch", By: 1}, {Level: "patch", By: 900}, {Level: "minor", By: 1}, {Level: "minor", By: 1_000_000_004}, {Level: "major", By: 1}}
	for i := 1; i < len(order); i++ {
		if lagScore(order[i-1]) >= lagScore(order[i]) {
			t.Errorf("%+v must score below %+v", order[i-1], order[i])
		}
	}
}

func TestNumber(t *testing.T) {
	for in, want := range map[any]int{float64(2): 2, 3: 3, int64(4): 4, json.Number("5"): 5, "6": 6, nil: 0, "x": 0} {
		if got := number(in); got != want {
			t.Errorf("number(%v) = %d, want %d", in, got, want)
		}
	}
}

// A window is closed at both ends for what began in it, open for what ended at its start.
func TestOverlaps(t *testing.T) {
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	at := func(d int) time.Time { return from.AddDate(0, 0, d) }
	end := func(d int) *time.Time { e := at(d); return &e }
	for name, c := range map[string]struct {
		start time.Time
		end   *time.Time
		want  bool
	}{
		"began at the very end of the window": {to, nil, true},
		"began after the window":              {to.Add(time.Second), nil, false},
		"ended at the start of the window":    {at(-5), end(0), false},
		"ended just inside the window":        {at(-5), end(1), true},
		"still open, began before the window": {at(-5), nil, true},
		"closed inside the window":            {at(2), end(3), true},
		"closed before the window":            {at(-5), end(-1), false},
	} {
		if got := overlaps(c.start, c.end, from, to); got != c.want {
			t.Errorf("%s: overlaps = %v, want %v", name, got, c.want)
		}
	}
}
