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
	"time"

	"github.com/VargaFoundation/trica/internal/auth"
)

// RequestIDHeader carries the request id in and out.
const RequestIDHeader = "X-Request-Id"

type ctxKey int

const (
	requestIDKey ctxKey = iota
	annotationsKey
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

// Middleware records every mutating request (anything but GET, HEAD and OPTIONS) and every
// denied one (401, 403, 429), whatever its method, after the handler has answered. It must
// be the outermost middleware, so that it sees the refusals of the inner ones.
//
// The entry is written after the response is decided: a process crash between the two loses
// it, and a failure to write it is logged, not shown to the client. The principal comes from
// auth.Middleware through an auth.Slot; a request that never authenticated is recorded as
// "anonymous". Only the path is recorded, never the query string or any header.
func Middleware(l *Log, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
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
			ctx, slot := auth.WithSlot(ctx)
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

			defer func() {
				rec := recover()
				if rec != nil {
					sw.status = http.StatusInternalServerError
				}
				l.record(r, sw.status, id, slot, ann, logger)
				if rec != nil {
					panic(rec)
				}
			}()
			next.ServeHTTP(sw, r.WithContext(ctx))
		})
	}
}

func (l *Log) record(r *http.Request, status int, id string, slot *auth.Slot, ann *annotations, logger *slog.Logger) {
	denied := status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests
	if !denied && !mutating(r.Method) {
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
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		details["remote"] = host
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
	_, err := l.Append(ctx, Entry{
		Actor: actor, Action: "http." + r.Method, Target: r.URL.Path, Outcome: outcome, RequestID: id, Details: details,
	})
	if err != nil {
		logger.Error("audit entry not recorded", "request_id", id, "actor", actor, "error", err.Error())
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
