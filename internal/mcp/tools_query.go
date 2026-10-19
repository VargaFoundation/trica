package mcp

import (
	"regexp"
	"strings"

	"github.com/VargaFoundation/trica/internal/api"
	"github.com/VargaFoundation/trica/internal/tql"
)

const (
	descSearch = `Find objects by free text (a name, IP or MAC address in any notation, serial number, id) or by a TQL query. ` +
		`Use it first when you do not know what an object is: "192.0.2.13" finds the device, port or service that holds that address. ` +
		`TQL: type:device vendor:arista os_version<4.20 sort:-power_w limit:10, (a OR b), -has:serial, rel.mounted_in:"rack 2", site:paris (a site and everything in it), status:absent. ` +
		`The answer starts with a summary (total, by_type, truncated) then a page of objects.`
	descList = `List objects of one type (optionally narrowed by a TQL query) in a stable order, paginated. ` +
		`Use it to enumerate ("all racks", "all firewalls"); use search for free text and filter_objects for attribute conditions.`
	descFilter = `List objects whose attributes meet structured conditions: [{attribute:"airflow", op:"eq", value:"rear_to_front"}, {attribute:"wear_pct", op:"gt", value:"70"}]. ` +
		`Operators: eq (case-insensitive, * wildcard), ne, gt, ge, lt, le (numbers as numbers, versions as versions), contains, within (address in prefix), in (comma list), exists, missing. ` +
		`Conditions are combined with AND. The answer starts with a summary then a page.`
	descRank = `Top-N objects by an attribute: "the 10 devices that draw the most power" is rank_filter{type:"device", attribute:"power_w", n:10}. ` +
		`Order is desc by default. Objects without the attribute are left out. Values are the current ones in the inventory (not averages over a period). ` +
		`Add filters or a TQL query to narrow the population (role:server).`
)

// SearchInput is the input of the search tool.
type SearchInput struct {
	Query  string   `json:"query" jsonschema:"free text (name, address, MAC, serial) or a TQL query such as: type:device vendor:arista os_version<4.20 sort:-power_w limit:10"`
	Fields []string `json:"fields,omitempty" jsonschema:"attributes to return for each object (default: all of them)"`
	PageArgs
}

func (s *Server) toolSearch(t *tc, in SearchInput) (any, error) {
	t.target = in.Query
	q, err := t.g.Query(in.Query, "", "", true)
	if err != nil {
		return nil, err
	}
	ans, _, err := t.runQuery(q, in.Query, in.PageArgs, in.Fields)
	return ans, err
}

// ListInput is the input of the list_objects tool.
type ListInput struct {
	Type   string   `json:"type,omitempty" jsonschema:"object type to list, e.g. device, rack, interface (see describe_schema)"`
	Query  string   `json:"query,omitempty" jsonschema:"optional TQL query narrowing the list"`
	Sort   string   `json:"sort,omitempty" jsonschema:"comma-separated fields, - for descending: -power_w,display"`
	Fields []string `json:"fields,omitempty" jsonschema:"attributes to return for each object (default: all of them)"`
	PageArgs
}

func (s *Server) toolList(t *tc, in ListInput) (any, error) {
	t.target = in.Type
	q, err := t.g.Query(in.Query, in.Sort, in.Type, false)
	if err != nil {
		return nil, err
	}
	ans, _, err := t.runQuery(q, in.Query, in.PageArgs, in.Fields)
	return ans, err
}

// Filter is one condition on an attribute.
type Filter struct {
	Attribute string `json:"attribute" jsonschema:"attribute name (see describe_schema), or display, status, parent, first_seen, last_seen"`
	Op        string `json:"op" jsonschema:"eq, ne, gt, ge, lt, le, contains, within, in, exists or missing"`
	Value     string `json:"value,omitempty" jsonschema:"the value to compare with (for in: a comma-separated list; not used by exists and missing)"`
}

// FilterInput is the input of the filter_objects tool.
type FilterInput struct {
	Type    string   `json:"type,omitempty" jsonschema:"object type to filter, e.g. device (default: every type you may read)"`
	Filters []Filter `json:"filters" jsonschema:"conditions, all of which must hold"`
	Query   string   `json:"query,omitempty" jsonschema:"optional TQL query combined with the filters"`
	Sort    string   `json:"sort,omitempty" jsonschema:"comma-separated fields, - for descending"`
	Fields  []string `json:"fields,omitempty" jsonschema:"attributes to return for each object (default: all of them)"`
	PageArgs
}

var fieldRe = regexp.MustCompile(`^[a-z_][a-z0-9_.]*$`)

var filterOps = map[string]string{
	"eq": tql.OpEq, "ne": tql.OpNe, "gt": tql.OpGt, "ge": tql.OpGe, "lt": tql.OpLt, "le": tql.OpLe,
	"contains": tql.OpContains, "within": tql.OpWithin, "in": tql.OpEq,
}

// node turns a filter into a syntax tree node, never into query text, so that
// a value cannot smuggle in another condition.
func (f Filter) node() (tql.Node, error) {
	attr := strings.TrimSpace(f.Attribute)
	if !fieldRe.MatchString(attr) {
		return nil, api.BadRequest("filter attribute %q is not a field name", f.Attribute)
	}
	if api.Sensitive(attr) {
		return nil, api.Forbidden("the attribute %q is not exposed and cannot be queried", attr)
	}
	switch f.Op {
	case "exists":
		return tql.Pred{Field: "has", Op: tql.OpEq, Values: []string{attr}}, nil
	case "missing":
		return tql.Not{Term: tql.Pred{Field: "has", Op: tql.OpEq, Values: []string{attr}}}, nil
	}
	op, ok := filterOps[f.Op]
	if !ok {
		return nil, api.BadRequest("filter op %q is not one of eq, ne, gt, ge, lt, le, contains, within, in, exists, missing", f.Op)
	}
	if strings.TrimSpace(f.Value) == "" {
		return nil, api.BadRequest("filter on %q needs a value", attr)
	}
	values := []string{f.Value}
	if f.Op == "in" {
		values = values[:0]
		for _, v := range strings.Split(f.Value, ",") {
			if v = strings.TrimSpace(v); v != "" {
				values = append(values, v)
			}
		}
	}
	return tql.Pred{Field: attr, Op: op, Values: values}, nil
}

