package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Proposal is a batch waiting for human approval.
type Proposal struct {
	ID          int64      `json:"id"`
	CreatedAt   time.Time  `json:"created_at"`
	Source      string     `json:"source"`
	Author      string     `json:"author"`
	Reason      string     `json:"reason"`
	Batch       *sdk.Batch `json:"batch"`
	Status      string     `json:"status"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
	DecidedBy   string     `json:"decided_by,omitempty"`
	ChangesetID *int64     `json:"changeset_id,omitempty"`
	// Preview is computed on read, against the current state.
	Preview *Result `json:"preview,omitempty"`
}

// ErrConflict is returned when a proposal is not pending any more.
var ErrConflict = errors.New("conflict")

// Propose validates b, previews it and queues it. A pending proposal with the
// same content is not duplicated: the deducer re-proposes the same cables on
// every pass.
func (e *Engine) Propose(ctx context.Context, b *sdk.Batch, author, reason string) (*Result, error) {
	preview, err := e.Preview(ctx, b)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	fp := fingerprint(b)
	var id int64
	err = e.Store.Pool.QueryRow(ctx, `
		INSERT INTO proposals (source, author, reason, batch, fingerprint) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (fingerprint) WHERE status = 'pending' DO UPDATE SET reason = EXCLUDED.reason
		RETURNING id`, b.Source, author, reason, raw, fp).Scan(&id)
	if err != nil {
		return nil, err
	}
	preview.ProposalID = id
	return preview, nil
}

// fingerprint identifies a batch's content, ignoring when it was observed.
func fingerprint(b *sdk.Batch) string {
	c := *b
	c.ObservedAt = time.Time{}
	c.Health = nil
	raw, _ := json.Marshal(&c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// GetProposal returns a proposal with a fresh preview when it is pending.
func (e *Engine) GetProposal(ctx context.Context, id int64) (*Proposal, error) {
	p, err := scanProposal(e.Store.Pool.QueryRow(ctx, proposalSelect+` WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	if p.Status == "pending" {
		if prev, err := e.Preview(ctx, p.Batch); err == nil {
			p.Preview = prev
		}
	}
	return p, nil
}

const proposalSelect = `SELECT id, created_at, source, author, reason, batch, status, decided_at, decided_by, changeset_id FROM proposals`

func scanProposal(row pgx.Row) (*Proposal, error) {
	var p Proposal
	var raw []byte
	if err := row.Scan(&p.ID, &p.CreatedAt, &p.Source, &p.Author, &p.Reason, &raw, &p.Status, &p.DecidedAt, &p.DecidedBy, &p.ChangesetID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	p.Batch = &sdk.Batch{}
	if err := json.Unmarshal(raw, p.Batch); err != nil {
		return nil, err
	}
	return &p, nil
}

// ListProposals returns proposals with the given status ("" = all), newest first.
func (e *Engine) ListProposals(ctx context.Context, status string, limit int) ([]*Proposal, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := e.Store.Pool.Query(ctx, proposalSelect+` WHERE ($1 = '' OR status = $1) ORDER BY id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Approve applies a pending proposal on behalf of by.
func (e *Engine) Approve(ctx context.Context, id int64, by string) (*Result, error) {
	var res *Result
	err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		p, err := scanProposal(tx.QueryRow(ctx, proposalSelect+` WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if p.Status != "pending" {
			return fmt.Errorf("%w: proposal %d is %s", ErrConflict, id, p.Status)
		}
		reason := fmt.Sprintf("proposal %d approved by %s", id, by)
		if p.Reason != "" {
			reason += ": " + p.Reason
		}
		res, err = e.apply(ctx, tx, p.Batch, p.Author, reason, "proposal")
		if err != nil {
			return err
		}
		res.ProposalID = id
		_, err = tx.Exec(ctx, `UPDATE proposals SET status = 'applied', decided_at = now(), decided_by = $2, changeset_id = $3 WHERE id = $1`, id, by, res.ChangesetID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if e.OnCommit != nil {
		e.OnCommit(*res)
	}
	return res, nil
}

// Reject closes a pending proposal without applying it.
func (e *Engine) Reject(ctx context.Context, id int64, by string) error {
	tag, err := e.Store.Pool.Exec(ctx, `UPDATE proposals SET status = 'rejected', decided_at = now(), decided_by = $2 WHERE id = $1 AND status = 'pending'`, id, by)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: proposal %d is not pending", ErrConflict, id)
	}
	return nil
}
