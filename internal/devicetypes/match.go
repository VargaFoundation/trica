package devicetypes

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Match kinds, from the most to the least precise.
const (
	// BySKU: the device's part number is in a model's part_numbers, the
	// list of the SKUs of one variant of a chassis (8SFF, 24SFF...).
	BySKU        = "sku"
	ByModel      = "model"
	ByPartNumber = "part_number"
	ByVariant    = "variant"
	// ByMounting: the device reports the model without the rack-mount suffix
	// its vendor appends to the product code (MikroTik "...+RM").
	ByMounting = "mounting"
)

// Match is the model chosen for a device and why.
type Match struct {
	Model *Model
	How   string
}

// vendorAliases maps normalised vendor spellings to one key. Everything else
// maps to its own normalised spelling.
var vendorAliases = map[string]string{
	"aristanetworks": "arista",
	"hp":             "hpe", "hpe": "hpe", "hewlettpackard": "hpe", "hewlettpackardenterprise": "hpe",
	"dell": "dell", "dellinc": "dell", "dellemc": "dell",
	"routerboard": "mikrotik", "mikrotik": "mikrotik", "mikrotikls": "mikrotik",
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func norm(s string) string { return nonAlnum.ReplaceAllString(strings.ToLower(s), "") }

func vendorKey(v string) string {
	k := norm(v)
	if a, ok := vendorAliases[k]; ok {
		return a
	}
	return k
}

// modelKey normalises a model name as written by a device. A leading vendor
// name ("Dell PowerEdge R640") is dropped, since devices often repeat it.
func modelKey(vendor, model string) string {
	k := norm(model)
	for _, v := range []string{norm(vendor), vendorKey(vendor)} {
		if v != "" && strings.HasPrefix(k, v) && len(k) > len(v) {
			return k[len(v):]
		}
	}
	return k
}

// mountingSuffix is, per vendor, the normalised suffix of the product code
// of a rack-mount edition that the device itself does not report: RouterOS
// names its board "CRS354-48G-4S+2Q+" when the product is "...+2Q+RM".
var mountingSuffix = map[string]string{"mikrotik": "rm"}

// variantSuffix is the airflow suffix some vendors append to a model name
// (Arista DCS-7050SX3-48YC8-F front-to-rear, -R rear-to-front).
var variantSuffix = regexp.MustCompile(`(?i)-[FR]$`)

// Index finds the model of a device.
type Index struct {
	byKey map[string][]*Model // vendorKey + "|" + key
	// bySKU indexes the part_numbers lists, apart from byKey: a variant
	// SKU must not make a name or the upstream part_number ambiguous.
	bySKU map[string][]*Model
	// variants are the airflow-less base models synthesised from -F / -R
	// families whose members agree on everything but the airflow.
	variants map[string]*Model
	// bySlug holds every model and synthesised base model by slug.
	bySlug map[string]*Model
}

func key(vendor, k string) string { return vendorKey(vendor) + "|" + k }

// NewIndex indexes models by vendor and by model name, part number and slug.
func NewIndex(models []*Model) *Index {
	ix := &Index{byKey: map[string][]*Model{}, bySKU: map[string][]*Model{}, variants: map[string]*Model{}, bySlug: map[string]*Model{}}
	addTo := func(idx map[string][]*Model, k string, m *Model) {
		for _, e := range idx[k] {
			if e == m {
				return
			}
		}
		idx[k] = append(idx[k], m)
	}
	add := func(k string, m *Model) { addTo(ix.byKey, k, m) }
	families := map[string][]*Model{}
	for _, m := range models {
		ix.bySlug[m.Slug] = m
		add(key(m.Vendor, modelKey(m.Vendor, m.Model)), m)
		add(key(m.Vendor, norm(m.Slug)), m)
		add(key(m.Vendor, modelKey(m.Vendor, m.Slug)), m)
		if m.PartNumber != "" {
			add(key(m.Vendor, norm(m.PartNumber)), m)
		}
		for _, pn := range m.PartNumbers {
			addTo(ix.bySKU, key(m.Vendor, norm(pn)), m)
		}
		if variantSuffix.MatchString(m.Model) {
			base := key(m.Vendor, modelKey(m.Vendor, variantSuffix.ReplaceAllString(m.Model, "")))
			families[base] = append(families[base], m)
		}
	}
	for base, fam := range families {
		sort.Slice(fam, func(i, j int) bool { return fam[i].Slug < fam[j].Slug })
		if _, exact := ix.byKey[base]; exact || !agree(fam) {
			continue
		}
		first := fam[0]
		v := *first
		v.Model = variantSuffix.ReplaceAllString(first.Model, "")
		v.Slug = variantSuffix.ReplaceAllString(first.Slug, "")
		v.PartNumber, v.PartNumbers, v.Airflow = "", nil, ""
		// The base model shows the images of its first variant: the faces
		// of an airflow family look the same, only the handles differ.
		ix.variants[base] = &v
		if _, taken := ix.bySlug[v.Slug]; !taken {
			ix.bySlug[v.Slug] = &v
		}
	}
	return ix
}

// BySlug returns the model with this slug, a synthesised base model
// included, or nil.
func (ix *Index) BySlug(slug string) *Model { return ix.bySlug[slug] }

// agree reports whether the models of a family differ only by airflow: same
// rack height, depth, weight and ports.
func agree(fam []*Model) bool {
	for _, m := range fam[1:] {
		f := fam[0]
		if m.UHeight != f.UHeight || m.FullDepth != f.FullDepth || m.WeightKg != f.WeightKg || !reflect.DeepEqual(m.Ports, f.Ports) {
			return false
		}
	}
	return true
}

// Find returns the model of a device from its vendor, model and part number,
// or nil. A part number listed in one model's part_numbers wins: it names
// the exact variant of the chassis, which the model name does not (HPE
// reports "ProLiant DL380 Gen9" for the 8SFF and the 24SFF alike). Then the
// name, then the upstream part_number, then the airflow family. A name or
// SKU that fits several different models is ambiguous and matches none: a
// wrong U height or picture is worse than a missing one.
func (ix *Index) Find(vendor, model, partNumber string) *Match {
	// No vendor, no match: keys are scoped by vendor and no model has none.
	if partNumber != "" {
		if m := unique(ix.bySKU[key(vendor, norm(partNumber))]); m != nil {
			return &Match{m, BySKU}
		}
	}
	if model != "" {
		if m := unique(ix.byKey[key(vendor, modelKey(vendor, model))]); m != nil {
			return &Match{m, ByModel}
		}
	}
	if model != "" {
		if sfx := mountingSuffix[vendorKey(vendor)]; sfx != "" {
			if m := unique(ix.byKey[key(vendor, modelKey(vendor, model)+sfx)]); m != nil {
				return &Match{m, ByMounting}
			}
		}
	}
	if partNumber != "" {
		if m := unique(ix.byKey[key(vendor, norm(partNumber))]); m != nil {
			return &Match{m, ByPartNumber}
		}
	}
	if model != "" {
		if m := ix.variants[key(vendor, modelKey(vendor, model))]; m != nil {
			return &Match{m, ByVariant}
		}
	}
	return nil
}

func unique(ms []*Model) *Model {
	if len(ms) == 1 {
		return ms[0]
	}
	return nil
}

// Models returns every indexed model, sorted by slug.
func (ix *Index) Models() []*Model {
	seen := map[*Model]bool{}
	var out []*Model
	for _, ms := range ix.byKey {
		for _, m := range ms {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}
