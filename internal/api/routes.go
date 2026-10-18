package api

import (
	"net/http"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// route is one endpoint. The table of routes is the single source of the
// mux registrations and of the OpenAPI document: an endpoint that is not in
// the table does not exist, and an endpoint in the table is documented.
type route struct {
	Method string
	// Path is relative to Prefix; {name} are path parameters.
	Path string
	// ID is the OpenAPI operationId, unique.
	ID      string
	Tag     string
	Summary string
	// Action is the right the caller needs (ActionRead...). Empty means the
	// endpoint is public (probes only).
	Action string
	// Mutating routes are audited by wrap.
	Mutating bool
	// Audit is the audit action name of a mutating route.
	Audit  string
	Params []param
	// Body is a zero value of the request body type, or nil.
	Body any
	// BodyOptional says the request may carry no body at all.
	BodyOptional bool
	// Responses are the success answers; the first is the main one.
	Responses []response
	// Errors lists the problem statuses beyond the standard 500 (and 401 and
	// 403 for protected routes).
	Errors []int
	H      handler
}

// param is a path or query parameter.
type param struct {
	Name     string
	In       string // "path" or "query"
	Desc     string
	Required bool
	// Schema is the JSON schema of the value; the marker enumTypes is replaced
	// by the registry's type names when the document is generated.
	Schema map[string]any
}

// response is a success answer.
type response struct {
	Status int
	Desc   string
	// Body is a zero value of the body type; nil means no body.
	Body any
	// Binary lists the media types of a body that is not JSON (an image).
	Binary []string
}

// enumTypes marks a schema whose enum is the registry's object types.
const enumTypes = "x-trica-enum-types"

func pathUUID(name, desc string) param {
	return param{Name: name, In: "path", Desc: desc, Required: true, Schema: map[string]any{"type": "string", "format": "uuid"}}
}

func pathInt(name, desc string) param {
	return param{Name: name, In: "path", Desc: desc, Required: true, Schema: map[string]any{"type": "integer", "minimum": 1}}
}

func qStr(name, desc string, required bool) param {
	return param{Name: name, In: "query", Desc: desc, Required: required, Schema: map[string]any{"type": "string"}}
}

func qEnum(name, desc string, values ...string) param {
	vals := make([]any, len(values))
	for i, v := range values {
		vals[i] = v
	}
	return param{Name: name, In: "query", Desc: desc, Schema: map[string]any{"type": "string", "enum": vals}}
}

func qUUID(name, desc string) param {
	return param{Name: name, In: "query", Desc: desc, Schema: map[string]any{"type": "string", "format": "uuid"}}
}

func qTime(name, desc string, required bool) param {
	return param{Name: name, In: "query", Desc: desc, Required: required, Schema: map[string]any{
		"type": "string", "description": "RFC 3339 time or YYYY-MM-DD date", "anyOf": []any{
			map[string]any{"format": "date-time"}, map[string]any{"format": "date"}}}}
}

// pagination parameters shared by every list.
var pagination = []param{
	{Name: "limit", In: "query", Desc: "Page size, 1 to 1000 (default 50).", Schema: map[string]any{"type": "integer", "minimum": 1, "maximum": MaxLimit, "default": DefaultLimit}},
	{Name: "offset", In: "query", Desc: "Items to skip (default 0).", Schema: map[string]any{"type": "integer", "minimum": 0, "default": 0}},
}

func list(ps ...param) []param { return append(append([]param(nil), ps...), pagination...) }

func ok(body any) []response { return []response{{Status: http.StatusOK, Desc: "OK", Body: body}} }

// routeTable is the whole API.
func (s *Server) routeTable() []route {
	const (
		tSchema = "schema"
		tInv    = "inventory"
		tBatch  = "batches"
		tProp   = "proposals"
		tDisc   = "discrepancies"
		tSaved  = "saved queries"
		tOps    = "operations"
	)
	typeParam := param{Name: "type", In: "query", Desc: "Restrict to one object type.", Schema: map[string]any{"type": "string", enumTypes: true}}
	return []route{
		{Method: "GET", Path: "/healthz", ID: "getHealthz", Tag: tOps, Summary: "Liveness probe (public).",
			Responses: ok(Status{}), H: s.hHealthz},
		{Method: "GET", Path: "/readyz", ID: "getReadyz", Tag: tOps, Summary: "Readiness probe: the database answers (public).",
			Responses: ok(Status{}), Errors: []int{http.StatusServiceUnavailable}, H: s.hReadyz},
		{Method: "GET", Path: "/openapi.json", ID: "getOpenAPI", Tag: tOps, Action: ActionRead,
			Summary:   "This document, generated from the route table and the schema registry.",
			Responses: ok(map[string]any{}), H: s.hOpenAPI},
		{Method: "GET", Path: "/me", ID: "getMe", Tag: tOps, Action: ActionRead,
			Summary:   "Who the caller is and which actions it holds (what a UI may offer; the server still checks every call).",
			Responses: ok(Me{}), H: s.hMe},
		{Method: "GET", Path: "/health/collection", ID: "getCollectionHealth", Tag: tOps, Action: ActionRead,
			Summary: "Collection health: the last run of every source.", Params: list(),
			Responses: ok(Page[SourceHealth]{}), Errors: []int{http.StatusBadRequest}, H: s.hCollectionHealth},

		{Method: "GET", Path: "/schema", ID: "getSchema", Tag: tSchema, Action: ActionRead,
			Summary:   "The schema registry: object types, attributes, relation types.",
			Responses: ok(SchemaDoc{}), H: s.hSchema},
		{Method: "GET", Path: "/schema/types/{type}", ID: "getSchemaType", Tag: tSchema, Action: ActionRead,
			Summary: "One object type.", Params: []param{{Name: "type", In: "path", Desc: "Object type name.", Required: true, Schema: map[string]any{"type": "string"}}},
			Responses: ok(TypeDoc{}), Errors: []int{http.StatusNotFound}, H: s.hSchemaType},

		{Method: "GET", Path: "/objects", ID: "listObjects", Tag: tInv, Action: ActionRead,
			Summary: "List objects, optionally filtered with a TQL query.",
			Params: list(qStr("q", "TQL query (see the query language reference).", false), typeParam,
				qStr("sort", "Comma-separated fields, a leading - sorts descending.", false)),
			Responses: ok(SearchPage{}), Errors: []int{http.StatusBadRequest}, H: s.hObjects},
		{Method: "GET", Path: "/objects/{id}", ID: "getObject", Tag: tInv, Action: ActionRead,
			Summary:   "One object, now or as of a date.",
			Params:    []param{pathUUID("id", "Object id."), qTime("at", "Show the state as of this time.", false)},
			Responses: ok(ObjectView{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hObject},
		{Method: "GET", Path: "/objects/{id}/provenance", ID: "getObjectProvenance", Tag: tInv, Action: ActionRead,
			Summary:   "Where each attribute of the object comes from.",
			Params:    []param{pathUUID("id", "Object id.")},
			Responses: ok(Provenance{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hProvenance},
		{Method: "GET", Path: "/objects/{id}/history", ID: "getObjectHistory", Tag: tInv, Action: ActionRead,
			Summary:   "The values the object's attributes had over time.",
			Params:    list(pathUUID("id", "Object id."), qStr("key", "Restrict to one attribute.", false)),
			Responses: ok(Page[store.Value]{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hHistory},
		{Method: "GET", Path: "/objects/{id}/relations", ID: "getObjectRelations", Tag: tInv, Action: ActionRead,
			Summary:   "The object's relations, now or as of a date.",
			Params:    list(pathUUID("id", "Object id."), qTime("at", "Show the relations as of this time.", false)),
			Responses: ok(Page[RelationView]{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hObjectRelations},
		{Method: "GET", Path: "/relations", ID: "listRelations", Tag: tInv, Action: ActionRead,
			Summary:   "The current relations of some types across the inventory, both ends named (the bulk read behind topology views). A relation is listed only if the caller may read both ends.",
			Params:    list(qStr("type", "Relation types, comma separated (at most 8), e.g. cabled_to,connects,neighbor_of.", true)),
			Responses: ok(Page[RelationLink]{}), Errors: []int{http.StatusBadRequest}, H: s.hRelations},
		{Method: "GET", Path: "/topology/live", ID: "getLiveTopology", Tag: tInv, Action: ActionRead,
			Summary:   "Live state of the links: throughput, link state and utilisation per interface id, read by the server from the configured metrics endpoint and cached (refresh_seconds). enabled is false when the server has no live block; stale is true when the last refresh failed and the values are the last good ones. Only interfaces the caller may read are listed. Nothing here is stored in the inventory.",
			Responses: ok(LiveTopology{}), H: s.hLiveTopology},
		{Method: "GET", Path: "/racks/{id}/contents", ID: "getRackContents", Tag: tInv, Action: ActionRead,
			Summary:   "The devices mounted in a rack, with position, face, model, height and depth: one call for an elevation. A device is listed only if the caller may read it, its model only if the caller may read the model. Ordered by position, unknown positions last.",
			Params:    list(pathUUID("id", "Rack id.")),
			Responses: ok(Page[RackItem]{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hRackContents},
		{Method: "GET", Path: "/device-types/{slug}/{face}", ID: "getDeviceTypeImage", Tag: tInv, Action: ActionRead,
			Summary: "The image of one face of a device model, from the device-type library or the operator's directory (devicetypes.extra_dir). Library content, not inventory data: any caller who may read gets it. Cached privately for an hour, then revalidated with the ETag. The rack contents name the images that exist.",
			Params: []param{
				{Name: "slug", In: "path", Desc: "Model slug (the device_type's slug).", Required: true, Schema: map[string]any{"type": "string", "pattern": "^[a-z0-9][a-z0-9._-]*$"}},
				{Name: "face", In: "path", Desc: "Face of the device.", Required: true, Schema: map[string]any{"type": "string", "enum": []any{"front", "rear"}}},
			},
			Responses: []response{
				{Status: http.StatusOK, Desc: "The image.", Binary: []string{"image/png", "image/jpeg", "image/webp"}},
				{Status: http.StatusNotModified, Desc: "The copy the caller holds (If-None-Match) is current."},
			},
			Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hDeviceTypeImage},
		{Method: "GET", Path: "/objects/{id}/timeline", ID: "getObjectTimeline", Tag: tInv, Action: ActionRead,
			Summary:   "The changes that touched the object.",
			Params:    list(pathUUID("id", "Object id."), qTime("from", "Start (default: the beginning).", false), qTime("to", "End (default: now).", false)),
			Responses: ok(Page[store.Change]{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hTimeline},

		{Method: "GET", Path: "/search", ID: "search", Tag: tInv, Action: ActionRead,
			Summary:   "Search: a counted summary first, then a page of objects.",
			Params:    list(qStr("q", "Free text or TQL query.", true)),
			Responses: ok(SearchPage{}), Errors: []int{http.StatusBadRequest}, H: s.hSearch},
		{Method: "GET", Path: "/whohad", ID: "whoHad", Tag: tInv, Action: ActionRead,
			Summary:   "Which objects had this value, now or at a date.",
			Params:    list(qStr("key", "Attribute name.", true), qStr("value", "Exact value.", true), qTime("at", "As of this time (default: any time).", false)),
			Responses: ok(Page[WhoHadItem]{}), Errors: []int{http.StatusBadRequest}, H: s.hWhoHad},
		{Method: "GET", Path: "/changes", ID: "listChanges", Tag: tInv, Action: ActionRead,
			Summary:   "The change feed between two times.",
			Params:    list(qTime("from", "Start.", true), qTime("to", "End (default: now).", false), qUUID("object", "Restrict to one object.")),
			Responses: ok(Page[store.Change]{}), Errors: []int{http.StatusBadRequest}, H: s.hChanges},

		{Method: "POST", Path: "/batches", ID: "submitBatch", Tag: tBatch, Action: ActionPropose, Mutating: true, Audit: "batch.submit",
			Summary: "Submit a batch. The source is derived from the credential, never from the body.",
			Params:  []param{qStr("reason", "Why (kept with a proposal).", false)},
			Body:    sdk.Batch{},
			Responses: []response{{Status: http.StatusOK, Desc: "Applied.", Body: BatchOutcome{}},
				{Status: http.StatusAccepted, Desc: "Queued as a proposal by the policy.", Body: BatchOutcome{}}},
			Errors: []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity}, H: s.hSubmit},
		{Method: "POST", Path: "/batches/preview", ID: "previewBatch", Tag: tBatch, Action: ActionPropose,
			Summary: "What a batch would change, without changing anything.",
			Body:    sdk.Batch{}, Responses: ok(PreviewOutcome{}),
			Errors: []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity}, H: s.hPreview},

		{Method: "GET", Path: "/proposals", ID: "listProposals", Tag: tProp, Action: ActionRead,
			Summary: "List proposals.", Params: list(qEnum("status", "Filter by status.", proposalStatuses...)),
			Responses: ok(Page[reconcile.Proposal]{}), Errors: []int{http.StatusBadRequest}, H: s.hProposals},
		{Method: "GET", Path: "/proposals/{id}", ID: "getProposal", Tag: tProp, Action: ActionRead,
			Summary:   "One proposal, with a preview against the current state while pending.",
			Params:    []param{pathInt("id", "Proposal id.")},
			Responses: ok(reconcile.Proposal{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hProposal},
		{Method: "POST", Path: "/proposals/{id}/approve", ID: "approveProposal", Tag: tProp, Action: ActionApprove, Mutating: true, Audit: "proposal.approve",
			Summary: "Approve and apply a pending proposal.", Params: []param{pathInt("id", "Proposal id.")},
			Responses: ok(DecisionOutcome{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict}, H: s.hApprove},
		{Method: "POST", Path: "/proposals/{id}/reject", ID: "rejectProposal", Tag: tProp, Action: ActionApprove, Mutating: true, Audit: "proposal.reject",
			Summary: "Reject a pending proposal.", Params: []param{pathInt("id", "Proposal id.")},
			Responses: ok(DecisionOutcome{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict}, H: s.hReject},

		{Method: "GET", Path: "/discrepancies", ID: "listDiscrepancies", Tag: tDisc, Action: ActionRead,
			Summary:   "List discrepancies between declared and observed.",
			Params:    list(qEnum("status", "Filter by status.", discrepancyStatuses...), qStr("kind", "Filter by kind.", false), qUUID("object", "Restrict to one object.")),
			Responses: ok(Page[reconcile.Discrepancy]{}), Errors: []int{http.StatusBadRequest}, H: s.hDiscrepancies},
		{Method: "POST", Path: "/discrepancies/{id}/accept", ID: "acceptDiscrepancy", Tag: tDisc, Action: ActionApprove, Mutating: true, Audit: "discrepancy.accept",
			Summary: "Accept an open discrepancy (the difference is expected).",
			Params:  []param{pathInt("id", "Discrepancy id.")}, Body: AcceptInput{}, BodyOptional: true,
			Responses: ok(AcceptOutcome{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hAccept},

		{Method: "GET", Path: "/saved-queries", ID: "listSavedQueries", Tag: tSaved, Action: ActionRead,
			Summary: "List saved queries.", Params: list(),
			Responses: ok(Page[search.Saved]{}), Errors: []int{http.StatusBadRequest}, H: s.hQueries},
		{Method: "POST", Path: "/saved-queries", ID: "createSavedQuery", Tag: tSaved, Action: ActionPropose, Mutating: true, Audit: "query.create",
			Summary: "Save a query. The owner is the caller.", Body: SavedQueryInput{},
			Responses: []response{{Status: http.StatusCreated, Desc: "Created.", Body: search.Saved{}}},
			Errors:    []int{http.StatusBadRequest, http.StatusConflict}, H: s.hQueryCreate},
		{Method: "GET", Path: "/saved-queries/{name}", ID: "getSavedQuery", Tag: tSaved, Action: ActionRead,
			Summary: "One saved query.", Params: []param{{Name: "name", In: "path", Desc: "Query name.", Required: true, Schema: map[string]any{"type": "string"}}},
			Responses: ok(search.Saved{}), Errors: []int{http.StatusNotFound}, H: s.hQuery},
		{Method: "DELETE", Path: "/saved-queries/{name}", ID: "deleteSavedQuery", Tag: tSaved, Action: ActionPropose, Mutating: true, Audit: "query.delete",
			Summary:   "Delete a saved query (its owner, or an administrator).",
			Params:    []param{{Name: "name", In: "path", Desc: "Query name.", Required: true, Schema: map[string]any{"type": "string"}}},
			Responses: []response{{Status: http.StatusNoContent, Desc: "Deleted."}}, Errors: []int{http.StatusNotFound}, H: s.hQueryDelete},
	}
}
