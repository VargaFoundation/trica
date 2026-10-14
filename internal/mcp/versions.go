package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/internal/tql"
)

const descCompliance = `Compare the versions an attribute holds (os_version by default; also bios_version, bmc_version) with a reference, for every object of a type. ` +
	`Give target ("4.30.2") for a strict reference, or policy (the name of a version_policy object) for a reference with tolerated lag. ` +
	`The answer starts with the counts (compliant, within_tolerance, behind, unknown, by version) then lists the objects that are behind or unknown ` +
	`(statuses selects others). "Which switches run a firmware behind the reference" is version_compliance{type:"device", query:"role:switch", target:"4.30.2"}.`

// Compliance statuses.
const (
	statusCompliant       = "compliant"
	statusWithinTolerance = "within_tolerance"
	statusBehind          = "behind"
	statusUnknown         = "unknown"
)

var complianceStatuses = []string{statusBehind, statusUnknown, statusWithinTolerance, statusCompliant}

// ComplianceInput is the input of version_compliance.
type ComplianceInput struct {
	Type      string   `json:"type,omitempty" jsonschema:"object type to check (default device; with a policy, the policy's selector decides)"`
	Attribute string   `json:"attribute,omitempty" jsonschema:"attribute holding the version (default os_version; a policy brings its own)"`
	Target    string   `json:"target,omitempty" jsonschema:"reference version, e.g. 4.30.2 (strict: no lag tolerated unless max_minor_lag or max_patch_lag is given)"`
	Policy    string   `json:"policy,omitempty" jsonschema:"name of a version_policy object: reference version, selector and tolerated lag come from it"`
	MaxMinor  int      `json:"max_minor_lag,omitempty" jsonschema:"with target: minor versions an object may lag behind"`
	MaxPatch  int      `json:"max_patch_lag,omitempty" jsonschema:"with target: patch versions an object may lag behind"`
	Query     string   `json:"query,omitempty" jsonschema:"optional TQL query narrowing the objects checked, e.g. role:switch vendor:arista"`
	Statuses  []string `json:"statuses,omitempty" jsonschema:"which objects to list: behind, unknown, within_tolerance, compliant (default behind and unknown)"`
	PageArgs
}

// Lag says how far behind an object is.
type Lag struct {
	Level string `json:"level"` // major, minor or patch
	By    int    `json:"by"`
}

// ComplianceItem is one object checked against the reference.
type ComplianceItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Display   string `json:"display"`
	Status    string `json:"status"`
	Current   string `json:"current,omitempty"`
	Reference string `json:"reference"`
	Lag       *Lag   `json:"lag,omitempty"`
}

// ComplianceSummary says first how the governed objects stand.
type ComplianceSummary struct {
	Attribute       string         `json:"attribute"`
	Reference       string         `json:"reference_version"`
	Policy          string         `json:"policy,omitempty"`
	MaxMinorLag     int            `json:"max_minor_lag"`
	MaxPatchLag     int            `json:"max_patch_lag"`
	Governed        int            `json:"governed"`
	GovernedIsLower bool           `json:"governed_is_lower_bound,omitempty"`
	Compliant       int            `json:"compliant"`
	WithinTolerance int            `json:"within_tolerance"`
	Behind          int            `json:"behind"`
	Unknown         int            `json:"unknown"`
	ByVersion       map[string]int `json:"by_version"`
	BehindByType    map[string]int `json:"behind_by_type,omitempty"`
	Listed          Summary        `json:"listed"`
	ListedStatuses  []string       `json:"listed_statuses"`
	// Warning says why an empty answer may not mean "all is well".
	Warning string `json:"warning,omitempty"`
}

var digitRuns = regexp.MustCompile(`[0-9]+`)

// versionKey reads the numbers of a version: every run of digits, in order
// ("4.20.7M" is [4 20 7]). It is the reading of the SQL function
// trica_version_key the search uses, so that "os_version<4.20" and this tool
// agree on what a version is.
func versionKey(v string) []uint64 {
	runs := digitRuns.FindAllString(v, -1)
	key := make([]uint64, 0, len(runs))
	for _, r := range runs {
		n, err := strconv.ParseUint(r, 10, 64)
		if err != nil {
			n = math.MaxUint64
		}
		key = append(key, n)
	}
	return key
}

