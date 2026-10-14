package api

import (
	"encoding/json"
	"strings"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The schema has no "secret" kind and no sensitivity flag: credentials are
// references, never values (AGENTS.md). Custom types are user-defined though,
// so the API does not rely on the model alone. An attribute whose NAME says it
// holds a secret is never returned, never searchable, and cannot be written.
// This is a deliberate over-approximation: a false positive hides one
// attribute, a false negative leaks a credential.
var sensitiveMarkers = []string{
	"password", "passwd", "passphrase", "secret", "token", "credential",
	"private_key", "privatekey", "api_key", "apikey", "community", "psk",
}

// Sensitive reports whether an attribute name must never leave the server.
func Sensitive(name string) bool {
	n := strings.ToLower(name)
	for _, m := range sensitiveMarkers {
		if strings.Contains(n, m) {
			return true
		}
	}
	return false
}

// redactMap returns m without its sensitive keys (m itself is not modified).
func redactMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if !Sensitive(k) {
			out[k] = v
		}
	}
	return out
}

// redactValues drops history and provenance rows of sensitive attributes.
func redactValues(in []store.Value) []store.Value {
	out := make([]store.Value, 0, len(in))
	for _, v := range in {
		if !Sensitive(v.Key) {
			out = append(out, v)
		}
	}
	return out
}

func redactClaims(in []store.Claim) []store.Claim {
	out := make([]store.Claim, 0, len(in))
	for _, c := range in {
		if !Sensitive(c.Key) {
			out = append(out, c)
		}
	}
	return out
}

// redactChanges drops attribute changes of sensitive keys and strips the
// sensitive keys of relation attributes (before and after of a relation change
// are attribute maps).
func redactChanges(in []store.Change) []store.Change {
	out := make([]store.Change, 0, len(in))
	for _, c := range in {
		if c.Key != "" && Sensitive(c.Key) {
			continue
		}
		if c.Relation != "" {
			c.Before, c.After = redactAny(c.Before), redactAny(c.After)
		}
		out = append(out, c)
	}
	return out
}

func redactAny(v any) any {
	if m, ok := v.(map[string]any); ok {
		return redactMap(m)
	}
	return v
}

func redactRelations(in []store.Relation) []store.Relation {
	out := make([]store.Relation, len(in))
	for i, r := range in {
		r.Attrs = redactMap(r.Attrs)
		out[i] = r
	}
	return out
}

// redactBatch returns a copy of b without the sensitive attributes. Proposals
// store the batch as submitted; nothing sensitive can get in through the API
// (see sensitiveInBatch), but a proposal can come from the engine's other
// callers.
func redactBatch(b *sdk.Batch) *sdk.Batch {
	if b == nil {
		return nil
	}
	raw, _ := json.Marshal(b)
	c := &sdk.Batch{}
	_ = json.Unmarshal(raw, c)
	for _, o := range c.Objects {
		o.Attributes = redactMap(o.Attributes)
		o.Withdraw = keepNonSensitive(o.Withdraw)
	}
	for _, r := range c.Relations {
		r.Attributes = redactMap(r.Attributes)
	}
	return c
}

func keepNonSensitive(keys []string) []string {
	var out []string
	for _, k := range keys {
		if !Sensitive(k) {
			out = append(out, k)
		}
	}
	return out
}

// sensitiveInBatch names the first sensitive attribute a batch tries to
// write, or "".
func sensitiveInBatch(b *sdk.Batch) string {
	for _, o := range b.Objects {
		for k := range o.Attributes {
			if Sensitive(k) {
				return k
			}
		}
		for k := range o.Identity {
			if Sensitive(k) {
				return k
			}
		}
	}
	for _, r := range b.Relations {
		for k := range r.Attributes {
			if Sensitive(k) {
				return k
			}
		}
	}
	return ""
}

// redactResult drops planned changes on sensitive keys.
func redactResult(res *reconcile.Result) *reconcile.Result {
	if res == nil {
		return nil
	}
	c := *res
	c.Changes = nil
	for _, ch := range res.Changes {
		if ch.Key != "" && Sensitive(ch.Key) {
			continue
		}
		c.Changes = append(c.Changes, ch)
	}
	return &c
}
