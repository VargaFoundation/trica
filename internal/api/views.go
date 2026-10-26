package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Pagination bounds. A page is `limit` items from `offset`; the body always
// says how many items match in total.
const (
	DefaultLimit = 50
	MaxLimit     = 1000
	// maxScan bounds what a handler reads from the store when the store has
	// no offset of its own (history, changes, proposals, who-had). A result
	// that hits it says "truncated": true.
	maxScan = 5000
)

type pageParams struct{ Limit, Offset int }

func parsePage(r *http.Request) (pageParams, error) {
	pp := pageParams{Limit: DefaultLimit}
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxLimit {
			return pp, badRequest("limit must be an integer between 1 and %d", MaxLimit)
		}
		pp.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return pp, badRequest("offset must be a non-negative integer")
		}
		pp.Offset = n
	}
	return pp, nil
}

// Page is the envelope of every list.
type Page[T any] struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	// Truncated: the server stopped reading at its scan bound; Total is then a lower bound.
	Truncated bool `json:"truncated,omitempty"`
	Items     []T  `json:"items"`
}

// pageOf slices an already filtered list.
func pageOf[T any](all []T, pp pageParams, truncated bool) Page[T] {
	items := []T{}
	if pp.Offset < len(all) {
		items = all[pp.Offset:min(len(all), pp.Offset+pp.Limit)]
	}
	return Page[T]{Total: len(all), Limit: pp.Limit, Offset: pp.Offset, Truncated: truncated, Items: items}
}

// ObjectView is an object as the API shows it.
type ObjectView struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	ParentID *string `json:"parent_id,omitempty"`
	// Parent names the parent object on a list item, when the caller may read
	// it: what a component is part of (a cpu, a disk: its server).
	Parent *ObjectRef `json:"parent,omitempty"`
	// SiteID is the id of the site the object is in (itself for a site).
	SiteID     string         `json:"site_id,omitempty"`
	Display    string         `json:"display"`
	Status     string         `json:"status"`
	Attributes map[string]any `json:"attributes"`
	CreatedAt  time.Time      `json:"created_at"`
	FirstSeen  *time.Time     `json:"first_seen,omitempty"`
	LastSeen   *time.Time     `json:"last_seen,omitempty"`
	Observed   bool           `json:"observed"`
	// AsOf is set when the attributes, status, display and parent are those of
	// that date, not the current ones.
	AsOf *time.Time `json:"as_of,omitempty"`
}

func objectView(o *store.Object) ObjectView {
	return ObjectView{
		ID: o.ID, Type: o.Type, ParentID: o.ParentID, Display: o.Display, Status: o.Status,
		Attributes: redactMap(o.Attrs), CreatedAt: o.CreatedAt, FirstSeen: o.FirstSeen, LastSeen: o.LastSeen, Observed: o.Observed,
	}
}

func objectViews(objs []*store.Object) []ObjectView {
	out := make([]ObjectView, 0, len(objs))
	for _, o := range objs {
		out = append(out, objectView(o))
	}
	return out
}

// SearchSummary is what a search says first: how many, of which types.
type SearchSummary struct {
	Query  string         `json:"query"`
	Total  int            `json:"total"`
	ByType map[string]int `json:"by_type"`
	// Fuzzy: nothing matched exactly and the items are near matches (typos).
	Fuzzy  bool  `json:"fuzzy"`
	TookMS int64 `json:"took_ms"`
}

// SearchPage is a counted summary, then a page of objects.
type SearchPage struct {
	Summary SearchSummary `json:"summary"`
	Total   int           `json:"total"`
	Limit   int           `json:"limit"`
	Offset  int           `json:"offset"`
	Items   []ObjectView  `json:"items"`
}

// ProvenanceEntry is one attribute with where it comes from.
type ProvenanceEntry struct {
	Key        string `json:"key"`
	Value      any    `json:"value"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
	Author     string `json:"author,omitempty"`
	// ValidFrom is when the attribute took this value.
	ValidFrom time.Time `json:"valid_from"`
	// FirstSeen and LastSeen are those of the winning source's claim.
	FirstSeen *time.Time `json:"first_seen,omitempty"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
	// OtherClaims are what the other sources currently say on the attribute.
	OtherClaims []store.Claim `json:"other_claims,omitempty"`
}