func component(k []uint64, i int) uint64 {
	if i < len(k) {
		return k[i]
	}
	return 0
}

// compareVersions orders two versions component by component, a missing
// component counting as zero ("4.20" equals "4.20.0"). SQL's comparison of
// the same keys ranks "4.20" below "4.20.0"; for compliance the two are the
// same release.
func compareVersions(a, b []uint64) int {
	for i := 0; i < max(len(a), len(b)); i++ {
		switch x, y := component(a, i), component(b, i); {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

// classify places a version against a reference and the lag tolerated.
func classify(current, ref []uint64, maxMinor, maxPatch int) (string, *Lag) {
	if len(current) == 0 {
		return statusUnknown, nil
	}
	if compareVersions(current, ref) >= 0 {
		return statusCompliant, nil
	}
	if component(current, 0) != component(ref, 0) {
		return statusBehind, &Lag{Level: "major", By: int(min(component(ref, 0)-component(current, 0), math.MaxInt32))}
	}
	if component(current, 1) != component(ref, 1) {
		by := int(min(component(ref, 1)-component(current, 1), math.MaxInt32))
		if by <= maxMinor {
			return statusWithinTolerance, &Lag{Level: "minor", By: by}
		}
		return statusBehind, &Lag{Level: "minor", By: by}
	}
	// Same major and minor: the patch decides; a difference below the patch
	// level counts as one patch.
	by := 1
	if component(ref, 2) > component(current, 2) {
		by = int(min(component(ref, 2)-component(current, 2), math.MaxInt32))
	}
	if by <= maxPatch {
		return statusWithinTolerance, &Lag{Level: "patch", By: by}
	}
	return statusBehind, &Lag{Level: "patch", By: by}
}

// lagScore orders lags: a major version behind outweighs any number of minors,
// a minor any number of patches. No lag scores zero.
func lagScore(l *Lag) int {
	if l == nil {
		return 0
	}
	level := map[string]int{"major": 2, "minor": 1}[l.Level]
	return level*1_000_000_000 + min(l.By, 999_999_999)
}

// number reads an integer attribute however the JSON decoding left it.
func number(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

func text(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(s)
	default:
		return strings.TrimSpace(fmt.Sprint(s))
	}
}

// policy is what a compliance check needs from a version_policy object.
type policy struct {
	name, attribute, selector, reference string
	maxMinor, maxPatch                   int
}

func (t *tc) loadPolicy(ref string) (*policy, error) {
	o, err := t.resolve(ref)
	if err != nil {
		return nil, err
	}
	if o.Type != "version_policy" {
		return nil, api.BadRequest("%q is a %s, not a version_policy", ref, o.Type)
	}
	p := &policy{name: o.Display, attribute: text(o.Attrs["attribute"]), selector: text(o.Attrs["selector"]),
		reference: text(o.Attrs["reference_version"]), maxMinor: number(o.Attrs["max_minor_lag"]), maxPatch: number(o.Attrs["max_patch_lag"])}
	if p.reference == "" {
		return nil, api.BadRequest("the policy %q has no reference_version", p.name)
	}
	if p.attribute == "" {
		return nil, api.BadRequest("the policy %q has no attribute: only policies that name the attribute holding the version are evaluated", p.name)
	}
	return p, nil
}

func (s *Server) toolCompliance(t *tc, in ComplianceInput) (any, error) {
	t.target = in.Type
	limit, offset, err := in.bounds()
	if err != nil {
		return nil, err
	}
	statuses := in.Statuses
	if len(statuses) == 0 {
		statuses = []string{statusBehind, statusUnknown}
	}
	for _, st := range statuses {
		if !slices.Contains(complianceStatuses, st) {
			return nil, api.BadRequest("statuses must be among %s", strings.Join(complianceStatuses, ", "))
		}
	}
	var (
		attribute = strings.TrimSpace(in.Attribute)
		reference = strings.TrimSpace(in.Target)
		selector  string
		pol       string
		maxMinor  = in.MaxMinor
		maxPatch  = in.MaxPatch
		typeName  = in.Type
	)
	switch {
	case in.Policy != "" && in.Target != "":
		return nil, api.BadRequest("give a target or a policy, not both")
	case in.Policy != "":
		t.target = in.Policy
		p, err := t.loadPolicy(in.Policy)
		if err != nil {
			return nil, err
		}
		attribute, reference, selector, pol, maxMinor, maxPatch = p.attribute, p.reference, p.selector, p.name, p.maxMinor, p.maxPatch
	case in.Target == "":
		return nil, api.BadRequest("give a target version or a policy")
	default:
		if typeName == "" {
			typeName = "device"
		}
		if attribute == "" {
			attribute = "os_version"
		}
	}
	if maxMinor < 0 || maxPatch < 0 {
		return nil, api.BadRequest("tolerated lags must not be negative")
	}
	if !fieldRe.MatchString(attribute) {
		return nil, api.BadRequest("attribute %q is not an attribute name", attribute)
	}
	if api.Sensitive(attribute) {
		return nil, api.Forbidden("the attribute %q is not exposed and cannot be queried", attribute)
	}
	refKey := versionKey(reference)
	if len(refKey) == 0 {
		return nil, api.BadRequest("the reference version %q has no number in it", reference)
	}
	q, err := t.g.Query(in.Query, "", typeName, false)
	if err != nil {
		return nil, err
	}
	if selector != "" {
		sq, err := tql.Parse(selector)
		if err != nil {
			return nil, api.BadRequest("the selector of policy %q is not a TQL query (%v)", pol, err)
		}
		if sq.Where != nil {
			and(q, sq.Where)
		}
		if err := t.g.Check(q); err != nil {
			return nil, err
		}
	}

	// Read the governed objects, a page at a time, up to the scan bound.
	var objs []*store.Object
	total := 0
	for off := 0; off < api.MaxScan; off += api.MaxLimit {
		res, err := s.searcher.SearchQuery(t.ctx, q, "version_compliance", search.Page{Limit: api.MaxLimit, Offset: off})
		if err != nil {
			return nil, err
		}
		total = res.Total
		objs = append(objs, res.Items...)
		if len(res.Items) < api.MaxLimit || len(objs) >= res.Total {
			break
		}
	}
	sum := ComplianceSummary{Attribute: attribute, Reference: reference, Policy: pol, MaxMinorLag: maxMinor, MaxPatchLag: maxPatch,
		Governed: total, GovernedIsLower: len(objs) < total, ByVersion: map[string]int{}, BehindByType: map[string]int{}, ListedStatuses: statuses}
	if total == 0 && selector != "" {
		// A selector can match no object (a malformed one is refused when
		// written): "nothing governed" must not read as "nothing behind".
		sum.Warning = fmt.Sprintf("the selector of policy %q (%s) matched no object: a selector is a search query such as role:switch", pol, selector)
	}
	var items []ComplianceItem
	byType := map[string]int{}
	for _, o := range objs {
		cur := text(o.Attrs[attribute])
		status, lag := classify(versionKey(cur), refKey, maxMinor, maxPatch)
		if cur == "" {
			sum.ByVersion["(none)"]++
		} else {
			sum.ByVersion[cur]++
		}
		switch status {
		case statusCompliant:
			sum.Compliant++
		case statusWithinTolerance:
			sum.WithinTolerance++
		case statusBehind:
			sum.Behind++
			sum.BehindByType[o.Type]++
		default:
			sum.Unknown++
		}
		if slices.Contains(statuses, status) {
			items = append(items, ComplianceItem{ID: o.ID, Type: o.Type, Display: o.Display, Status: status, Current: cur, Reference: reference, Lag: lag})
			byType[o.Type]++
		}
	}
	// Worst first: behind, unknown, within tolerance, compliant; within a
	// status the furthest behind, then by name.
	rank := func(s string) int { return slices.Index(complianceStatuses, s) }
	sort.SliceStable(items, func(i, j int) bool {
		if ri, rj := rank(items[i].Status), rank(items[j].Status); ri != rj {
			return ri < rj
		}
		if li, lj := lagScore(items[i].Lag), lagScore(items[j].Lag); li != lj {
			return li > lj
		}
		return items[i].Display < items[j].Display
	})
	page := api.PageOf(items, limit, offset, sum.GovernedIsLower)
	sum.Listed = newSummary(page.Total, offset, len(page.Items), byType)
	sum.Listed.TotalIsLowerBound = sum.GovernedIsLower
	return &Answer{Summary: sum, Limit: limit, Offset: offset, Items: page.Items}, nil
}
