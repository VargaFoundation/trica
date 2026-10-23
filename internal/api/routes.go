package api

import (
	"net/http"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/topomap"
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
	// BodyBinary lists the media types of a request body that is not JSON (a
	// zip archive); it is read by the handler. Exclusive with Body.
	BodyBinary []string
	// MaxBody bounds the request body; zero means the general limit.
	MaxBody int64
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
		tColl   = "collectors"
		tTheme  = "themes"
	)
	collectorKind := param{Name: "kind", In: "path", Desc: "Kind of the collector module.", Required: true,
		Schema: map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]*$"}}
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

		{Method: "GET", Path: "/collectors", ID: "listCollectors", Tag: tColl, Action: ActionRead,
			Summary:   "The collector modules this binary knows, with their instances counted by status, and the sources no module claims.",
			Responses: ok(CollectorCatalogue{}), H: s.hListCollectors},
		{Method: "GET", Path: "/collectors/{kind}", ID: "getCollector", Tag: tColl, Action: ActionRead,
			Summary:   "One collector module: what it collects, how it is configured, what it was tested with.",
			Params:    []param{collectorKind},
			Responses: ok(CollectorModule{}), Errors: []int{http.StatusNotFound}, H: s.hGetCollector},
		{Method: "GET", Path: "/collectors/{kind}/instances", ID: "listCollectorInstances", Tag: tColl, Action: ActionRead,
			Summary:   "The instances of a module (its targets and the sources reported by its collectors), failing first.",
			Params:    append([]param{collectorKind}, list(qEnum("status", "Filter by status.", instanceStatuses...))...),
			Responses: ok(Page[CollectorInstance]{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hListCollectorInstances},
		{Method: "GET", Path: "/collectors/{kind}/instances/{name}", ID: "getCollectorInstance", Tag: tColl, Action: ActionRead,
			Summary:   "One instance: its configuration (credential references to administrators only) and its last run.",
			Params:    []param{collectorKind, {Name: "name", In: "path", Desc: "Name of the instance (the source without its kind prefix).", Required: true, Schema: map[string]any{"type": "string"}}},
			Responses: ok(CollectorInstance{}), Errors: []int{http.StatusNotFound}, H: s.hGetCollectorInstance},

		{Method: "GET", Path: "/schema", ID: "getSchema", Tag: tSchema, Action: ActionRead,
			Summary:   "The schema registry: object types, attributes, relation types.",
			Responses: ok(SchemaDoc{}), H: s.hSchema},
		{Method: "GET", Path: "/schema/types/{type}", ID: "getSchemaType", Tag: tSchema, Action: ActionRead,
			Summary: "One object type.", Params: []param{{Name: "type", In: "path", Desc: "Object type name.", Required: true, Schema: map[string]any{"type": "string"}}},
			Responses: ok(TypeDoc{}), Errors: []int{http.StatusNotFound}, H: s.hSchemaType},

		{Method: "GET", Path: "/objects", ID: "listObjects", Tag: tInv, Action: ActionRead,
			Summary: "List objects, optionally filtered with a TQL query.",
			Params: list(qStr("q", "TQL query (see the query language reference).", false), typeParam,
				qStr("sort", "Comma-separated fields, a leading - sorts descending.", false),
				qEnum("mounted", "true keeps the objects mounted in a rack now, false those mounted in none (the same as rel.mounted_in:* and -rel.mounted_in:*).", "true", "false")),
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
		{Method: "GET", Path: "/objects/{id}/series", ID: "getObjectSeries", Tag: tInv, Action: ActionRead,
			Summary: "A measurement of the object (power, temperature) over a range, in buckets, from its history.",
			Params: []param{pathUUID("id", "Object id."), qStr("key", "The measurement: an attribute the schema marks as a series.", true),
				qTime("from", "Start of the range (default: 24 hours before to).", false), qTime("to", "End of the range (default: now).", false),
				{Name: "points", In: "query", Desc: "Number of buckets, 1 to 1000 (default 120).", Schema: map[string]any{"type": "integer", "minimum": 1, "maximum": MaxSeriesPoints, "default": DefaultSeriesPoints}}},
			Responses: ok(Series{}), Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}, H: s.hSeries},
		{Method: "GET", Path: "/objects/{id}/series/keys", ID: "getObjectSeriesKeys", Tag: tInv, Action: ActionRead,
			Summary:   "The measurements of the object (the series attributes of its type) that have at least one reading in a range, current or withdrawn: which curves its page draws, in one call (ADR 0045).",
			Params:    []param{pathUUID("id", "Object id."), qTime("from", "Start of the range (default: 24 hours before to).", false), qTime("to", "End of the range (default: now).", false)},
			Responses: ok(SeriesKeys{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hSeriesKeys},
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
		{Method: "GET", Path: "/topology/map", ID: "getTopologyMap", Tag: tInv, Action: ActionRead,
			Summary: "One level of the topology map (ADR 0051): the places of the level (sites; the rooms of a site; the racks of a room; the devices of a rack; or the devices in no site) as nodes, the links between them aggregated per pair and medium as edges (physical cables and adjacencies, tunnels, Internet uplinks), with the live rates when a live source is configured, and portals standing for what lies outside the level. level is global, site, room or rack, or unplaced; id is the site, the room (or a site, for its floor outside rooms) or the rack, and is refused for global and unplaced. Computed from the shared inventory graph, at most 10 seconds behind the last commit; nodes and edges are listed only if the caller may read them (the Internet and the uplinks only to a caller who reads gateways in every site).",
			Params: []param{requiredParam(qEnum("level", "The level to show.", topomap.LevelGlobal, topomap.LevelSite, topomap.LevelRoom, topomap.LevelRack, topomap.LevelUnplaced)),
				qUUID("id", "The place the level is about: a site, a room or a rack. Absent for global and unplaced.")},
			Responses: ok(topomap.TopologyMap{}), Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}, H: s.hTopologyMap},
		{Method: "GET", Path: "/racks/{id}/contents", ID: "getRackContents", Tag: tInv, Action: ActionRead,
			Summary:   "The devices mounted in a rack, with position, face, model, height and depth, and each device's airflow check against the rack's front_aisle (ADR 0049): one call for an elevation. A device is listed only if the caller may read it, its model only if the caller may read the model. Ordered by position, unknown positions last.",
			Params:    list(pathUUID("id", "Rack id.")),
			Responses: ok(Page[RackItem]{}), Errors: []int{http.StatusBadRequest, http.StatusNotFound}, H: s.hRackContents},
		{Method: "GET", Path: "/racks/{id}/removed", ID: "getRackRemoved", Tag: tInv, Action: ActionRead,
			Summary:   "The devices taken out of a rack and not mounted anywhere since (ADR 0053): a device is listed when its most recent placement, in any rack, ended in this one and it has no current placement. Each item is what the elevation knew of the device (position, face, model, height) plus when and by which source's placement it ended, and the device's status. Most recently removed first, 20 by default. A device is listed only if the caller may read it. A device moved to another rack, or put back, is not listed.",
			Params:    list(pathUUID("id", "Rack id."), qTime("since", "Only what was taken out at or after this time.", false)),
			Responses: ok(Page[RemovedItem]{}), Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}, H: s.hRackRemoved},
		{Method: "GET", Path: "/objects/{id}/placement-history", ID: "getPlacementHistory", Tag: tInv, Action: ActionRead,
			Summary:   "Where a device was mounted (ADR 0053): the current placement first, then the closed ones, the most recently closed first, 20 by default. Each has the rack, position and face (as they were when it ended), when it began and ended, and the source whose placement it was. A rack the caller may not read is left out. Not for objects other than devices (404).",
			Params:    list(pathUUID("id", "Device id.")),
			Responses: ok(Page[PlacementRecord]{}), Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}, H: s.hPlacementHistory},
		{Method: "GET", Path: "/objects/{id}/faces", ID: "getObjectFaces", Tag: tInv, Action: ActionRead,
			Summary:   "The faces of a device's model with its parts in place: the images of the faces, the spots the device-type library knows on them (bays, power supplies, fans, ports, slots), and the device's parts the caller may read, each with the spot it goes in or none when its name does not say. Parts are listed only for the kinds the faces have spots for; absent parts are left out. Empty when the device has no model with images.",
			Params:    list(pathUUID("id", "Object id.")),
			Responses: ok(DeviceFaces{}), Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}, H: s.hObjectFaces},
		{Method: "GET", Path: "/objects/{id}/airflow", ID: "getObjectAirflow", Tag: tInv, Action: ActionRead,
			Summary:   "The airflow check of a device (ADR 0049): the rack it is mounted in and the face its front is on, which aisle the rack's front faces (front_aisle), what its model, the device itself (airflow, configured or observed) and its fans and power supplies say about its airflow, and the result: ok (it takes its air in from the cold aisle), mismatch (from the hot aisle: hot air recirculated, the reason says how) or unknown (the reason says which fact is missing). Warnings say when the device or its parts contradict its model (the wrong variant, a fan tray or a power supply turned the other way). The rack contents carry the same check per device.",
			Params:    []param{pathUUID("id", "Device id.")},
			Responses: ok(DeviceAirflow{}), Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}, H: s.hObjectAirflow},
		{Method: "GET", Path: "/objects/{id}/device-type-candidates", ID: "getDeviceTypeCandidates", Tag: tInv, Action: ActionRead,
			Summary:   "How sure the inventory is of a device's model, and the models it may be (ADR 0048). certain and reason say what decided the model (declared, sku, model, mounting, part_number) or why it is not certain (base: the name matched a model with chassis variants; airflow; ambiguous: several models fit and none matched; none). candidates are the models of the family the library finds by name and variant_of, with their height and the API paths of their face images. A person chooses one by sending a partial batch that relates the device to the model (instance_of, POST /batches): a declaration wins over the library's match by name and survives later collections; a part number that names another chassis is reported as sku_conflict, not applied. can_choose says whether the caller may send that batch.",
			Params:    []param{pathUUID("id", "Device id.")},
			Responses: ok(DeviceTypeChoice{}), Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}, H: s.hDeviceTypeCandidates},
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

		{Method: "GET", Path: "/ui/themes", ID: "getThemes", Tag: tTheme, Action: ActionAdmin,
			Summary:   "The theme in force, the deployment default, the built-in themes and the stored archives.",
			Responses: ok(ThemeState{}), Errors: []int{http.StatusNotFound}, H: s.hThemes},
		{Method: "POST", Path: "/ui/themes", ID: "uploadTheme", Tag: tTheme, Action: ActionAdmin, Mutating: true, Audit: "theme.upload",
			Summary:    "Upload a theme archive (the zip itself as the body, 2 MiB at most). It is validated and kept, not selected.",
			BodyBinary: []string{"application/zip"}, MaxBody: MaxThemeBody,
			Responses: []response{{Status: http.StatusCreated, Desc: "Kept.", Body: StoredTheme{}},
				{Status: http.StatusOK, Desc: "Already kept (same bytes).", Body: StoredTheme{}}},
			Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity}, H: s.hThemeUpload},
		{Method: "PUT", Path: "/ui/themes/active", ID: "activateTheme", Tag: tTheme, Action: ActionAdmin, Mutating: true, Audit: "theme.activate",
			Summary: "Select the theme for every replica: a built-in one or a stored archive. Refused when the configuration locks the theme.",
			Body:    ThemeSelection{}, Responses: ok(ThemeState{}),
			Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict}, H: s.hThemeActivate},
		{Method: "DELETE", Path: "/ui/themes/active", ID: "resetTheme", Tag: tTheme, Action: ActionAdmin, Mutating: true, Audit: "theme.reset",
			Summary:   "Drop the selection: the deployment default is back. Refused when the configuration locks the theme.",
			Responses: []response{{Status: http.StatusNoContent, Desc: "Reset."}}, Errors: []int{http.StatusNotFound, http.StatusConflict}, H: s.hThemeReset},
		{Method: "DELETE", Path: "/ui/themes/{sha256}", ID: "deleteTheme", Tag: tTheme, Action: ActionAdmin, Mutating: true, Audit: "theme.delete",
			Summary: "Delete a stored archive. The theme in force is refused.",
			Params: []param{{Name: "sha256", In: "path", Desc: "Identity of the archive.", Required: true,
				Schema: map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}}},
			Responses: []response{{Status: http.StatusNoContent, Desc: "Deleted."}}, Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict}, H: s.hThemeDelete},
	}
}

// requiredParam marks a parameter required.
func requiredParam(p param) param { p.Required = true; return p }