// Provenance is the provenance of every attribute of an object.
type Provenance struct {
	ObjectID   string            `json:"object_id"`
	Attributes []ProvenanceEntry `json:"attributes"`
}

// ObjectRef names another object.
type ObjectRef struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Display string `json:"display"`
}

// RelationView is a relation seen from one object.
type RelationView struct {
	store.Relation
	// Direction is "out" when the object is the From end, "in" when it is the To end.
	Direction string    `json:"direction"`
	Other     ObjectRef `json:"other"`
}

// RelationEnd is one end of a relation listed in bulk: the object, and where
// it stands, so that a client can group without one more request per object.
type RelationEnd struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Display string `json:"display"`
	// ParentID is the object's parent (an interface's device).
	ParentID string `json:"parent_id,omitempty"`
	// SiteID is the site the object is in, when it has exactly one.
	SiteID string `json:"site_id,omitempty"`
}

// RelationLink is a relation with both of its ends named.
type RelationLink struct {
	store.Relation
	From RelationEnd `json:"from"`
	To   RelationEnd `json:"to"`
}

// RackItem is one device mounted in a rack, with what an elevation needs.
type RackItem struct {
	Device ObjectRef `json:"device"`
	// Position is the lowest U the device occupies (1 is the bottom), when known.
	Position *float64 `json:"position,omitempty"`
	// Face is front or rear, when known.
	Face string `json:"face,omitempty"`
	// Model is the device's model (its instance_of relation), when it has one
	// the caller may read; UHeight and FullDepth come from it. When the model
	// is unknown or has no height, UHeight is the device's own u_height.
	Model     *ObjectRef `json:"model,omitempty"`
	UHeight   *float64   `json:"u_height,omitempty"`
	FullDepth bool       `json:"full_depth"`
	// ModelName is the model's commercial name (its model attribute).
	ModelName string `json:"model_name,omitempty"`
	// Role is the device's role (server, switch, firewall...), when known:
	// what a face is drawn as when the model has no image.
	Role string `json:"role,omitempty"`
	// Health is the device's own health (ok, warning, critical, unknown).
	Health string `json:"health,omitempty"`
	// PartsHealth is the worst health among the device's parts the caller
	// may read (power supplies, fans, disks, modules and their own parts),
	// counting ok, warning and critical only; absent parts are left out.
	PartsHealth string `json:"parts_health,omitempty"`
	// FrontImage and RearImage are the API paths of the model's face images,
	// when the device-type library has them (GET /device-types/{slug}/{face}).
	FrontImage string `json:"front_image,omitempty"`
	RearImage  string `json:"rear_image,omitempty"`
	// VariantUncertain says the library is not sure which chassis the device
	// is and nobody declared it; Variants is the number of models it may be
	// (GET /objects/{id}/device-type-candidates lists them, ADR 0048).
	VariantUncertain bool `json:"variant_uncertain,omitempty"`
	Variants         int  `json:"variants,omitempty"`
	// Airflow is the device's airflow check against the rack's aisles: does
	// it take its air in from the cold aisle (ADR 0049).
	Airflow *AirflowCheck `json:"airflow,omitempty"`
}

// ChangeView is a change with its object's rights already checked.
type ChangeView = store.Change

// WhoHadItem is one period during which an object had a value.
type WhoHadItem struct {
	Object ObjectRef `json:"object"`
	store.Value
}

// BatchOutcome is the answer to a submitted batch.
type BatchOutcome struct {
	// Source is the source the batch was attributed to: the one the server
	// derived from the credential, whatever the body said.
	Source string `json:"source"`
	// Status is "applied" or "proposed" (the policy queued it for approval).
	Status string            `json:"status"`
	Result *reconcile.Result `json:"result"`
}

// PreviewOutcome is the answer to a batch preview.
type PreviewOutcome struct {
	Source string            `json:"source"`
	Result *reconcile.Result `json:"result"`
}

// ProposalView is a proposal as the API shows it.
type ProposalView = reconcile.Proposal

