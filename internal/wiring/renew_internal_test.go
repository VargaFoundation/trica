package wiring

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/pki"
)

// A failed renewal is retried well before the certificate expires, not after
// another two thirds of its lifetime.
func TestRenewServerCertRetriesAFailure(t *testing.T) {
	ca, err := pki.NewCA("test CA", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	calls := 0
	issue := func() (tls.Certificate, error) {
		calls++
		if calls == 1 {
			return tls.Certificate{}, errors.New("disk full")
		}
		return ca.IssueServer([]string{"localhost"}, time.Hour, time.Now())
	}
	set := make(chan struct{})
	start := time.Now()
	go renewServerCert(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), 3*time.Second, issue, func(tls.Certificate) error {
		close(set)
		cancel()
		return nil
	})
	select {
	case <-set:
	case <-ctx.Done():
		t.Fatal("the certificate was never set")
	}
	// 2 s to the first attempt, 300 ms to the retry; another two thirds
	// would have been 4 s.
	if d := time.Since(start); d >= 4*time.Second {
		t.Fatalf("the retry came after %v", d)
	}
	if calls != 2 {
		t.Fatalf("%d issues, want 2", calls)
	}
}
