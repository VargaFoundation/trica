package store_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/testdb"
)

// Listen survives the loss of its connection: it reconnects, says so with an
// event (what was sent meanwhile is lost), and keeps delivering.
func TestListenReconnects(t *testing.T) {
	t.Cleanup(store.SetListenRetry(50 * time.Millisecond))
	s := testdb.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan struct{}, 16)
	done := make(chan error, 1)
	go func() {
		done <- s.Listen(ctx, "trica_test", func() {
			select {
			case events <- struct{}{}:
			default:
			}
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	expect := func(what string) {
		t.Helper()
		select {
		case <-events:
		case <-time.After(10 * time.Second):
			t.Fatalf("no event: %s", what)
		}
	}
	notify := func() {
		t.Helper()
		if _, err := s.Pool.Exec(context.Background(), `SELECT pg_notify('trica_test', 'x')`); err != nil {
			t.Fatal(err)
		}
	}

	expect("listening")
	notify()
	expect("notification")

	var killed int
	if err := s.Pool.QueryRow(context.Background(), `SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid() AND query LIKE 'LISTEN%'`).Scan(&killed); err != nil || killed != 1 {
		t.Fatalf("terminate the listener: %d %v", killed, err)
	}
	expect("listening again")
	notify()
	expect("notification after the reconnection")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Listen did not return when its context ended")
	}
}
