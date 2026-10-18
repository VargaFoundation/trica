package live

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/VargaFoundation/trica/internal/store"
)

// Inventory is what the join reads: the current owners, interfaces and
// member_of relations. It is read, never written.
type Inventory interface {
	LiveObjects(ctx context.Context) ([]*store.Object, error)
	LiveRelations(ctx context.Context) ([]store.Relation, error)
}

// Snapshot is one measurement set, shared by every caller until the next
// refresh. It is never modified once published.
type Snapshot struct {
	// GeneratedAt is when the measurements were taken; nil when none has
	// ever succeeded.
	GeneratedAt *time.Time
	// Stale is true when the last refresh failed in part or in full: the
	// values are the last good ones, or none.
	Stale bool
	// Refresh is the refresh period.
	Refresh time.Duration
	// Links maps an interface id to its live state.
	Links map[string]Link
	// Sources says what each source contributed.
	Sources []SourceStatus
}

// SourceStatus is the outcome of one source at the last refresh.
type SourceStatus struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Series is how many series the source answered (excluded interfaces
	// left out); Matched how many of them met an interface of the
	// inventory.
	Series  int `json:"series"`
	Matched int `json:"matched"`
	// Stale is true when one of its queries failed at the last refresh.
	Stale bool `json:"stale"`
	// LastSuccess is when all of its queries last succeeded.
	LastSuccess *time.Time `json:"last_success,omitempty"`
}

// keepStale is how many refresh periods the last good answer of a failing
// query is still served.
const keepStale = 10

// Options builds a Service.
type Options struct {
	Inventory Inventory
	Log       *slog.Logger
	// Metrics receives the upstream counters; nil keeps them private.
	Metrics prometheus.Registerer
	// Now is the clock; tests replace it.
	Now func() time.Time
}

// Service answers Snapshot from a cache refreshed at most every
// cfg.Refresh.
type Service struct {
	cfg     *Config
	sources []*source
	q       querier
	inv     Inventory
	log     *slog.Logger
	now     func() time.Time
	m       *metrics

	mu          sync.Mutex
	snap        *Snapshot
	lastAttempt time.Time
	// last keeps, per source and query, the last good vector, served when
	// the query fails.
	last        map[string][3][]sample
	lastSuccess map[string]time.Time
}

type metrics struct {
	queries   *prometheus.CounterVec
	duration  prometheus.Histogram
	series    *prometheus.GaugeVec
	refreshes *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		queries: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "trica_live_upstream_queries_total",
			Help: "Queries sent to the live metrics endpoint, by source and outcome (ok, error)."}, []string{"source", "outcome"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "trica_live_upstream_query_duration_seconds",
			Help: "Duration of one query to the live metrics endpoint.", Buckets: prometheus.ExponentialBuckets(0.005, 3, 8)}),
		series: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "trica_live_series",
			Help: "Series of the last refresh, by source and whether they met an interface of the inventory (matched, unmatched)."}, []string{"source", "state"}),
		refreshes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "trica_live_refreshes_total",
			Help: "Refreshes of the live overlay, by outcome (ok, stale)."}, []string{"outcome"}),
	}
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	reg.MustRegister(m.queries, m.duration, m.series, m.refreshes)
	return m
}

// New builds the service for a validated configuration.
func New(cfg *Config, o Options) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	srcs, err := cfg.compiled()
	if err != nil {
		return nil, err
	}
	pc, err := newPromClient(cfg)
	if err != nil {
		return nil, err
	}
	return newService(cfg, srcs, pc, o), nil
}

