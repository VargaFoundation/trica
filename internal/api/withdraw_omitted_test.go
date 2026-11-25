package api_test

import (
	"testing"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// withdraw_omitted would let one call retire everything a person declared on
// other objects (ADR 0071): a person's batch carrying it is refused, a token
// bound to its own source may send it.
func TestWithdrawOmittedIsNotForPeople(t *testing.T) {
	e := newEnv(t, reconcile.Policy{})
	full := func() *sdk.Batch {
		b := userBatch("", func(b *sdk.Batch) {
			b.Object("a", "application").Identify("name", "billing").Set("criticality", "high")
		})
		b.Scope, b.Confidence, b.WithdrawOmitted = sdk.ScopeFull, sdk.Declared, true
		return b
	}
	wantProblem(t, e.do("POST", "/api/v1/batches", tokAdmin, batchJSON(t, full())), 400, "invalid-request")

	// The same batch without the flag is a person's ordinary declaration.
	b := full()
	b.WithdrawOmitted = false
	wantStatus(t, e.do("POST", "/api/v1/batches", tokAdmin, batchJSON(t, b)), 200)

	// A token bound to a source is not refused at the door.
	wantStatus(t, e.do("POST", "/api/v1/batches", tokMCP, batchJSON(t, full())), 200)
}
