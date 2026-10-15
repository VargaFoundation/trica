package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VargaFoundation/trica/internal/auth"
)

// RequestIDHeader carries the request id in and out.
const RequestIDHeader = "X-Request-Id"

type ctxKey int

const (
	requestIDKey ctxKey = iota
	annotationsKey
	recordedKey
)

// validRequestID bounds what a client may put in the audit log through its request id.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// RequestID returns the request id set by Middleware, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

type annotations struct {
	mu sync.Mutex
	m  map[string]any
}

// Annotate adds a key to the details of the entry Middleware will record for this request,
// e.g. the id of the object a handler changed. It does nothing outside Middleware. Secrets
// are redacted on the way into the log, whatever the key.
func Annotate(ctx context.Context, key string, value any) {
	if a, ok := ctx.Value(annotationsKey).(*annotations); ok {
		a.mu.Lock()
		a.m[key] = value
		a.mu.Unlock()
	}
}

// Options tunes NewMiddleware.
type Options struct {
	// RefusalsOnly records the denied requests (401, 403, 429) only. It is for handlers that
	// record their own calls (the API and the MCP server, through api.Auditor): the
	// middleware then adds what they cannot see, the refusals of the layers in front of
	// them.
	RefusalsOnly bool
	// RateLimitedEvery is how often a 429 is recorded per caller (the principal, else the
	// peer address): the first of each period is, the next ones are counted in the
	// "suppressed" detail of the following entry that is. Every entry is a serialised,
	// chained write: a flood of refused requests must not become a flood of writes. One
	// minute when zero.
	RateLimitedEvery time.Duration
	// Logger receives the write failures and the counts a sweep drops; slog.Default when nil.
	Logger *slog.Logger
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// Middleware is NewMiddleware recording every mutating and every denied request.
func Middleware(l *Log, logger *slog.Logger) func(http.Handler) http.Handler {
	return NewMiddleware(l, Options{Logger: logger})
}

// NewMiddleware records every mutating request (anything but GET, HEAD and OPTIONS) and every
// denied one (401, 403, 429), whatever its method, after the handler has answered; with
// RefusalsOnly, the denied ones only. It must be the outermost middleware, so that it sees
// the refusals of the inner ones.
//
// A request whose handler appended an entry itself, with the request's context, is not
// recorded again: one request, one entry. The entry is written after the response is
// decided: a process crash between the two loses it, and a failure to write it is logged,
// not shown to the client. The principal comes from auth.Middleware or auth.WithPrincipal
// through an auth.Slot; a request that never authenticated is recorded as "anonymous". Only
// the path is recorded, never the query string or any header.
func NewMiddleware(l *Log, o Options) func(http.Handler) http.Handler {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.RateLimitedEvery <= 0 {
		o.RateLimitedEvery = time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	m := &middleware{l: l, o: o, sampler: &sampler{every: o.RateLimitedEvery, now: o.Now, logger: o.Logger, seen: map[string]*sample{}}}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validRequestID.MatchString(id) {
				id = newRequestID()
			}
			w.Header().Set(RequestIDHeader, id)
			ctx := context.WithValue(r.Context(), requestIDKey, id)
			ann := &annotations{m: map[string]any{}}
			ctx = context.WithValue(ctx, annotationsKey, ann)
			done := &atomic.Bool{}
			ctx = context.WithValue(ctx, recordedKey, done)
			ctx, slot := auth.WithSlot(ctx)
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

			defer func() {
				rec := recover()
				if rec != nil {
					sw.status = http.StatusInternalServerError
				}
				if !done.Load() {
					m.record(r, sw.status, id, slot, ann)
				}
				if rec != nil {
					panic(rec)
				}
			}()
			next.ServeHTTP(sw, r.WithContext(ctx))
		})
	}
}

type middleware struct {
	l       *Log
	o       Options
	sampler *sampler
}

// markRecorded tells the middleware watching ctx's request, if any, that the request has
// its entry.
func markRecorded(ctx context.Context) {
	if done, ok := ctx.Value(recordedKey).(*atomic.Bool); ok {
		done.Store(true)
	}
}

func (m *middleware) record(r *http.Request, status int, id string, slot *auth.Slot, ann *annotations) {
	denied := status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests
	if !denied && (m.o.RefusalsOnly || !mutating(r.Method)) {
		return
	}
	outcome := OutcomeSuccess
	switch {
	case denied:
		outcome = OutcomeDenied
	case status >= http.StatusBadRequest:
		outcome = OutcomeFailure
	}
	actor := "anonymous"
	if slot.Principal != nil {
		actor = slot.Principal.Subject
	}
	details := map[string]any{"method": r.Method, "status": status}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		details["remote"] = host
	}
	if status == http.StatusTooManyRequests {
		key := actor
		if slot.Principal == nil {
			key = "ip:" + host
		}
		ok, suppressed := m.sampler.admit(key)
		if !ok {
			return
		}
		if suppressed > 0 {
			details["suppressed"] = suppressed
		}
	}
	if slot.Reason != "" {
		details["reason"] = slot.Reason
	}
	ann.mu.Lock()
	for k, v := range ann.m {
		details[k] = v
	}
	ann.mu.Unlock()
	// The client going away must not lose the record of what it did.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	_, err = m.l.Append(ctx, Entry{
		Actor: actor, Action: "http." + r.Method, Target: r.URL.Path, Outcome: outcome, RequestID: id, Details: details,
	})
	if err != nil {
		m.o.Logger.Error("audit entry not recorded", "request_id", id, "actor", actor, "error", err.Error())
	}
}

// sampler admits one rate-limited request per key and period, and counts the others.
type sampler struct {
	every  time.Duration
	now    func() time.Time
	logger *slog.Logger

	mu        sync.Mutex
	seen      map[string]*sample
	lastSweep time.Time
}

type sample struct {
	at         time.Time // when the last admitted request was
	suppressed int       // requests refused since, not admitted
}

// admit reports whether a rate-limited request of key is recorded, and how many of key's
// were not since the last one that was.
func (s *sampler) admit(key string) (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	// The key is settled before the sweep, which would drop it with its count.
	defer s.sweep(now)
	c, ok := s.seen[key]
	if ok && now.Sub(c.at) < s.every {
		c.suppressed++
		return false, 0
	}
	n := 0
	if ok {
		n = c.suppressed
	}
	s.seen[key] = &sample{at: now}
	return true, n
}

// sweep drops the keys whose period is over, so that the map cannot grow without bound. A
// key dropped with requests still counted has them logged, since no entry will carry them.
func (s *sampler) sweep(now time.Time) {
	if now.Sub(s.lastSweep) < s.every {
		return
	}
	s.lastSweep = now
	for k, c := range s.seen {
		if now.Sub(c.at) < s.every {
			continue
		}
		if c.suppressed > 0 {
			s.logger.Warn("rate-limited requests not recorded in the audit log", "caller", k, "count", c.suppressed)
		}
		delete(s.seen, k)
	}
}

func mutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// statusWriter remembers the status code. Unwrap lets http.ResponseController reach the
// underlying writer (flush, deadlines).
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