// and adds conditions to a query.
func and(q *tql.Query, nodes ...tql.Node) {
	terms := nodes
	if q.Where != nil {
		terms = append([]tql.Node{q.Where}, nodes...)
	}
	switch len(terms) {
	case 0:
	case 1:
		q.Where = terms[0]
	default:
		q.Where = tql.And{Terms: terms}
	}
}

func filterNodes(filters []Filter) ([]tql.Node, error) {
	nodes := make([]tql.Node, 0, len(filters))
	for _, f := range filters {
		n, err := f.node()
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func (s *Server) toolFilter(t *tc, in FilterInput) (any, error) {
	t.target = in.Type
	if len(in.Filters) == 0 && strings.TrimSpace(in.Query) == "" {
		return nil, api.BadRequest("give at least one filter (or a query); to list a type use list_objects")
	}
	q, err := t.g.Query(in.Query, in.Sort, in.Type, false)
	if err != nil {
		return nil, err
	}
	nodes, err := filterNodes(in.Filters)
	if err != nil {
		return nil, err
	}
	and(q, nodes...)
	if err := t.g.Check(q); err != nil {
		return nil, err
	}
	ans, _, err := t.runQuery(q, describeFilters(in), in.PageArgs, in.Fields)
	return ans, err
}

func describeFilters(in FilterInput) string {
	parts := make([]string, 0, len(in.Filters)+2)
	if in.Type != "" {
		parts = append(parts, "type="+in.Type)
	}
	for _, f := range in.Filters {
		parts = append(parts, f.Attribute+" "+f.Op+" "+f.Value)
	}
	if in.Query != "" {
		parts = append(parts, "query: "+in.Query)
	}
	return strings.Join(parts, "; ")
}

// RankInput is the input of the rank_filter tool.
type RankInput struct {
	Attribute string   `json:"attribute" jsonschema:"the attribute to rank by, e.g. power_w, wear_pct, inlet_temp_c"`
	Type      string   `json:"type,omitempty" jsonschema:"object type to rank, e.g. device"`
	Order     string   `json:"order,omitempty" jsonschema:"desc (highest first, the default) or asc"`
	N         int      `json:"n,omitempty" jsonschema:"how many objects to return, 1 to 1000 (default 10)"`
	Filters   []Filter `json:"filters,omitempty" jsonschema:"conditions narrowing the population, all of which must hold"`
	Query     string   `json:"query,omitempty" jsonschema:"optional TQL query narrowing the population, e.g. role:server"`
	Fields    []string `json:"fields,omitempty" jsonschema:"other attributes to return besides the ranked one"`
}

// RankedItem is one row of a ranking.
type RankedItem struct {
	Rank       int            `json:"rank"`
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Display    string         `json:"display"`
	Value      any            `json:"value"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

func (s *Server) toolRank(t *tc, in RankInput) (any, error) {
	attr := strings.TrimSpace(in.Attribute)
	t.target = in.Type + "/" + attr
	if !fieldRe.MatchString(attr) {
		return nil, api.BadRequest("attribute %q is not a field name", in.Attribute)
	}
	if api.Sensitive(attr) {
		return nil, api.Forbidden("the attribute %q is not exposed and cannot be queried", attr)
	}
	desc := true
	switch strings.ToLower(in.Order) {
	case "", "desc":
	case "asc":
		desc = false
	default:
		return nil, api.BadRequest("order must be desc or asc")
	}
	n := in.N
	if n == 0 {
		n = 10
	}
	n, _, err := api.PageBounds(n, 0)
	if err != nil {
		return nil, api.BadRequest("n must be an integer between 1 and %d", api.MaxLimit)
	}
	q, err := t.g.Query(in.Query, "", in.Type, false)
	if err != nil {
		return nil, err
	}
	nodes, err := filterNodes(in.Filters)
	if err != nil {
		return nil, err
	}
	nodes = append(nodes, tql.Pred{Field: "has", Op: tql.OpEq, Values: []string{attr}})
	and(q, nodes...)
	q.Sort = []tql.SortKey{{Field: attr, Desc: desc}}
	if err := t.g.Check(q); err != nil {
		return nil, err
	}
	fields := append([]string{attr}, in.Fields...)
	ans, res, err := t.runQuery(q, "rank "+attr, PageArgs{Limit: n}, fields)
	if err != nil {
		return nil, err
	}
	items := objectItems(res.Items, fields)
	ranked := make([]RankedItem, 0, len(items))
	for i, it := range items {
		extra := map[string]any{}
		for k, v := range it.Attributes {
			if k != attr {
				extra[k] = v
			}
		}
		if len(extra) == 0 {
			extra = nil
		}
		ranked = append(ranked, RankedItem{Rank: i + 1, ID: it.ID, Type: it.Type, Display: it.Display, Value: it.Attributes[attr], Attributes: extra})
	}
	order := "desc"
	if !desc {
		order = "asc"
	}
	ans.Summary = struct {
		RankedBy string `json:"ranked_by"`
		Order    string `json:"order"`
		Summary
	}{RankedBy: attr, Order: order, Summary: newSummary(res.Total, res.Offset, len(items), res.ByType)}
	ans.Items = ranked
	return ans, nil
}
