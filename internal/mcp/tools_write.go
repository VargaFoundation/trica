package mcp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

const (
	descPropose = `Propose a change to the inventory: new objects, new values, relations to open or close. ` +
		`Nothing is applied: the proposal is queued and a person approves or rejects it in trica; you get its id and a preview of what it would change. ` +
		`Use it to document planned work ("a new server in rack 2 at U10") or to correct data you were told about. ` +
		`Objects are designated by type and identity (serial, name, ...), exactly as a collector's batch: ` +
		`{"objects":[{"ref":"srv","type":"device","identity":{"name":"srv-new"},"attributes":{"role":"server"}}],` +
		`"relations":[{"type":"mounted_in","from":{"ref":"srv"},"to":{"type":"rack","identity":{"name":"rack 2"}},"attributes":{"position":10}}],"reason":"..."}. ` +
		`An existing object is designated by identity {"id":"<uuid>"}. Search first so that you do not propose what already exists.`
	maxProposed = 200
	maxReason   = 500
)

// ProposeInput is the input of propose_change: the content of an sdk batch
// (the document a collector submits) and the reason for it. The batch's source,
// scope and confidence are not the caller's to choose, so they are not offered.
//
// The types below mirror sdk.Object, sdk.Relation and sdk.Endpoint without
// their recursion (an endpoint's parent is itself an endpoint in the sdk): a
// tool's input schema is a finite document that an assistant reads, and it
// cannot describe a type that contains itself.
type ProposeInput struct {
	Reason      string              `json:"reason" jsonschema:"why: what this change documents, for the person who reviews it (at most 500 characters)"`
	Objects     []*ProposedObject   `json:"objects,omitempty" jsonschema:"objects to create or update, designated by type and identity"`
	Relations   []*ProposedRelation `json:"relations,omitempty" jsonschema:"relations to open"`
	Unrelations []*ProposedRelation `json:"unrelations,omitempty" jsonschema:"relations to close"`
}

// ProposedObject is an object of a proposal (sdk.Object).
type ProposedObject struct {
	Ref        string            `json:"ref" jsonschema:"a name local to this proposal, used by relations to point at the object"`
	Type       string            `json:"type" jsonschema:"object type, e.g. device, interface, rack (see describe_schema)"`
	Identity   map[string]string `json:"identity" jsonschema:"what recognises the object: serial, name, mac, ... or id for an existing object"`
	Parent     string            `json:"parent,omitempty" jsonschema:"ref of the object that contains this one in the proposal (an interface's device)"`
	Attributes map[string]any    `json:"attributes,omitempty" jsonschema:"attribute values, e.g. role, vendor"`
}

// ProposedEndpoint is one end of a relation (sdk.Endpoint): an object of the
// proposal by ref, or an existing object by type and identity.
type ProposedEndpoint struct {
	Ref      string            `json:"ref,omitempty" jsonschema:"the ref of an object of this proposal"`
	Type     string            `json:"type,omitempty" jsonschema:"type of an existing object"`
	Identity map[string]string `json:"identity,omitempty" jsonschema:"identity of an existing object, or id"`
	Parent   *ProposedParent   `json:"parent,omitempty" jsonschema:"the existing object that scopes a weak identity (an interface's device)"`
}

// ProposedParent scopes an endpoint's identity: the device of a port, found by type and identity.
type ProposedParent struct {
	Ref      string            `json:"ref,omitempty"`
	Type     string            `json:"type,omitempty"`
	Identity map[string]string `json:"identity,omitempty"`
}

// ProposedRelation is a relation of a proposal (sdk.Relation).
type ProposedRelation struct {
	Type       string           `json:"type" jsonschema:"relation type, e.g. mounted_in, cabled_to (see describe_schema)"`
	From       ProposedEndpoint `json:"from"`
	To         ProposedEndpoint `json:"to"`
	Attributes map[string]any   `json:"attributes,omitempty" jsonschema:"relation attributes, e.g. position and face for mounted_in"`
}

func (e ProposedEndpoint) endpoint() sdk.Endpoint {
	out := sdk.Endpoint{Ref: e.Ref, Type: e.Type, Identity: e.Identity}
	if e.Parent != nil {
		out.Parent = &sdk.Endpoint{Ref: e.Parent.Ref, Type: e.Parent.Type, Identity: e.Parent.Identity}
	}
	return out
}

func sdkRelations(in []*ProposedRelation) ([]*sdk.Relation, error) {
	out := make([]*sdk.Relation, 0, len(in))
	for _, r := range in {
		if r == nil {
			return nil, api.BadRequest("a batch relation is null")
		}
		out = append(out, &sdk.Relation{Type: r.Type, From: r.From.endpoint(), To: r.To.endpoint(), Attributes: r.Attributes})
	}
	return out, nil
}

