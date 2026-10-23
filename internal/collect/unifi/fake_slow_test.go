package unifi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A slow endpoint stands for a server that never answers in time. When its
// request ends before the delay, the handler must abort the connection and
// not return: a handler that returns without writing makes net/http answer
// "200 OK" with an empty body, which a client reading at that moment would
// classify as a malformed answer instead of a transport error (#186).
func TestSlowFakeAbortsInsteadOfAnsweringWhenTheRequestEnds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		handle func(*testing.T) http.HandlerFunc
		path   string
	}{
		{"site manager", func(t *testing.T) http.HandlerFunc {
			f := newFake(t, SetRecorded)
			f.Slow("", time.Hour)
			return f.handle
		}, "/v1/hosts"},
		{"network", func(t *testing.T) http.HandlerFunc {
			f := newFakeNet(t, SetNetwork)
			f.Slow("", time.Hour)
			return f.handle
		}, networkPrefix + "/sites"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // the client has already gone
			r := httptest.NewRequest(http.MethodGet, tc.path, nil).WithContext(ctx)
			w := httptest.NewRecorder()
			var got any
			func() {
				defer func() { got = recover() }()
				tc.handle(t)(w, r)
			}()
			if err, _ := got.(error); !errors.Is(err, http.ErrAbortHandler) {
				t.Fatalf("handler returned or panicked with %v, want http.ErrAbortHandler (status %d, body %q)", got, w.Code, w.Body.String())
			}
		})
	}
}
