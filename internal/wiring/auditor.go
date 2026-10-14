package wiring

import (
	"context"
	"fmt"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/audit"
)

// Auditor makes the hash-chained audit log an api.Auditor: the API and the MCP
// server record through it, and every entry is appended to the chain.
type Auditor struct {
	Log *audit.Log
}

var _ api.Auditor = (*Auditor)(nil)

// Record appends e to the chain. The actor is "user:<sub>" or "token:<id>"
// (audit's convention); the time is the log's, not e.At, so that the chain's
// order is the order of appends. The audit log redacts secrets in details.
func (a *Auditor) Record(ctx context.Context, e api.AuditEntry) error {
	var outcome string
	switch e.Outcome {
	case api.OutcomeOK:
		outcome = audit.OutcomeSuccess
	case api.OutcomeDenied:
		outcome = audit.OutcomeDenied
	case api.OutcomeError:
		outcome = audit.OutcomeFailure
	default:
		return fmt.Errorf("wiring: audit: unknown outcome %q", e.Outcome)
	}
	_, err := a.Log.Append(ctx, audit.Entry{
		Actor: string(e.Kind) + ":" + e.Actor, Action: e.Action, Target: e.Target, Outcome: outcome, Details: e.Details,
	})
	return err
}
