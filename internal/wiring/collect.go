package wiring

import (
	"context"
	"fmt"
	"time"

	// Every collector registers itself in an init function; this is the one
	// place the server pulls them in.
	_ "github.com/VargaFoundation/trica/internal/collect/all"
	"github.com/VargaFoundation/trica/internal/collect/runner"
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// collectLockKey is the advisory lock that makes in-server collection a
// singleton across replicas: two replicas collecting the same switch would
// double its load and race each other's full-scope batches (ADR 0026). It
// differs from the engine's, the audit log's and the deducer's.
const collectLockKey = 7426204

// collectRetry is how often a replica that does not hold the lock tries again,
// and how often the holder checks that its connection (and so its lock) lives.
var collectRetry = 15 * time.Second

// runCollect runs the collectors of collect.targets_file and submits what
// they emit to the reconciliation engine, in process: the collectors never
// write the inventory, the engine does (ADR 0005). Like the transport, a
// component the operator configured that cannot start stops the server: a
// targets file that does not parse must not leave a deployment discovering
// nothing in silence.
//
// Only one replica collects at a time, the one holding a session-level
// PostgreSQL advisory lock; the others retry. A failing target does not stop
// the server or the other targets (the runner contains it and reports it on
// the health board).
func (w *Wiring) runCollect(ctx context.Context, d server.Deps) error {
	file := w.cfg.Collect.TargetsFile
	targets, err := w.collectTargets()
	if err != nil {
		return err
	}
	emitter := sdk.EmitterFunc(func(ctx context.Context, b *sdk.Batch) error {
		// The batch's source is the target's ("arista:sw-a1"); the engine
		// decides what it does to the inventory, and refuses what it must.
		_, err := d.Engine.Submit(ctx, b, b.Source, "collector")
		return err
	})
	r, err := runner.New(runner.Config{Targets: targets, Emitter: emitter, Log: d.Log, Registerer: d.Metrics})
	if err != nil {
		return fmt.Errorf("collect.targets_file %s: %w", file, err)
	}
	d.Log.Info("collection configured", "targets", len(targets))

	for ctx.Err() == nil {
		w.collectAsLeader(ctx, d, r)
		select {
		case <-ctx.Done():
		case <-time.After(collectRetry):
		}
	}
	return nil
}

// collectAsLeader takes the lock and runs r until ctx ends or the lock is
// lost; it returns at once when another replica holds the lock.
func (w *Wiring) collectAsLeader(ctx context.Context, d server.Deps, r *runner.Runner) {
	conn, err := d.Store.Pool.Acquire(ctx)
	if err != nil {
		logFailure(d.Log, "collection: no database connection", err)
		return
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, collectLockKey).Scan(&got); err != nil {
		logFailure(d.Log, "collection: lock", err)
		return
	}
	if !got {
		return
	}
	defer func() {
		// Not the run's context: the lock must be released on shutdown too.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, collectLockKey)
	}()
	d.Log.Info("collection started on this replica")

	// A lock lives and dies with its connection: if the connection is lost,
	// stop collecting, another replica may hold the lock by now.
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(collectRetry)
		defer t.Stop()
		for {
			select {
			case <-rctx.Done():
				return
			case <-t.C:
				if err := conn.Conn().Ping(rctx); err != nil {
					logFailure(d.Log, "collection: lock connection lost, stopping", err)
					cancel()
					return
				}
			}
		}
	}()
	_ = r.Run(rctx)
}
