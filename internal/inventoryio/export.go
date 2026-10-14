package inventoryio

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// sensitiveName matches attribute names that hold secrets. schema.Sensitive is
// the rule every reader shares; this regexp is a second line of defence for an
// export, which leaves the cluster, and also catches pre-shared key spellings.
var sensitiveName = regexp.MustCompile(`(?i)(password|passwd|passphrase|secret|token|community|api_?key|private_?key|credential|psk|pre_?shared)`)

func isSensitive(attr string) bool {
	return schema.Sensitive(attr) || sensitiveName.MatchString(attr)
}

// ExportOptions selects what to export.
type ExportOptions struct {
	// At exports the state at that date; nil means now.
	At *time.Time
	// History adds every past value of every attribute and every closed relation.
	History bool
	// NoProvenance leaves out the per-attribute source and confidence.
	NoProvenance bool
}

// Export reads the inventory into a Document. It never exports a sensitive
// attribute, in the current state or in the history, and says how many it left out.
func Export(ctx context.Context, s *store.Store, reg *schema.Registry, opt ExportOptions) (*Document, error) {
	objs, err := s.AllObjects(ctx)
	if err != nil {
		return nil, err
	}
	types := make(map[string]string, len(objs))
	for _, o := range objs {
		types[o.ID] = o.Type
	}
	vals, err := s.ValuesAt(ctx, opt.At, false)
	if err != nil {
		return nil, err
	}
	byObj := map[string][]store.ObjectValue{}
	for _, v := range vals {
		byObj[v.ObjectID] = append(byObj[v.ObjectID], v)
	}
	doc := &Document{Format: FormatName, Version: Version, At: opt.At, Omitted: &OmitReport{}}
	now := time.Now().UTC().Truncate(time.Second)
	doc.ExportedAt = &now

	included := map[string]*Object{}
	for _, id := range sdk.SortedKeys(byObj) {
		typ, ok := types[id]
		if !ok {
			continue
		}
		o := &Object{Ref: id, Type: typ, Identity: map[string]string{}, Attributes: map[string]any{}}
		exists := false
		t := reg.Types[typ]
		if t == nil {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("object %s: type %q is not in the registry; no identity exported", id, typ))
		}
		for _, v := range byObj[id] {
			switch v.Key {
			case "_status":
				exists = true
				o.Status, _ = v.Value.Value.(string)
			case "_parent":
				o.Parent, _ = v.Value.Value.(string)
			default:
				if strings.HasPrefix(v.Key, "_") {
					continue
				}
				if isSensitive(v.Key) {
					doc.Omitted.SensitiveAttributes++
					continue
				}
				o.Attributes[v.Key] = v.Value.Value
				if !opt.NoProvenance {
					if o.Provenance == nil {
						o.Provenance = map[string]Provenance{}
					}
					o.Provenance[v.Key] = Provenance{Source: v.Source, Confidence: v.Confidence, Since: v.ValidFrom.UTC()}
				}
				if t != nil {
					if a := t.Attributes[v.Key]; a != nil && a.Identity != "" {
						o.Identity[v.Key] = fmt.Sprint(v.Value.Value)
					}
				}
			}
		}
		if !exists {
			continue // did not exist at that date
		}
		included[id] = o
	}
	for _, o := range included {
		if o.Parent != "" && included[o.Parent] == nil {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("object %s: parent %s is not part of the export; parent dropped", o.Ref, o.Parent))
			o.Parent = ""
		}
		doc.Objects = append(doc.Objects, *o)
	}
	if opt.History {
		if err := addHistory(ctx, s, doc, included, opt); err != nil {
			return nil, err
		}
	}
	sort.Slice(doc.Objects, func(i, j int) bool {
		a, b := doc.Objects[i], doc.Objects[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if ka, kb := identityKey(a.Identity), identityKey(b.Identity); ka != kb {
			return ka < kb
		}
		return a.Ref < b.Ref
	})

	rels, err := s.RelationsAt(ctx, opt.At, false)
	if err != nil {
		return nil, err
	}
	state := map[int64]bool{}
	for _, r := range rels {
		state[r.ID] = true
		doc.addRelation(r, included, false)
	}
	if opt.History {
		all, err := s.RelationsAt(ctx, nil, true)
		if err != nil {
			return nil, err
		}
		for _, r := range all {
			if state[r.ID] || (opt.At != nil && r.ValidFrom.After(*opt.At)) {
				continue
			}
			doc.addRelation(r, included, true)
		}
	}
	sort.SliceStable(doc.Relations, func(i, j int) bool {
		a, b := doc.Relations[i], doc.Relations[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})
	return doc, nil
}

func identityKey(id map[string]string) string {
	var b strings.Builder
	for _, k := range sdk.SortedKeys(id) {
		b.WriteString(k + "=" + id[k] + ";")
	}
	return b.String()
}

func (d *Document) addRelation(r store.Relation, included map[string]*Object, past bool) {
	if included[r.FromID] == nil || included[r.ToID] == nil {
		return
	}
	since := r.ValidFrom.UTC()
	out := Relation{Type: r.Type, From: r.FromID, To: r.ToID, Source: r.Source, Confidence: r.Confidence, Since: &since}
	if len(r.Attrs) > 0 {
		out.Attributes = map[string]any{}
		for k, v := range r.Attrs {
			if isSensitive(k) {
				d.Omitted.SensitiveAttributes++
				continue
			}
			out.Attributes[k] = v
		}
	}
	if past && r.ValidTo != nil {
		u := r.ValidTo.UTC()
		out.Until = &u
	}
	d.Relations = append(d.Relations, out)
}

func addHistory(ctx context.Context, s *store.Store, doc *Document, included map[string]*Object, opt ExportOptions) error {
	all, err := s.ValuesAt(ctx, nil, true)
	if err != nil {
		return err
	}
	hist := map[string][]store.ObjectValue{}
	for _, v := range all {
		if strings.HasPrefix(v.Key, "_") || included[v.ObjectID] == nil {
			continue
		}
		if isSensitive(v.Key) {
			continue // already counted for the state; history is never exported
		}
		if opt.At != nil && v.ValidFrom.After(*opt.At) {
			continue
		}
		hist[v.ObjectID] = append(hist[v.ObjectID], v)
	}
	for i := range doc.Objects {
		for _, v := range hist[doc.Objects[i].Ref] {
			c := ValueChange{Key: v.Key, Value: v.Value.Value, Source: v.Source, Confidence: v.Confidence, From: v.ValidFrom.UTC()}
			if v.ValidTo != nil {
				u := v.ValidTo.UTC()
				c.Until = &u
			}
			doc.Objects[i].History = append(doc.Objects[i].History, c)
		}
	}
	return nil
}
