// Package sdk is the public contract between a collector and the trica server.
//
// A collector never writes to the inventory. It reports what it saw as a Batch
// of observations; the server's reconciliation engine decides what to create,
// update, propose or flag. This keeps one place in the codebase that knows the
// identity, priority and absence rules, whatever the number of collectors.
//
// A minimal collector is a type implementing Collector:
//
//	type uptime struct{ host string }
//
//	func (u uptime) Kind() string { return "example" }
//
//	func (u uptime) Collect(ctx context.Context, emit sdk.Emitter) error {
//		b := sdk.NewBatch("example:"+u.host, sdk.ScopeFull)
//		dev := b.Object("host", "device").
//			Identify("name", u.host).
//			Set("role", "server")
//		_ = dev
//		return emit.Emit(ctx, b)
//	}
package sdk

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Confidence says how a value came to be known. It is stored with every value
// and every relation, and it is what the UI and the MCP tools show next to them.
type Confidence string

const (
	// Observed: read directly on the equipment or system.
	Observed Confidence = "observed"
	// Deduced: inferred from other observations (a cable from two LLDP views).
	Deduced Confidence = "deduced"
	// Declared: written by a human or a declaration file.
	Declared Confidence = "declared"
)

// Valid reports whether c is one of the three known levels.
func (c Confidence) Valid() bool {
	return c == Observed || c == Deduced || c == Declared
}

// Scope tells the server how much of the world a batch claims to describe.
type Scope string

const (
	// ScopeFull: the batch is the complete view of its source. Any claim the
	// source made before and does not repeat here is withdrawn.
	ScopeFull Scope = "full"
	// ScopePartial: the batch only adds or refreshes claims (events, deltas).
	// Nothing is withdrawn.
	ScopePartial Scope = "partial"
)

// Batch is one report from one source.
type Batch struct {
	// Source identifies who is speaking, e.g. "arista:sw-a1" or
	// "agent:cont-svr1". Claims are owned per source: two sources never
	// overwrite each other, the priority rules choose between them.
	Source string `json:"source"`
	// Scope: see ScopeFull / ScopePartial.
	Scope Scope `json:"scope"`
	// ObservedAt is when the source looked. It becomes first/last seen.
	ObservedAt time.Time `json:"observed_at"`
	// Confidence is the default for every object and relation of the batch.
	Confidence Confidence  `json:"confidence"`
	Objects    []*Object   `json:"objects"`
	Relations  []*Relation `json:"relations"`
	// Unrelations withdraw this source's claims on relations; used by
	// partial-scope batches (a full-scope batch withdraws by omission).
	Unrelations []*Relation `json:"unrelations,omitempty"`
	// Health is the collector's own report on this run; it feeds the
	// collection health board.
	Health *Health `json:"health,omitempty"`

	refs map[string]*Object
}

