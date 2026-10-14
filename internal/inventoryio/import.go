package inventoryio

import (
	"context"
	"fmt"
	"regexp"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// ImportOptions configures an import.
type ImportOptions struct {
	// Name completes the source: the batch source is "import:<Name>".
	Name string
	// Scope is what the file claims: sdk.ScopePartial (default) only adds and
	// refreshes; sdk.ScopeFull also withdraws what an earlier import of the
	// same name claimed and this one does not repeat.
	Scope sdk.Scope
	// Author and Reason go in the changeset.
	Author, Reason string
	// DryRun previews the changes without committing.
	DryRun bool
}

// ToBatch turns a document into the batch import submits. It is pure: nothing
// is written, and every defect of the file is reported here, before the engine
// sees anything.
func ToBatch(doc *Document, reg *schema.Registry, opt ImportOptions) (*sdk.Batch, error) {
	if !nameRe.MatchString(opt.Name) {
		return nil, fmt.Errorf("import name %q: use lowercase letters, digits, '.', '_' or '-'", opt.Name)
	}
	scope := opt.Scope
	if scope == "" {
		scope = sdk.ScopePartial
	}
	b := sdk.NewBatch("import:"+opt.Name, scope)
	// A file is written by a human or exported from another inventory: its
	// values are declarations, the lowest rung of the priority order.
	b.Confidence = sdk.Declared

	refs := map[string]bool{}
	for i, o := range doc.Objects {
		if o.Ref == "" {
			return nil, fmt.Errorf("object %d: empty ref", i)
		}
		if refs[o.Ref] {
			return nil, fmt.Errorf("object %q: duplicate ref", o.Ref)
		}
		refs[o.Ref] = true
		t, err := reg.Type(o.Type)
		if err != nil {
			return nil, fmt.Errorf("object %q: %w", o.Ref, err)
		}
		if len(o.Identity) == 0 {
			return nil, fmt.Errorf("object %q (%s): no identity; an object needs at least one identifying attribute", o.Ref, o.Type)
		}
		for k := range o.Identity {
			if a := t.Attributes[k]; a == nil || a.Identity == "" {
				return nil, fmt.Errorf("object %q (%s): %q is not an identifying attribute of the type", o.Ref, o.Type, k)
			}
		}
		for k := range o.Attributes {
			if isSensitive(k) {
				return nil, fmt.Errorf("object %q: attribute %q looks like a secret; secrets are never stored in the inventory", o.Ref, k)
			}
		}
	}
	for _, o := range doc.Objects {
		obj := b.Object(o.Ref, o.Type)
		for k, v := range o.Identity {
			obj.Identify(k, v)
		}
		for k, v := range o.Attributes {
			obj.Set(k, v)
		}
		if o.Parent != "" {
			if !refs[o.Parent] {
				return nil, fmt.Errorf("object %q: unknown parent %q", o.Ref, o.Parent)
			}
			obj.In(o.Parent)
		}
	}
	for i, r := range doc.Relations {
		if r.Until != nil {
			continue // a closed relation is history; import never replays the past
		}
		if _, err := reg.Relation(r.Type); err != nil {
			return nil, fmt.Errorf("relation %d: %w", i, err)
		}
		if !refs[r.From] || !refs[r.To] {
			return nil, fmt.Errorf("relation %d (%s): unknown endpoint %q or %q", i, r.Type, r.From, r.To)
		}
		rel := b.Relate(r.Type, r.From, r.To)
		for k, v := range r.Attributes {
			if isSensitive(k) {
				return nil, fmt.Errorf("relation %d: attribute %q looks like a secret", i, k)
			}
			rel.With(k, v)
		}
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return b, nil
}

// Import submits the document to the engine. It is the only way in: this
// package holds no write path to the inventory tables.
func Import(ctx context.Context, e *reconcile.Engine, doc *Document, opt ImportOptions) (*reconcile.Result, error) {
	b, err := ToBatch(doc, e.Schema(), opt)
	if err != nil {
		return nil, err
	}
	if opt.DryRun {
		return e.Preview(ctx, b)
	}
	return e.Submit(ctx, b, opt.Author, opt.Reason)
}
