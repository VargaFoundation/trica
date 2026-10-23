package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/tql"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// decodeStrict reads one JSON value and refuses unknown fields and trailing data.
func decodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return badRequest("the request body is empty")
		}
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return err
		}
		return badRequest("invalid JSON body: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return err
		}
		return badRequest("invalid JSON body: trailing data after the first value")
	}
	return nil
}

// ----------------------------------------------------------------- batches

// bindBatch decodes a batch and attributes it to the caller. The source is
// never the client's choice: a person speaks as "user:<subject>", a token as
// the source it is bound to. A person's batch is also forced to "declared"
// confidence: a person does not observe, and must not outrank a collector by
// claiming to.
func (c *call) bindBatch() (b *sdk.Batch, claimed string, err error) {
	b = &sdk.Batch{}
	if err := decodeStrict(c.r.Body, b); err != nil {
		return nil, "", err
	}
	claimed = b.Source
	src := c.p.BatchSource()
	if src == "" || (c.p.Kind != KindUser && c.p.Kind != KindToken) {
		return nil, claimed, forbidden("this credential is not bound to a source and cannot submit batches")
	}
	b.Source = src
	if c.p.Kind == KindUser {
		b.Confidence = sdk.Declared
		for _, o := range b.Objects {
			if o != nil {
				o.Confidence = ""
			}
		}
		for _, r := range b.Relations {
			if r != nil {
				r.Confidence = ""
			}
		}
	}
	for _, o := range b.Objects {
		if o == nil {
			return nil, claimed, badRequest("a batch object is null")
		}
	}
	for _, r := range append(append([]*sdk.Relation(nil), b.Relations...), b.Unrelations...) {
		if r == nil {
			return nil, claimed, badRequest("a batch relation is null")
		}
	}
	return b, claimed, nil
}