// Object is one observed thing.
type Object struct {
	// Ref is local to the batch; relations point to objects through it.
	Ref  string `json:"ref"`
	Type string `json:"type"`
	// Identity holds the identifiers the server uses to recognise the object
	// across renames and readdressing: serial, mac, uuid, name, ip, ...
	// Which keys are strong and which are weak is defined by the schema.
	Identity map[string]string `json:"identity"`
	// Parent, when set, scopes weak identifiers: an interface named
	// "Ethernet1" is only unique inside its device.
	Parent     string         `json:"parent,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
	// Confidence overrides the batch default when set.
	Confidence Confidence `json:"confidence,omitempty"`
	// Withdraw lists attributes on which this source withdraws its claims.
	Withdraw []string `json:"withdraw,omitempty"`
}

// IDKey is the identity key that designates an existing object by its trica
// id. The UI and the API use it to edit an object they already show.
const IDKey = "id"

// Endpoint designates one end of a relation: either an object of the same
// batch (Ref) or an object the server must find by identity (Type+Identity,
// optionally scoped by a parent found the same way).
type Endpoint struct {
	Ref      string            `json:"ref,omitempty"`
	Type     string            `json:"type,omitempty"`
	Identity map[string]string `json:"identity,omitempty"`
	Parent   *Endpoint         `json:"parent,omitempty"`
}

// Relation is one observed link between two objects.
type Relation struct {
	Type       string         `json:"type"`
	From       Endpoint       `json:"from"`
	To         Endpoint       `json:"to"`
	Attributes map[string]any `json:"attributes,omitempty"`
	Confidence Confidence     `json:"confidence,omitempty"`
}

// Health is a collector's report on one run against one target.
type Health struct {
	Target    string        `json:"target"`
	OK        bool          `json:"ok"`
	Error     string        `json:"error,omitempty"`
	Duration  time.Duration `json:"duration"`
	Items     int           `json:"items"`
	Warnings  []string      `json:"warnings,omitempty"`
	Collector string        `json:"collector"`
	Version   string        `json:"version,omitempty"`
}

// NewBatch starts a batch for source, observed now, with observed confidence.
func NewBatch(source string, scope Scope) *Batch {
	return &Batch{
		Source:     source,
		Scope:      scope,
		ObservedAt: time.Now().UTC(),
		Confidence: Observed,
		refs:       map[string]*Object{},
	}
}

// Object adds (or returns the existing) object with the given local ref.
func (b *Batch) Object(ref, typ string) *Object {
	if b.refs == nil {
		b.reindex()
	}
	if o, ok := b.refs[ref]; ok {
		return o
	}
	o := &Object{Ref: ref, Type: typ, Identity: map[string]string{}, Attributes: map[string]any{}}
	b.Objects = append(b.Objects, o)
	b.refs[ref] = o
	return o
}

// Lookup returns the object with the given local ref, if any.
func (b *Batch) Lookup(ref string) (*Object, bool) {
	if b.refs == nil {
		b.reindex()
	}
	o, ok := b.refs[ref]
	return o, ok
}

func (b *Batch) reindex() {
	b.refs = make(map[string]*Object, len(b.Objects))
	for _, o := range b.Objects {
		b.refs[o.Ref] = o
	}
}

// Relate adds a relation between two local refs.
func (b *Batch) Relate(typ, from, to string) *Relation {
	return b.RelateEndpoints(typ, Endpoint{Ref: from}, Endpoint{Ref: to})
}

// RelateEndpoints adds a relation between arbitrary endpoints.
func (b *Batch) RelateEndpoints(typ string, from, to Endpoint) *Relation {
	r := &Relation{Type: typ, From: from, To: to, Attributes: map[string]any{}}
	b.Relations = append(b.Relations, r)
	return r
}

// Unrelate withdraws this source's claim on a relation.
func (b *Batch) Unrelate(typ string, from, to Endpoint) *Relation {
	r := &Relation{Type: typ, From: from, To: to}
	b.Unrelations = append(b.Unrelations, r)
	return r
}

// Ref designates an object of the same batch.
func Ref(ref string) Endpoint { return Endpoint{Ref: ref} }

// ByID designates an existing object by its trica id.
func ByID(typ, id string) Endpoint {
	return Endpoint{Type: typ, Identity: map[string]string{IDKey: id}}
}

// Unset withdraws this source's claims on keys.
func (o *Object) Unset(keys ...string) *Object {
	o.Withdraw = append(o.Withdraw, keys...)
	return o
}

// Identify sets an identifier. Empty values are ignored, so callers can pass
// optional fields without checking them.
func (o *Object) Identify(key, value string) *Object {
	if value != "" {
		o.Identity[key] = value
	}
	return o
}

// Set sets an attribute. Nil values and empty strings are ignored: absence of
// a reading is not a reading of "nothing".
func (o *Object) Set(key string, value any) *Object {
	switch v := value.(type) {
	case nil:
		return o
	case string:
		if v == "" {
			return o
		}
	}
	o.Attributes[key] = value
	return o
}

// In scopes the object under a parent ref (an interface in its device).
func (o *Object) In(parentRef string) *Object {
	o.Parent = parentRef
	return o
}

// With sets a relation attribute.
func (r *Relation) With(key string, value any) *Relation {
	if value != nil {
		r.Attributes[key] = value
	}
	return r
}

// Validate checks the batch is self-consistent. The server validates again
// against the schema; this catches collector bugs at the source.
func (b *Batch) Validate() error {
	if b.Source == "" {
		return fmt.Errorf("batch: empty source")
	}
	if b.Scope != ScopeFull && b.Scope != ScopePartial {
		return fmt.Errorf("batch %s: invalid scope %q", b.Source, b.Scope)
	}
	if !b.Confidence.Valid() {
		return fmt.Errorf("batch %s: invalid confidence %q", b.Source, b.Confidence)
	}
	b.reindex()
	if len(b.refs) != len(b.Objects) {
		return fmt.Errorf("batch %s: duplicate object refs", b.Source)
	}
	for _, o := range b.Objects {
		if o.Type == "" {
			return fmt.Errorf("batch %s: object %q has no type", b.Source, o.Ref)
		}
		if len(o.Identity) == 0 {
			return fmt.Errorf("batch %s: object %q has no identity", b.Source, o.Ref)
		}
		if o.Parent != "" {
			if _, ok := b.refs[o.Parent]; !ok {
				return fmt.Errorf("batch %s: object %q has unknown parent %q", b.Source, o.Ref, o.Parent)
			}
		}
		if o.Confidence != "" && !o.Confidence.Valid() {
			return fmt.Errorf("batch %s: object %q: invalid confidence %q", b.Source, o.Ref, o.Confidence)
		}
	}
	if err := b.checkParentCycles(); err != nil {
		return err
	}
	for i, r := range append(append([]*Relation(nil), b.Relations...), b.Unrelations...) {
		if r.Type == "" {
			return fmt.Errorf("batch %s: relation %d has no type", b.Source, i)
		}
		for _, e := range []Endpoint{r.From, r.To} {
			if err := b.validEndpoint(e); err != nil {
				return fmt.Errorf("batch %s: relation %d (%s): %w", b.Source, i, r.Type, err)
			}
		}
	}
	return nil
}

func (b *Batch) checkParentCycles() error {
	for _, o := range b.Objects {
		seen := map[string]bool{o.Ref: true}
		for p := o.Parent; p != ""; p = b.refs[p].Parent {
			if seen[p] {
				return fmt.Errorf("batch %s: parent cycle through %q", b.Source, p)
			}
			seen[p] = true
		}
	}
	return nil
}

func (b *Batch) validEndpoint(e Endpoint) error {
	if e.Ref != "" {
		if _, ok := b.refs[e.Ref]; !ok {
			return fmt.Errorf("unknown ref %q", e.Ref)
		}
		return nil
	}
	if e.Type == "" || len(e.Identity) == 0 {
		return fmt.Errorf("endpoint needs a ref or a type and an identity")
	}
	if e.Parent != nil {
		return b.validEndpoint(*e.Parent)
	}
	return nil
}

// SortedKeys returns the keys of m in order; handy for deterministic output.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Emitter receives batches. The server-side implementation reconciles them;
// the remote collector and the agent implementation send them over mTLS and
// spool them on disk while the server is unreachable.
type Emitter interface {
	Emit(ctx context.Context, b *Batch) error
}

// EmitterFunc adapts a function to Emitter.
type EmitterFunc func(ctx context.Context, b *Batch) error

// Emit calls f.
func (f EmitterFunc) Emit(ctx context.Context, b *Batch) error { return f(ctx, b) }

// Collector is what a collector plugin implements.
type Collector interface {
	// Kind is the collector family ("arista", "redfish", ...).
	Kind() string
	// Collect performs one run and emits zero or more batches.
	Collect(ctx context.Context, emit Emitter) error
}