// SourceHealth is one row of the collection health board.
type SourceHealth struct {
	Source string `json:"source"`
	// Module is the kind of the collector module the source belongs to (its
	// prefix), when this binary registers one: the key of GET /collectors.
	Module      string     `json:"module,omitempty"`
	Collector   string     `json:"collector"`
	Target      string     `json:"target"`
	Version     string     `json:"version,omitempty"`
	OK          bool       `json:"ok"`
	LastAttempt time.Time  `json:"last_attempt"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	DurationMS  int64      `json:"duration_ms"`
	Items       int        `json:"items"`
	Warnings    []string   `json:"warnings"`
	Failures    int        `json:"failures"`
}

// SavedQueryInput is the body that creates a saved query.
type SavedQueryInput struct {
	Name        string `json:"name"`
	Query       string `json:"query"`
	Description string `json:"description,omitempty"`
}

// AcceptInput is the optional body of a discrepancy acceptance.
type AcceptInput struct {
	Note string `json:"note,omitempty"`
}

// SchemaDoc is the registry as the API shows it.
type SchemaDoc struct {
	Types     []TypeDoc     `json:"types"`
	Relations []RelationDoc `json:"relations"`
}

// TypeDoc describes an object type.
type TypeDoc struct {
	Name        string          `json:"name"`
	Layer       string          `json:"layer"`
	Label       schema.Label    `json:"label"`
	Description string          `json:"description"`
	Display     string          `json:"display"`
	Parents     []string        `json:"parents"`
	Custom      bool            `json:"custom"`
	Attributes  []AttributeDoc  `json:"attributes"`
	Identity    IdentityKeysDoc `json:"identity"`
}

// IdentityKeysDoc lists the attributes that identify objects of a type.
type IdentityKeysDoc struct {
	Strong []string `json:"strong"`
	Weak   []string `json:"weak"`
}

// AttributeDoc describes an attribute.
type AttributeDoc struct {
	Name        string                  `json:"name"`
	Kind        schema.Kind             `json:"kind"`
	Label       schema.Label            `json:"label"`
	Description string                  `json:"description"`
	Unit        string                  `json:"unit,omitempty"`
	Values      []string                `json:"values,omitempty"`
	Identity    schema.IdentityStrength `json:"identity,omitempty"`
	Search      bool                    `json:"search"`
	Required    bool                    `json:"required"`
	// Series: a measurement whose history GET /objects/{id}/series draws.
	Series bool `json:"series,omitempty"`
	Custom bool `json:"custom"`
}

// RelationDoc describes a relation type.
type RelationDoc struct {
	Name        string         `json:"name"`
	Label       schema.Label   `json:"label"`
	Inverse     schema.Label   `json:"inverse"`
	Description string         `json:"description"`
	From        []string       `json:"from"`
	To          []string       `json:"to"`
	Impact      string         `json:"impact,omitempty"`
	Attributes  []AttributeDoc `json:"attributes"`
	Custom      bool           `json:"custom"`
}

func attributeDocs(attrs map[string]*schema.Attribute) []AttributeDoc {
	out := []AttributeDoc{}
	for _, n := range sdk.SortedKeys(attrs) {
		a := attrs[n]
		if Sensitive(n) {
			continue
		}
		out = append(out, AttributeDoc{Name: n, Kind: a.Kind, Label: a.Label, Description: a.Description, Unit: a.Unit,
			Values: a.Values, Identity: a.Identity, Search: a.Search, Required: a.Required, Series: a.Series, Custom: a.Custom})
	}
	return out
}

func typeDoc(t *schema.Type) TypeDoc {
	strong, weak := t.IdentityKeys()
	return TypeDoc{Name: t.Name, Layer: t.Layer, Label: t.Label, Description: t.Description, Display: t.Display,
		Parents: nonNil(t.Parents), Custom: t.Custom, Attributes: attributeDocs(t.Attributes),
		Identity: IdentityKeysDoc{Strong: nonNil(withoutSensitive(strong)), Weak: nonNil(withoutSensitive(weak))}}
}

func withoutSensitive(in []string) []string {
	var out []string
	for _, s := range in {
		if !Sensitive(s) {
			out = append(out, s)
		}
	}
	return out
}

func relationDoc(rt *schema.RelationType) RelationDoc {
	return RelationDoc{Name: rt.Name, Label: rt.Label, Inverse: rt.Inverse, Description: rt.Description,
		From: nonNil(rt.From), To: nonNil(rt.To), Impact: rt.Impact, Attributes: attributeDocs(rt.Attributes), Custom: rt.Custom}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