func newService(cfg *Config, srcs []*source, q querier, o Options) *Service {
	s := &Service{cfg: cfg, sources: srcs, q: q, inv: o.Inventory, log: o.Log, now: o.Now, m: newMetrics(o.Metrics),
		last: map[string][3][]sample{}, lastSuccess: map[string]time.Time{}}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// Snapshot returns the current measurement set, refreshing it when older
// than the refresh period. A refresh is attempted at most once per period,
// whatever its outcome, so a failing endpoint is not hammered by callers.
func (s *Service) Snapshot(ctx context.Context) *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.snap != nil && now.Sub(s.lastAttempt) < s.cfg.Refresh {
		return s.snap
	}
	s.lastAttempt = now
	// The refresh serves every caller: a caller that goes away must not
	// cancel it.
	ctx = context.WithoutCancel(ctx)
	s.snap = s.refresh(ctx, now)
	return s.snap
}

func (s *Service) refresh(ctx context.Context, now time.Time) *Snapshot {
	results := make([]sourceResult, len(s.sources))
	failed := make([]bool, len(s.sources))
	got := make([][3][]sample, len(s.sources))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, src := range s.sources {
		results[i].src = src
		// The last good answers stand in for a failed query, for a while:
		// past keepStale they would describe a network that has moved on.
		if t, ok := s.lastSuccess[src.name]; ok && now.Sub(t) <= keepStale*s.cfg.Refresh {
			got[i] = s.last[src.name]
		}
		exprs := [3]string{}
		if src.rx != "" {
			exprs[0] = src.rateExpr(src.rx, s.cfg.RateWindow)
		}
		if src.tx != "" {
			exprs[1] = src.rateExpr(src.tx, s.cfg.RateWindow)
		}
		if src.oper != "" {
			exprs[2] = src.selector(src.oper)
		}
		for j, e := range exprs {
			if e == "" {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				qctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
				defer cancel()
				start := time.Now()
				vec, err := s.q.query(qctx, e)
				s.m.duration.Observe(time.Since(start).Seconds())
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					s.m.queries.WithLabelValues(src.name, "error").Inc()
					s.log.Warn("live: query failed", "source", "live:"+src.name, "error", err)
					failed[i] = true
					return
				}
				s.m.queries.WithLabelValues(src.name, "ok").Inc()
				got[i][j] = vec
			}()
		}
	}
	wg.Wait()
	for i, src := range s.sources {
		s.last[src.name] = got[i]
		if !failed[i] {
			s.lastSuccess[src.name] = now
		}
		results[i].rx, results[i].tx, results[i].oper = got[i][0], got[i][1], got[i][2]
	}

	snap := &Snapshot{Refresh: s.cfg.Refresh, Links: map[string]Link{}}
	stale := false
	for _, f := range failed {
		stale = stale || f
	}
	objs, err := s.inv.LiveObjects(ctx)
	var rels []store.Relation
	if err == nil {
		rels, err = s.inv.LiveRelations(ctx)
	}
	if err != nil {
		s.log.Warn("live: inventory read failed", "source", "live", "error", err)
		s.m.refreshes.WithLabelValues("stale").Inc()
		if s.snap != nil {
			old := *s.snap
			old.Stale = true
			return &old
		}
		snap.Stale = true
		return snap
	}
	links, stats := newIndex(objs, rels).build(results)
	snap.Links = links
	anySuccess := false
	for i, src := range s.sources {
		st := SourceStatus{Name: src.name, Kind: src.kind, Series: stats[i].series, Matched: stats[i].matched, Stale: failed[i]}
		if t, ok := s.lastSuccess[src.name]; ok {
			t := t
			st.LastSuccess = &t
			anySuccess = true
		}
		s.m.series.WithLabelValues(src.name, "matched").Set(float64(st.Matched))
		s.m.series.WithLabelValues(src.name, "unmatched").Set(float64(st.Series - st.Matched))
		snap.Sources = append(snap.Sources, st)
	}
	if anySuccess {
		t := now
		snap.GeneratedAt = &t
	}
	snap.Stale = stale
	if stale {
		s.m.refreshes.WithLabelValues("stale").Inc()
	} else {
		s.m.refreshes.WithLabelValues("ok").Inc()
	}
	return snap
}