// checkBatch applies the rules that do not need the engine: no secret is
// written, types exist, and the caller holds each action on every type the
// batch touches (objects, and the ends of relations and unrelations) and on
// the existing objects it designates by id.
func (c *call) checkBatch(b *sdk.Batch, actions ...string) error {
	if k := sensitiveInBatch(b); k != "" {
		return newErr(http.StatusBadRequest, "secret-attribute",
			"the attribute %q looks like a secret; the inventory stores references to credentials, never the credentials", k)
	}
	reg := c.s.registry()
	for _, t := range batchTypes(b) {
		if _, ok := reg.Types[t]; !ok {
			return newErr(http.StatusBadRequest, "invalid-batch", "unknown object type %q", t)
		}
	}
	for _, action := range actions {
		if t, ok := c.allowTypes(action, batchTypes(b)); !ok {
			return forbidden("the %q action is not granted on %s objects", action, t)
		}
	}
	// Objects designated by id exist already: their own type and site count.
	var ids []string
	for _, o := range b.Objects {
		if id := o.Identity[sdk.IDKey]; id != "" {
			id, err := parseUUID(id, "an object identity id")
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
	}
	// Likewise the objects a relation or unrelation designates by id: the
	// type stored for the id counts, not the one the caller wrote.
	for _, e := range batchEndpoints(b) {
		if id := e.Identity[sdk.IDKey]; e.Ref == "" && id != "" {
			id, err := parseUUID(id, "an endpoint identity id")
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	objs, err := c.s.st.Objects(c.ctx(), ids)
	if err != nil {
		return err
	}
	oids := make([]string, 0, len(objs))
	for _, o := range objs {
		oids = append(oids, o.ID)
	}
	sites, err := c.s.siteIDs(c.ctx(), oids)
	if err != nil {
		return err
	}
	for _, o := range objs {
		for _, action := range actions {
			if !c.s.auth.Allow(c.p, action, o.Type, sites[o.ID]) {
				return forbidden("the %q action is not granted on object %s", action, o.ID)
			}
		}
	}
	return nil
}

func reasonParam(c *call) (string, error) {
	r := c.r.URL.Query().Get("reason")
	if len(r) > 500 {
		return "", badRequest("reason is limited to 500 characters")
	}
	return r, nil
}

// visibleResult drops from a result what the caller may not read: the planned
// changes on types it may not read and, unless it may read every type, the ids
// of the objects the batch touched (their types are not known here).
func (c *call) visibleResult(res *reconcile.Result) *reconcile.Result {
	res = redactResult(res)
	if res == nil {
		return nil
	}
	if _, readsAll := c.readableTypes(); !readsAll {
		res.Objects, res.Touched = map[string]string{}, nil
	}
	allowed := map[string]bool{}
	var keep []reconcile.PlannedChange
	for _, ch := range res.Changes {
		ok, seen := allowed[ch.Type]
		if !seen {
			ok = c.s.auth.Allow(c.p, ActionRead, ch.Type, "")
			allowed[ch.Type] = ok
		}
		if ok {
			keep = append(keep, ch)
		}
	}
	res.Changes = keep
	return res
}

func (s *Server) hSubmit(c *call) error {
	b, claimed, err := c.bindBatch()
	c.audited("batch.submit", c.p.BatchSource(), map[string]any{"claimed_source": claimed})
	if err != nil {
		return err
	}
	c.auditDetails["objects"], c.auditDetails["relations"] = len(b.Objects), len(b.Relations)
	if err := c.checkBatch(b, ActionPropose); err != nil {
		return err
	}
	reason, err := reasonParam(c)
	if err != nil {
		return err
	}
	res, err := s.eng.Submit(c.ctx(), b, c.p.Subject, reason)
	if err != nil {
		return err
	}
	status, code := "applied", http.StatusOK
	if res.ProposalID != 0 {
		status, code = "proposed", http.StatusAccepted
		c.auditDetails["proposal_id"] = res.ProposalID
	} else {
		c.auditDetails["changeset_id"] = res.ChangesetID
	}
	c.auditDetails["status"] = status
	return c.json(code, BatchOutcome{Source: b.Source, Status: status, Result: c.visibleResult(res)})
}

func (s *Server) hPreview(c *call) error {
	b, _, err := c.bindBatch()
	if err != nil {
		return err
	}
	// A preview shows current values: reading is needed as well as proposing.
	if err := c.checkBatch(b, ActionPropose, ActionRead); err != nil {
		return err
	}
	res, err := s.eng.Preview(c.ctx(), b)
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, PreviewOutcome{Source: b.Source, Result: c.visibleResult(res)})
}

// --------------------------------------------------------------- proposals

var proposalStatuses = []string{"pending", "applied", "rejected", "superseded"}

func proposalID(c *call) (int64, error) {
	id, err := strconv.ParseInt(c.r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, badRequest("id must be a positive integer")
	}
	return id, nil
}

// proposalView is the proposal with what the caller may not see removed.
func (c *call) proposalView(p *reconcile.Proposal) *reconcile.Proposal {
	v := *p
	v.Batch = redactBatch(p.Batch)
	v.Preview = c.visibleResult(p.Preview)
	return &v
}

func (s *Server) hProposals(c *call) error {
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	status := c.r.URL.Query().Get("status")
	if status != "" && !contains(proposalStatuses, status) {
		return badRequest("status must be one of %s", strings.Join(proposalStatuses, ", "))
	}
	const scan = 500 // the engine's own maximum
	all, err := s.eng.ListProposals(c.ctx(), status, scan)
	if err != nil {
		return err
	}
	_, readsAll := c.readableTypes()
	out := []*reconcile.Proposal{}
	for _, p := range all {
		if !readsAll {
			if _, ok := c.allowTypes(ActionRead, batchTypes(p.Batch)); !ok {
				continue
			}
		}
		v := c.proposalView(p)
		v.Preview = nil
		out = append(out, v)
	}
	return c.json(http.StatusOK, pageOf(out, pp, len(all) >= scan))
}

// loadProposal returns the proposal after checking that the caller holds
// action on every type its batch touches.
func (c *call) loadProposal(action string) (*reconcile.Proposal, error) {
	id, err := proposalID(c)
	if err != nil {
		return nil, err
	}
	p, err := c.s.eng.GetProposal(c.ctx(), id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("no proposal %d", id)
	}
	if err != nil {
		return nil, err
	}
	if t, ok := c.allowTypes(action, batchTypes(p.Batch)); !ok {
		return nil, forbidden("the %q action is not granted on %s objects", action, t)
	}
	return p, nil
}

func (s *Server) hProposal(c *call) error {
	p, err := c.loadProposal(ActionRead)
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, c.proposalView(p))
}

// DecisionOutcome is the answer to approving or rejecting a proposal.
type DecisionOutcome struct {
	ProposalID int64 `json:"proposal_id"`
	// Status is "applied" or "rejected".
	Status string            `json:"status"`
	Result *reconcile.Result `json:"result,omitempty"`
}

func (s *Server) hApprove(c *call) error {
	c.audited("proposal.approve", c.r.PathValue("id"), nil)
	p, err := c.loadProposal(ActionApprove)
	if err != nil {
		return err
	}
	c.auditDetails = map[string]any{"proposal_source": p.Source}
	res, err := s.eng.Approve(c.ctx(), p.ID, c.p.Subject)
	if err != nil {
		return err
	}
	c.auditDetails["changeset_id"] = res.ChangesetID
	return c.json(http.StatusOK, DecisionOutcome{ProposalID: p.ID, Status: "applied", Result: c.visibleResult(res)})
}

func (s *Server) hReject(c *call) error {
	c.audited("proposal.reject", c.r.PathValue("id"), nil)
	p, err := c.loadProposal(ActionApprove)
	if err != nil {
		return err
	}
	c.auditDetails = map[string]any{"proposal_source": p.Source}
	if err := s.eng.Reject(c.ctx(), p.ID, c.p.Subject); err != nil {
		return err
	}
	return c.json(http.StatusOK, DecisionOutcome{ProposalID: p.ID, Status: "rejected"})
}

// ----------------------------------------------------------- discrepancies

var discrepancyStatuses = []string{"open", "resolved", "accepted"}

func discrepancyView(d reconcile.Discrepancy) reconcile.Discrepancy {
	if Sensitive(d.Key) {
		d.Declared, d.Observed, d.Details = nil, nil, nil
	}
	return d
}

func (s *Server) hDiscrepancies(c *call) error {
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	v := c.r.URL.Query()
	f := reconcile.DiscrepancyFilter{Status: v.Get("status"), Kind: v.Get("kind")}
	if f.Status != "" && !contains(discrepancyStatuses, f.Status) {
		return badRequest("status must be one of %s", strings.Join(discrepancyStatuses, ", "))
	}
	if o := v.Get("object"); o != "" {
		if f.ObjectID, err = parseUUID(o, "object"); err != nil {
			return err
		}
	}
	page, err := c.discrepancies(f, pp)
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, page)
}

