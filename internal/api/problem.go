package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/tql"
)

// ProblemContentType is the media type of every error body (RFC 9457).
const ProblemContentType = "application/problem+json"

// Problem is an RFC 9457 problem details object.
type Problem struct {
	// Type is a URN naming the problem class: "urn:trica:problem:<slug>".
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Instance is the request path.
	Instance string `json:"instance,omitempty"`
}

// httpError is an error a handler returns to be answered as a problem.
type httpError struct {
	status int
	slug   string
	detail string
	// title overrides the generic title of the status, for a problem class that
	// has its own ("Batch rejected").
	title string
}

func (e *httpError) Error() string { return fmt.Sprintf("%d %s: %s", e.status, e.slug, e.detail) }

func newErr(status int, slug, format string, args ...any) *httpError {
	return &httpError{status: status, slug: slug, detail: fmt.Sprintf(format, args...)}
}

func badRequest(format string, args ...any) *httpError {
	return newErr(http.StatusBadRequest, "invalid-request", format, args...)
}

func notFound(format string, args ...any) *httpError {
	return newErr(http.StatusNotFound, "not-found", format, args...)
}

func forbidden(format string, args ...any) *httpError {
	return newErr(http.StatusForbidden, "forbidden", format, args...)
}

var titles = map[int]string{
	http.StatusBadRequest:            "Bad request",
	http.StatusUnauthorized:          "Unauthorized",
	http.StatusForbidden:             "Forbidden",
	http.StatusNotFound:              "Not found",
	http.StatusMethodNotAllowed:      "Method not allowed",
	http.StatusConflict:              "Conflict",
	http.StatusRequestEntityTooLarge: "Request too large",
	http.StatusUnprocessableEntity:   "Unprocessable content",
	http.StatusInternalServerError:   "Internal error",
	http.StatusServiceUnavailable:    "Service unavailable",
}

// classify turns any error into the problem the client sees. Errors that are
// not the client's fault are logged and answered without their text: a
// database error can name tables and values.
func classify(err error) (*httpError, bool) {
	var he *httpError
	var syn *tql.Error
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &he):
		return he, false
	case errors.As(err, &tooBig):
		return newErr(http.StatusRequestEntityTooLarge, "too-large", "the request body exceeds %d bytes", tooBig.Limit), false
	case errors.Is(err, reconcile.ErrInvalid):
		return newErr(http.StatusBadRequest, "invalid-batch", "%s", err.Error()), false
	case errors.Is(err, reconcile.ErrRejected):
		// Well formed, refused by the engine (ADR 0005): the caller can fix and resend.
		he := newErr(http.StatusUnprocessableEntity, "rejected", "%s", err.Error())
		he.title = "Batch rejected"
		return he, false
	case errors.Is(err, search.ErrQuery), errors.As(err, &syn):
		return newErr(http.StatusBadRequest, "invalid-query", "%s", err.Error()), false
	case errors.Is(err, reconcile.ErrNameTaken):
		return newErr(http.StatusConflict, "name-taken", "%s", err.Error()), false
	case errors.Is(err, reconcile.ErrConflict):
		return newErr(http.StatusConflict, "conflict", "%s", err.Error()), false
	case errors.Is(err, store.ErrNotFound):
		return newErr(http.StatusNotFound, "not-found", "not found"), false
	case errors.Is(err, context.Canceled):
		// The client went away: nobody reads this.
		return newErr(http.StatusServiceUnavailable, "canceled", "request canceled"), false
	}
	return newErr(http.StatusInternalServerError, "internal", "an internal error occurred"), true
}

func writeProblem(w http.ResponseWriter, r *http.Request, he *httpError) {
	title := he.title
	if title == "" {
		title = titles[he.status]
	}
	if title == "" {
		title = http.StatusText(he.status)
	}
	p := Problem{Type: "urn:trica:problem:" + he.slug, Title: title, Status: he.status, Detail: he.detail, Instance: r.URL.Path}
	w.Header().Set("Content-Type", ProblemContentType)
	if he.status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(he.status)
	_ = json.NewEncoder(w).Encode(p)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) *httpError {
	he, internal := classify(err)
	if internal {
		s.log.Error("api: internal error", slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Any("error", err))
	}
	writeProblem(w, r, he)
	return he
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
	return nil
}
