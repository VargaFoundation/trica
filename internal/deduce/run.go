package deduce

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Source is the batch source of deduced cables. The policy decides whether
// its batches are applied or queued as proposals ("deduce:*").
const Source = "deduce:cabling"

// Deducer runs the deduction against the inventory and submits the result.
type Deducer struct {
	Engine *reconcile.Engine
	Store  *store.Store
	// Now is the clock; tests replace it.
	Now func() time.Time
	Log *slog.Logger
}

// Report is what one pass did.
type Report struct {
	Plan   *Plan
	Result *reconcile.Result
	// Resolved counts cable discrepancies closed because they no longer hold.
	Resolved int64
}

func (d *Deducer) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC().Truncate(time.Microsecond)
	}
	return time.Now().UTC().Truncate(time.Microsecond)
}

func (d *Deducer) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

// Run performs one pass: read the current inventory, deduce, submit the
// cables as one full-scope batch (a cable no longer supported is withdrawn,
// which closes its relation), then open and resolve cable discrepancies.
func (d *Deducer) Run(ctx context.Context) (*Report, error) {
	start := d.now()
	objs, err := d.Store.AllObjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("deduce: read objects: %w", err)
	}
	rels, err := d.Store.CurrentRelations(ctx)
	if err != nil {
		return nil, fmt.Errorf("deduce: read relations: %w", err)
	}
	plan := Deduce(objs, rels)

	b := sdk.NewBatch(Source, sdk.ScopeFull)
	b.ObservedAt = start
	b.Confidence = sdk.Deduced
	for _, c := range plan.Cables {
		r := b.RelateEndpoints(relCabled, sdk.ByID(typeInterface, c.A), sdk.ByID(typeInterface, c.B))
		r.Attributes["evidence"] = c.Evidence
		r.Attributes["level"] = c.Level
	}
	res, err := d.Engine.Submit(ctx, b, "", "cables deduced from LLDP and forwarding tables")
	if err != nil {
		return nil, fmt.Errorf("deduce: submit: %w", err)
	}

	rep := &Report{Plan: plan, Result: res}
	err = d.Store.InTx(ctx, func(tx pgx.Tx) error {
		for _, f := range plan.Findings {
			if err := reconcile.OpenDiscrepancy(ctx, tx, discrepancy(f), start); err != nil {
				return err
			}
		}
		n, err := reconcile.ResolveStale(ctx, tx, []string{KindConflict, KindNotObserved, KindNotDeclared}, "cable:", start, Source)
		rep.Resolved = n
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("deduce: discrepancies: %w", err)
	}
	for _, s := range plan.Skipped {
		d.log().Debug("port skipped", "source", Source, "object_id", s.Port, "reason", s.Reason)
	}
	d.log().Info("cabling deduced", "source", Source, "cables", len(plan.Cables), "skipped", len(plan.Skipped),
		"findings", len(plan.Findings), "resolved", rep.Resolved, "opened", res.RelationsOpened, "closed", res.RelationsClosed,
		"proposal_id", res.ProposalID)
	return rep, nil
}

// Loop runs a pass every interval until ctx ends. A failed pass is logged
// and retried at the next tick. The acceptance criterion is a moved cable
// reflected within 15 minutes, collection interval included: the interval
// must stay well below that.
func (d *Deducer) Loop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := d.Run(ctx); err != nil && ctx.Err() == nil {
			d.log().Error("deduction failed", "source", Source, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func discrepancy(f Finding) reconcile.Discrepancy {
	d := reconcile.Discrepancy{Kind: f.Kind, Fingerprint: f.Fingerprint(), ObjectID: f.Port, RelatedID: f.Other, Key: relCabled}
	if f.Declared != "" {
		d.Declared, _ = json.Marshal(f.Declared)
	}
	if f.Observed != "" {
		d.Observed, _ = json.Marshal(f.Observed)
	}
	return d
}