// discrepancies lists the discrepancies matching f that the caller may see, as one page.
func (c *call) discrepancies(f reconcile.DiscrepancyFilter, pp pageParams) (Page[reconcile.Discrepancy], error) {
	s := c.s
	if _, readsAll := c.readableTypes(); readsAll {
		f.Limit, f.Offset = pp.Limit, pp.Offset
		items, total, err := reconcile.ListDiscrepancies(c.ctx(), s.st, f)
		if err != nil {
			return Page[reconcile.Discrepancy]{}, err
		}
		out := make([]reconcile.Discrepancy, 0, len(items))
		for _, d := range items {
			out = append(out, discrepancyView(d))
		}
		return Page[reconcile.Discrepancy]{Total: total, Limit: pp.Limit, Offset: pp.Offset, Items: out}, nil
	}
	// Some objects are off limits: read what the scan bound allows, keep what
	// the caller may see, then page, so that the total is exact.
	var all []reconcile.Discrepancy
	truncated := false
	for off := 0; ; off += MaxLimit {
		f.Limit, f.Offset = MaxLimit, off
		items, total, err := reconcile.ListDiscrepancies(c.ctx(), s.st, f)
		if err != nil {
			return Page[reconcile.Discrepancy]{}, err
		}
		all = append(all, items...)
		if off+MaxLimit >= total {
			break
		}
		if len(all) >= maxScan {
			truncated = true
			break
		}
	}
	seen := map[string]bool{}
	var ids []string
	for _, d := range all {
		for _, id := range []string{d.ObjectID, d.RelatedID} {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	refs, err := c.visibleRefs(ids)
	if err != nil {
		return Page[reconcile.Discrepancy]{}, err
	}
	out := []reconcile.Discrepancy{}
	for _, d := range all {
		if _, ok := refs[d.ObjectID]; d.ObjectID != "" && !ok {
			continue
		}
		if _, ok := refs[d.RelatedID]; d.RelatedID != "" && !ok {
			continue
		}
		out = append(out, discrepancyView(d))
	}
	return pageOf(out, pp, truncated), nil
}

// AcceptOutcome is the answer to accepting a discrepancy.
type AcceptOutcome struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

func (s *Server) hAccept(c *call) error {
	c.audited("discrepancy.accept", c.r.PathValue("id"), nil)
	id, err := strconv.ParseInt(c.r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return badRequest("id must be a positive integer")
	}
	var in AcceptInput
	raw, err := io.ReadAll(c.r.Body)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := decodeStrict(bytes.NewReader(raw), &in); err != nil {
			return err
		}
	}
	if len(in.Note) > 1000 {
		return badRequest("note is limited to 1000 characters")
	}
	var objID, relID *string
	err = s.st.Pool.QueryRow(c.ctx(), `SELECT object_id::text, related_id::text FROM discrepancies WHERE id = $1`, id).Scan(&objID, &relID)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("no discrepancy %d", id)
	}
	if err != nil {
		return err
	}
	var ids []string
	for _, p := range []*string{objID, relID} {
		if p != nil {
			ids = append(ids, *p)
		}
	}
	if err := c.allowObjects(ActionApprove, ids); err != nil {
		return err
	}
	if err := reconcile.AcceptDiscrepancy(c.ctx(), s.st, id, c.p.Subject, in.Note); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return notFound("no open discrepancy %d", id)
		}
		return err
	}
	return c.json(http.StatusOK, AcceptOutcome{ID: id, Status: "accepted"})
}

