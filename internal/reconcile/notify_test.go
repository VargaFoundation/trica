package reconcile_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
)

// listen opens a session listening on the changes channel, as another replica
// would.
func listen(t *testing.T, s *store.Store) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.ConnectConfig(ctx, s.Pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	if _, err := conn.Exec(ctx, "LISTEN "+reconcile.ChangesChannel); err != nil {
		t.Fatal(err)
	}
	return conn
}

// next returns the payload of the next notification, or "" when none arrives
// within d.
func next(t *testing.T, conn *pgx.Conn, d time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	n, err := conn.WaitForNotification(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ""
		}
		t.Fatal(err)
	}
	return n.Payload
}

// Every commit is announced to the other replicas with its changeset id (#57,
// ADR 0032); a preview, rolled back, announces nothing.
func TestCommitsAreAnnounced(t *testing.T) {
	e, s, c := newEngine(t, reconcile.Policy{AbsentAfter: 30 * time.Minute})
	ctx := context.Background()
	conn := listen(t, s)

	if _, err := e.Preview(ctx, switchBatch(c, "sw-a1", "192.0.2.10")); err != nil {
		t.Fatal(err)
	}
	if got := next(t, conn, 300*time.Millisecond); got != "" {
		t.Fatalf("a preview was announced: %q", got)
	}

	r := apply(t, e, switchBatch(c, "sw-a1", "192.0.2.10"))
	if got, want := next(t, conn, 5*time.Second), strconv.FormatInt(r.ChangesetID, 10); got != want {
		t.Fatalf("apply announced %q, want changeset %s", got, want)
	}

	// Ethernet2 stops being reported; the sweep that marks it absent is a
	// commit too.
	c.advance(20 * time.Minute)
	b := switchBatch(c, "sw-a1", "192.0.2.10")
	b.Objects = b.Objects[:2]
	apply(t, e, b)
	if got := next(t, conn, 5*time.Second); got == "" {
		t.Fatal("the second apply was not announced")
	}
	c.advance(20 * time.Minute)
	if gone, err := e.SweepAbsent(ctx); err != nil || len(gone) != 1 {
		t.Fatalf("sweep: %v %v", gone, err)
	}
	if got := next(t, conn, 5*time.Second); got == "" {
		t.Fatal("the absence sweep was not announced")
	}
}