// ProposeOutcome is the answer to propose_change.
type ProposeOutcome struct {
	Status     string            `json:"status"` // always "proposed"
	ProposalID int64             `json:"proposal_id"`
	Source     string            `json:"source"`
	Reason     string            `json:"reason"`
	Preview    *reconcile.Result `json:"preview"`
	Note       string            `json:"note"`
}

// proposalSource is the source a proposal is attributed to: always an mcp:
// source, so that the policy and the audit trail recognise it (ADR 0009). A
// token bound to an mcp: source keeps it; any other principal (a person on
// stdio, a collector's token) speaks as mcp:<subject>.
func proposalSource(p api.Principal) string {
	if src := p.BatchSource(); strings.HasPrefix(src, "mcp:") && len(src) > len("mcp:") {
		return src
	}
	return "mcp:" + p.Subject
}

// checkEndpoint refuses a relation end the caller may not propose on: an
// existing object is designated by type (the right is per type), an object of
// the proposal by ref (already covered by the batch check).
func (t *tc) checkEndpoint(e ProposedEndpoint) error {
	if e.Ref == "" && e.Type == "" {
		return api.BadRequest("a relation end needs a ref (an object of this proposal) or a type and an identity")
	}
	if e.Type != "" && !t.g.Allow(api.ActionPropose, e.Type, "") {
		return api.Forbidden("proposing changes to %s objects is not granted", e.Type)
	}
	if e.Parent != nil && e.Parent.Type != "" && !t.g.Allow(api.ActionPropose, e.Parent.Type, "") {
		return api.Forbidden("proposing changes to %s objects is not granted", e.Parent.Type)
	}
	return nil
}

func (s *Server) toolPropose(t *tc, in ProposeInput) (any, error) {
	// The write tool is only offered to callers who hold "propose", but the
	// right is checked again here, at the moment of writing.
	if !t.g.Allow(api.ActionPropose, "", "") {
		return nil, api.Forbidden("the %q action is not granted", api.ActionPropose)
	}
	reason := strings.TrimSpace(in.Reason)
	switch {
	case reason == "":
		return nil, api.BadRequest("a reason is required: say what this change documents")
	case len(reason) > maxReason:
		return nil, api.BadRequest("the reason is limited to %d characters", maxReason)
	case len(in.Objects)+len(in.Relations)+len(in.Unrelations) == 0:
		return nil, api.BadRequest("the proposal is empty")
	case len(in.Objects) > maxProposed || len(in.Relations) > maxProposed || len(in.Unrelations) > maxProposed:
		return nil, api.BadRequest("a proposal holds at most %d objects, %d relations and %d unrelations", maxProposed, maxProposed, maxProposed)
	}
	objects := make([]*sdk.Object, 0, len(in.Objects))
	for _, o := range in.Objects {
		if o == nil {
			return nil, api.BadRequest("a batch object is null")
		}
		objects = append(objects, &sdk.Object{Ref: o.Ref, Type: o.Type, Identity: o.Identity, Parent: o.Parent, Attributes: o.Attributes})
	}
	relations, err := sdkRelations(in.Relations)
	if err != nil {
		return nil, err
	}
	unrelations, err := sdkRelations(in.Unrelations)
	if err != nil {
		return nil, err
	}
	// A proposal says what a person declares: partial (it withdraws nothing
	// by omission) and declared, whatever the caller would like. The inputs
	// above have no field for either.
	b := &sdk.Batch{
		Source: proposalSource(t.p), Scope: sdk.ScopePartial, ObservedAt: s.now().UTC(), Confidence: sdk.Declared,
		Objects: objects, Relations: relations, Unrelations: unrelations,
	}
	t.details["source"] = b.Source
	if err := t.g.CheckBatch(b, api.ActionPropose); err != nil {
		return nil, err
	}
	// CheckBatch rights cover the objects of the batch; a relation also touches
	// the objects at its ends, which the caller must be allowed to propose on.
	for _, r := range append(slices.Clone(in.Relations), in.Unrelations...) {
		for _, e := range []ProposedEndpoint{r.From, r.To} {
			if err := t.checkEndpoint(e); err != nil {
				return nil, err
			}
		}
	}
	// Engine.Propose, never Submit: whatever the policy says, MCP can only
	// queue. A policy that forgot to list mcp:* would otherwise let Submit apply.
	res, err := s.eng.Propose(t.ctx, b, t.p.Subject, reason)
	if err != nil {
		return nil, err
	}
	t.target = fmt.Sprintf("proposal:%d", res.ProposalID)
	t.details["proposal_id"] = res.ProposalID
	return &ProposeOutcome{Status: "proposed", ProposalID: res.ProposalID, Source: b.Source, Reason: reason, Preview: t.g.VisibleResult(res),
		Note: "Queued for review: nothing has been applied. A person approves or rejects the proposal in trica."}, nil
}