// allowObjects requires action on each of the existing objects (type and site).
func (c *call) allowObjects(action string, ids []string) error {
	objs, err := c.s.st.Objects(c.ctx(), ids)
	if err != nil {
		return err
	}
	oids := make([]string, 0, len(objs))
	for _, o := range objs {
		oids = append(oids, o.ID)
	}
	sites, err := c.s.siteIDs(c.ctx(), oids)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if !c.s.auth.Allow(c.p, action, o.Type, sites[o.ID]) {
			return forbidden("the %q action is not granted on %s objects here", action, o.Type)
		}
	}
	return nil
}

// ----------------------------------------------------------- saved queries

var savedNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,99}$`)

func (s *Server) hQueries(c *call) error {
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	all, err := s.searcher.SavedQueries(c.ctx())
	if err != nil {
		return err
	}
	if all == nil {
		all = []search.Saved{}
	}
	return c.json(http.StatusOK, pageOf(all, pp, false))
}

func (s *Server) hQuery(c *call) error {
	q, err := s.searcher.SavedQuery(c.ctx(), c.r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) {
		return notFound("no saved query %q", c.r.PathValue("name"))
	}
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, q)
}

func (s *Server) hQueryCreate(c *call) error {
	var in SavedQueryInput
	c.audited("query.create", "", nil)
	if err := decodeStrict(c.r.Body, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	c.audited("query.create", in.Name, nil)
	if !savedNameRe.MatchString(in.Name) {
		return badRequest("name must be 1 to 100 characters: letters, digits, space, dot, dash, underscore")
	}
	parsed, err := tql.Parse(in.Query)
	if err != nil {
		return err
	}
	for _, f := range parsed.Fields() {
		if Sensitive(f) {
			return newErr(http.StatusForbidden, "sensitive-attribute", "the attribute %q is not exposed and cannot be queried", f)
		}
	}
	if hasSensitiveHas(parsed.Where) {
		return newErr(http.StatusForbidden, "sensitive-attribute", "a queried attribute is not exposed")
	}
	if len(in.Description) > 1000 {
		return badRequest("description is limited to 1000 characters")
	}
	if _, err := s.searcher.SavedQuery(c.ctx(), in.Name); err == nil {
		return newErr(http.StatusConflict, "conflict", "a saved query named %q exists; delete it first", in.Name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	// The owner is the caller, whatever the body says (the body has no owner field).
	if err := s.searcher.Save(c.ctx(), search.Saved{Name: in.Name, Query: in.Query, Description: in.Description, Owner: c.p.Subject}); err != nil {
		return err
	}
	saved, err := s.searcher.SavedQuery(c.ctx(), in.Name)
	if err != nil {
		return err
	}
	return c.json(http.StatusCreated, saved)
}

func (s *Server) hQueryDelete(c *call) error {
	name := c.r.PathValue("name")
	c.audited("query.delete", name, nil)
	q, err := s.searcher.SavedQuery(c.ctx(), name)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("no saved query %q", name)
	}
	if err != nil {
		return err
	}
	if q.Owner != c.p.Subject && !s.auth.Allow(c.p, ActionAdmin, "", "") {
		return forbidden("only the owner or an administrator may delete this saved query")
	}
	if err := s.searcher.DeleteSavedQuery(c.ctx(), name); err != nil {
		return err
	}
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}

// ------------------------------------------------------------------ health

var urlCredsRe = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s@]+@`)

