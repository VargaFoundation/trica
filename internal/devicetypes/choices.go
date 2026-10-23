package devicetypes

import (
	"sort"
	"strings"
)

// ByDeclared: the model was not matched but declared by another source (a
// person choosing the chassis, a declaration file); the library steps aside
// (ADR 0048).
const ByDeclared = "declared"

// Why a match is not certain (Choice.Reason when Certain is false).
const (
	// UncertainBase: the name matched a model that has chassis variants
	// (Dell "PowerEdge R330" and its 8SFF, 4LFF hot-plug and cabled
	// chassis); which one the device is, nothing it reports says.
	UncertainBase = "base"
	// UncertainAirflow: the name matched an airflow family's base model;
	// the airflow is unknown.
	UncertainAirflow = "airflow"
	// UncertainAmbiguous: nothing matched, but models fit the name (HPE
	// "ProLiant DL180 Gen9" has three chassis and no generic model).
	UncertainAmbiguous = "ambiguous"
	// UncertainNone: nothing matched and nothing fits.
	UncertainNone = "none"
)

// How a candidate was found.
const (
	// CandidateName: its name starts with the device's model name, word for word.
	CandidateName = "name"
	// CandidateVariantOf: its file says it is a variant of a model of the family
	// (variant_of), or the family's models are variants of it.
	CandidateVariantOf = "variant_of"
)

// MaxCandidates bounds a family: a name that fits more models than this
// (a bare "PowerEdge") says too little to choose from.
const MaxCandidates = 12

// Candidate is one model a device may be.
type Candidate struct {
	Model *Model
	How   string
}

// Choice is what the library can say about the model of a device: the model
// it matches, whether that match is certain, and the models the device may
// be when it is not.
type Choice struct {
	// Match is the library's match (Find), or nil.
	Match *Match
	// Certain: the match names the exact chassis. A part number listed by one
	// model is certain; a name, a mounting suffix or an upstream part number
	// is certain unless another model's variant_of names it (ADR 0056).
	Certain bool
	// Reason is the match kind (sku, model, mounting, part_number) when
	// Certain, else one of the Uncertain* values.
	Reason string
	// Candidates are the models of the family, sorted by slug, the match
	// included when it belongs to it. Empty when nothing fits.
	Candidates []Candidate
}

// words splits a model name into lowercase alphanumeric words and drops a
// leading vendor name ("Dell PowerEdge R330" is "poweredge r330").
func words(vendor, name string) []string {
	ws := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	vs := map[string]bool{norm(vendor): true, vendorKey(vendor): true}
	for k := len(ws) - 1; k >= 1; k-- {
		if vs[strings.Join(ws[:k], "")] && len(ws) > k {
			return ws[k:]
		}
	}
	return ws
}

func hasWordPrefix(ws, prefix []string) bool {
	if len(prefix) == 0 || len(ws) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if ws[i] != p {
			return false
		}
	}
	return true
}

// Choices says what the library knows of the model of a device: Find's
// match, whether it is certain, and the candidate models (ADR 0048).
//
// The family of a device is the models of its vendor whose name starts, word
// for word, with the name the device reports ("PowerEdge R330 8 x 2.5in
// hot-plug" for "PowerEdge R330"; "DCS-7050SX-64-F" for "DCS-7050SX-64"),
// the match itself, and the models a variant_of links to any of them; when
// variant_of curates the family, the models found by name alone that it does
// not link are left out. A family larger than MaxCandidates is no family: the
// name is too vague.
func (ix *Index) Choices(vendor, model, partNumber string) Choice {
	ch := Choice{Match: ix.Find(vendor, model, partNumber)}
	ch.Candidates = ix.family(vendor, model, ch.Match)
	switch {
	case ch.Match == nil && len(ch.Candidates) > 0:
		ch.Reason = UncertainAmbiguous
	case ch.Match == nil:
		ch.Reason = UncertainNone
	case ch.Match.How == BySKU:
		ch.Certain, ch.Reason = true, BySKU
	case ch.Match.How == ByVariant:
		ch.Reason = UncertainAirflow
	case ch.Match.Model.VariantOf != "":
		// The name is a variant's own: its file says it is one chassis.
		ch.Certain, ch.Reason = true, ch.Match.How
	default:
		ch.Certain, ch.Reason = true, ch.Match.How
		if ix.hasVariants(ch.Match.Model.Slug) {
			ch.Certain, ch.Reason = false, UncertainBase
		}
	}
	return ch
}

// hasVariants reports whether some model names this one in its variant_of:
// only then is a name match not enough to tell the chassis (ADR 0056). A
// model whose name merely starts with another's ("U7 Pro Max" and "U7 Pro")
// is another product, not a variant.
func (ix *Index) hasVariants(slug string) bool { return ix.baseOf[slug] }

func (ix *Index) family(vendor, model string, match *Match) []Candidate {
	vk := vendorKey(vendor)
	if vk == "" {
		return nil
	}
	// The device's own name, even when a SKU named the chassis: its siblings
	// are what a person may still change it to.
	dw := words(vendor, model)
	all := ix.Models()
	found := map[string]string{} // slug -> how
	for _, m := range all {
		if vendorKey(m.Vendor) == vk && hasWordPrefix(words(m.Vendor, m.Model), dw) {
			found[m.Slug] = CandidateName
		}
	}
	if match != nil {
		if _, ok := found[match.Model.Slug]; !ok {
			found[match.Model.Slug] = CandidateName
		}
	}
	// variant_of, both ways: the base of a member, and the members of a base.
	bases := map[string]bool{}
	for _, m := range all {
		if _, in := found[m.Slug]; in {
			bases[m.Slug] = true
			if m.VariantOf != "" {
				bases[m.VariantOf] = true
			}
		}
	}
	for _, m := range all {
		if vendorKey(m.Vendor) != vk {
			continue
		}
		if _, in := found[m.Slug]; in {
			continue
		}
		if bases[m.Slug] || (m.VariantOf != "" && bases[m.VariantOf]) {
			found[m.Slug] = CandidateVariantOf
		}
	}
	// A family some file curates with variant_of is that family: a model
	// found by its name only, which no member names and which names none, is
	// another product that shares the words ("ProLiant DL380 Gen10 Plus" for
	// "ProLiant DL380 Gen10").
	targets := map[string]bool{}
	for slug := range found {
		if m := ix.BySlug(slug); m != nil && m.VariantOf != "" {
			targets[m.VariantOf] = true
		}
	}
	if len(targets) > 0 {
		for slug, how := range found {
			m := ix.BySlug(slug)
			if how == CandidateName && m != nil && m.VariantOf == "" && !targets[slug] && (match == nil || match.Model.Slug != slug) {
				delete(found, slug)
			}
		}
	}
	if len(found) > MaxCandidates {
		return nil
	}
	out := make([]Candidate, 0, len(found))
	for slug, how := range found {
		m := ix.BySlug(slug)
		if m == nil {
			continue
		}
		out = append(out, Candidate{Model: m, How: how})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model.Slug < out[j].Model.Slug })
	return out
}
