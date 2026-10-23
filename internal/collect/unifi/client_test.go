package unifi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A server that echoes the key it was given, in its error message or in a
// transport error, must not get it into a health record.
func TestErrorMessagesDoNotEchoTheKey(t *testing.T) {
	const key = "echoed-secret-key-9999"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"x","message":"bad key ` + r.Header.Get("X-API-KEY") + `"}`))
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	cl := newClient(srv.Client(), base, key)
	_, err := cl.get(context.Background(), "/v1/hosts", nil)
	if err == nil || !strings.Contains(err.Error(), "bad key [redacted]") || strings.Contains(err.Error(), key) {
		t.Fatalf("error %v", err)
	}
	srv.Close()
	_, err = cl.get(context.Background(), "/v1/hosts", url.Values{"q": {key}})
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatalf("transport error %v", err)
	}
	if e := cl.scrub(&url.Error{Op: "Get", URL: "https://x/?k=" + key, Err: errString("dial " + key)}); strings.Contains(e.Error(), key) {
		t.Errorf("scrub left the key: %v", e)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestResponseTooLarge(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1<<20)
		for i := 0; i <= maxBody>>20; i++ {
			_, _ = w.Write(buf)
		}
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	_, err := newClient(srv.Client(), base, "k").get(context.Background(), "/v1/hosts", nil)
	if err == nil || kindOf(err) != errMalformed || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error %v", err)
	}
}

func TestClipLongMessage(t *testing.T) {
	c := &client{key: "k"}
	if got := c.clip(strings.Repeat("x", 300)); len(got) != 203 {
		t.Errorf("clip: %d", len(got))
	}
}

func TestListQueryIsNotShared(t *testing.T) {
	var queries []string
	n := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		n++
		if n == 1 {
			_, _ = w.Write([]byte(`{"data":[{"id":"a"}],"nextToken":"t2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"b"}]}`))
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	q := url.Values{"hostIds[]": {"h1"}}
	hosts, err := listAll[host](context.Background(), newClient(srv.Client(), base, "k"), "/v1/hosts", q)
	if err != nil || len(hosts) != 2 || hosts[0].ID != "a" || hosts[1].ID != "b" {
		t.Fatalf("%v, %v", hosts, err)
	}
	if q.Get("nextToken") != "" {
		t.Error("the caller's query was modified")
	}
	if len(queries) != 2 || !strings.Contains(queries[1], "nextToken=t2") || !strings.Contains(queries[1], "hostIds") {
		t.Errorf("queries %v", queries)
	}
}

func TestListDecodeFailure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"not":"a list"}}`))
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	_, err := listAll[host](context.Background(), newClient(srv.Client(), base, "k"), "/v1/hosts", nil)
	if err == nil || kindOf(err) != errMalformed {
		t.Fatalf("error %v", err)
	}
	// An answer with no data is an empty list, not an error.
	srv2 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":null}`)) }))
	defer srv2.Close()
	base2, _ := url.Parse(srv2.URL)
	if hosts, err := listAll[host](context.Background(), newClient(srv2.Client(), base2, "k"), "/v1/hosts", nil); err != nil || len(hosts) != 0 {
		t.Errorf("%v, %v", hosts, err)
	}
}

func TestSlowServerHitsTimeout(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	hc := srv.Client()
	hc.Timeout = 100 * time.Millisecond
	_, err := newClient(hc, base, "k").get(context.Background(), "/v1/hosts", nil)
	if err == nil || kindOf(err) != errTransport {
		t.Fatalf("error %v", err)
	}
}