// scrubError removes credentials a collector may have left in an error text
// ("https://user:pass@host/..." becomes "https://***@host/...").
func scrubError(s string) string { return urlCredsRe.ReplaceAllString(s, "${1}***@") }

func (s *Server) hCollectionHealth(c *call) error {
	pp, err := parsePage(c.r)
	if err != nil {
		return err
	}
	all, err := s.sourceHealth(c.ctx())
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, pageOf(all, pp, false))
}

// sourceHealth reads the last run of every source, with the credentials
// a collector may have left in a text cleaned out, and names the module each
// source belongs to when the binary has one for its prefix.
func (s *Server) sourceHealth(ctx context.Context) ([]SourceHealth, error) {
	rows, err := s.st.Pool.Query(ctx, `SELECT source, collector, target, version, ok, last_attempt, last_success, last_error,
		duration_ms, items, warnings, failures FROM source_health ORDER BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	mods := moduleIndex(s.collectorSource())
	all := []SourceHealth{}
	for rows.Next() {
		var h SourceHealth
		var warnings []byte
		if err := rows.Scan(&h.Source, &h.Collector, &h.Target, &h.Version, &h.OK, &h.LastAttempt, &h.LastSuccess, &h.LastError,
			&h.DurationMS, &h.Items, &warnings, &h.Failures); err != nil {
			return nil, err
		}
		h.Module = moduleOf(h.Source, mods)
		h.LastError = scrubError(h.LastError)
		_ = json.Unmarshal(warnings, &h.Warnings)
		for i, w := range h.Warnings {
			h.Warnings[i] = scrubError(w)
		}
		if h.Warnings == nil {
			h.Warnings = []string{}
		}
		all = append(all, h)
	}
	return all, rows.Err()
}

// Status is the body of the probes.
type Status struct {
	Status string `json:"status"`
}

func (s *Server) hHealthz(c *call) error { return c.json(http.StatusOK, Status{Status: "ok"}) }

func (s *Server) hReadyz(c *call) error {
	ctx, cancel := context.WithTimeout(c.ctx(), 2*time.Second)
	defer cancel()
	if err := s.st.Pool.Ping(ctx); err != nil {
		s.log.Warn("api: not ready", "error", err)
		return newErr(http.StatusServiceUnavailable, "not-ready", "the database does not answer")
	}
	return c.json(http.StatusOK, Status{Status: "ready"})
}
