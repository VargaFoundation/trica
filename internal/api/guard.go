package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/tql"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Guard gives another front end (the MCP server) the access rules the REST
// handlers apply to one authenticated caller, so that there is a single
// implementation of them: readable types, per-object type and site checks,
// the refusal of sensitive attributes, batch checks and result filtering. A
// Guard is cheap and short-lived: build one per call.
type Guard struct{ c *call }

// Guard returns the access rules of s for principal p. ctx is the context of
// the call being served.
func (s *Server) Guard(ctx context.Context, p Principal) *Guard {
	r := (&http.Request{}).WithContext(ctx)
	return &Guard{c: &call{s: s, r: r, p: p}}
}

// Principal is the caller the guard speaks for.
func (g *Guard) Principal() Principal { return g.c.p }

// Allow is the Authorizer's scoped check for this caller.
func (g *Guard) Allow(action, objType, site string) bool {
	return g.c.s.auth.Allow(g.c.p, action, objType, site)
}

// Query parses q and applies the rules every list and search obeys: sensitive
// attributes are refused in fields, sort keys and has: predicates, typeName
// (optional) narrows to one type, and the query is restricted, in its syntax
// tree, to the types the caller may read.
func (g *Guard) Query(q, sort, typeName string, requireQ bool) (*tql.Query, error) {
	return g.c.searchQuery(q, sort, typeName, requireQ)
}

// Check refuses a query that names a sensitive attribute (in a condition,
// a has: predicate or a sort key). Query does it already: use Check for a
// syntax tree built by the caller.
func (g *Guard) Check(q *tql.Query) error { return checkSensitive(q) }

// Restrict narrows q, in its syntax tree, to typeName when set and to the
// types the caller may read. Query does it already.
func (g *Guard) Restrict(q *tql.Query, typeName string) error { return g.c.restrictTypes(q, typeName) }

// ObjectAt loads an object the caller may read, as it is now or at a date
// (at nil means now); its view carries the id of its site.
func (g *Guard) ObjectAt(id string, at *time.Time) (*ObjectView, error) { return g.c.objectAt(id, at) }

// ReadableTypes lists the types the caller reads in every site, and whether that is all of them.
func (g *Guard) ReadableTypes() (types []string, all bool) { return g.c.readableTypes() }

// LoadObject returns the object after checking that the caller may read it:
// an error answering 404 when it does not exist, 403 when its type and site
// are not covered. The second result is the id of the object's site.
func (g *Guard) LoadObject(id string) (*store.Object, string, error) { return g.c.loadObject(id) }

// VisibleRefs loads the objects with these ids and keeps those the caller may read.
func (g *Guard) VisibleRefs(ids []string) (map[string]ObjectRef, error) { return g.c.visibleRefs(ids) }

// RelationViews names the other end of each relation (sensitive attributes
// stripped) and drops those whose other end the caller may not read.
func (g *Guard) RelationViews(self string, rels []store.Relation) ([]RelationView, error) {
	return g.c.relationViews(self, redactRelations(rels))
}

// FilterChanges strips sensitive attributes and keeps the changes on objects the caller may read.
func (g *Guard) FilterChanges(in []store.Change) ([]store.Change, error) {
	return g.c.filterChanges(redactChanges(in))
}

// CheckBatch applies the rules a batch must meet before it reaches the
// engine: no secret attribute, known types, and the caller holds each action
// on every type the batch touches and on the existing objects it designates.
func (g *Guard) CheckBatch(b *sdk.Batch, actions ...string) error {
	return g.c.checkBatch(b, actions...)
}

// VisibleResult drops from an engine result what the caller may not read.
func (g *Guard) VisibleResult(res *reconcile.Result) *reconcile.Result { return g.c.visibleResult(res) }

// SiteIDs returns the site of each object id (see Server.siteIDs).
func (g *Guard) SiteIDs(ids []string) (map[string]string, error) {
	return g.c.s.siteIDs(g.c.ctx(), ids)
}

// Provenance returns where each attribute of an object comes from, sensitive attributes removed.
// The caller's right to read the object is not checked here: call LoadObject first.
func (g *Guard) Provenance(id string) (*Provenance, error) { return g.c.s.provenance(g.c.ctx(), id) }

// ObjectViewOf is the API's view of an object (sensitive attributes removed).
func ObjectViewOf(o *store.Object) ObjectView { return objectView(o) }

// ErrorInfo classifies an error the way the REST API does: the HTTP status,
// the problem slug and a detail that is safe to show. An error that is not the
// client's fault gets a generic detail (a database error can name tables and
// values); internal reports that so that the caller logs the original.
func ErrorInfo(err error) (status int, slug, detail string, internal bool) {
	he, internal := classify(err)
	return he.status, he.slug, he.detail, internal
}

// Redaction helpers, shared with the REST handlers: an attribute whose name
// says it holds a secret never leaves the server.
var (
	RedactMap       = redactMap
	RedactValues    = redactValues
	RedactClaims    = redactClaims
	RedactChanges   = redactChanges
	RedactRelations = redactRelations
)

// SensitiveInBatch names the first sensitive attribute a batch tries to write, or "".
func SensitiveInBatch(b *sdk.Batch) string { return sensitiveInBatch(b) }

// MaxScan is how many rows a handler reads when the store has no offset of its own.
const MaxScan = maxScan

// PageBounds validates limit and offset as the REST API does (0 limit means the default).
func PageBounds(limit, offset int) (int, int, error) {
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return 0, 0, badRequest("limit must be an integer between 1 and %d", MaxLimit)
	}
	if offset < 0 {
		return 0, 0, badRequest("offset must be a non-negative integer")
	}
	return limit, offset, nil
}

// PageOf slices an already filtered list into a Page.
func PageOf[T any](all []T, limit, offset int, truncated bool) Page[T] {
	return pageOf(all, pageParams{Limit: limit, Offset: offset}, truncated)
}

// Discrepancies lists discrepancies (status, kind and object are optional filters; object is a UUID).
func (g *Guard) Discrepancies(status, kind, object string, limit, offset int) (Page[reconcile.Discrepancy], error) {
	var none Page[reconcile.Discrepancy]
	if status != "" && !contains(discrepancyStatuses, status) {
		return none, badRequest("status must be one of %s", strings.Join(discrepancyStatuses, ", "))
	}
	f := reconcile.DiscrepancyFilter{Status: status, Kind: kind}
	if object != "" {
		var err error
		if f.ObjectID, err = parseUUID(object, "object"); err != nil {
			return none, err
		}
	}
	limit, offset, err := PageBounds(limit, offset)
	if err != nil {
		return none, err
	}
	return g.c.discrepancies(f, pageParams{Limit: limit, Offset: offset})
}

// SchemaDoc returns the registry as the REST API documents it: types
// (optionally one) and relations, sensitive attributes left out.
func (s *Server) SchemaDoc() SchemaDoc { return s.schemaDoc() }

// BadRequest, Forbidden and NotFound build the errors ErrorInfo classifies as
// 400, 403 and 404, for front ends that apply their own validation.
func BadRequest(format string, args ...any) error { return badRequest(format, args...) }

// Forbidden: see BadRequest.
func Forbidden(format string, args ...any) error { return forbidden(format, args...) }

// NotFound: see BadRequest.
func NotFound(format string, args ...any) error { return notFound(format, args...) }
