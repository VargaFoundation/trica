package api

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/search"
	"github.com/VargaFoundation/trica/internal/store"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// hOpenAPI serves the OpenAPI document, generated now.
func (s *Server) hOpenAPI(c *call) error {
	doc, err := s.OpenAPI()
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, doc)
}

// OpenAPI generates the OpenAPI 3.1 document from the route table and the
// schema registry as they are now. Nothing in it is written by hand per
// endpoint or per object type: a type added to the registry has its schema
// in the next call.
func (s *Server) OpenAPI() (map[string]any, error) {
	g := newSchemaGen()
	reg := s.registry()
	types := reg.TypeNames()

	paths := map[string]any{}
	tagSet := map[string]bool{}
	for _, rt := range s.routes {
		op, err := s.operation(g, rt, types)
		if err != nil {
			return nil, err
		}
		tagSet[rt.Tag] = true
		item, _ := paths[rt.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[rt.Path] = item
		}
		item[strings.ToLower(rt.Method)] = op
	}
	tags := []any{}
	for _, t := range sortedSet(tagSet) {
		tags = append(tags, map[string]any{"name": t})
	}

	components := g.components
	objectSchemas, err := objectComponents(g, reg, types)
	if err != nil {
		return nil, err
	}
	for name, sc := range objectSchemas {
		components[name] = sc
	}
	problemRefs := map[string]any{}
	for _, status := range []int{400, 401, 403, 404, 409, 413, 422, 500, 503} {
		problemRefs["Problem"+strconv.Itoa(status)] = map[string]any{
			"description": titles[status],
			"content":     map[string]any{ProblemContentType: map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Problem"}}},
		}
	}
	if g.err != nil {
		return nil, g.err
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "trica API",
			"version":     s.version,
			"description": "Infrastructure inventory: historised graph, provenance, proposals. Generated from the route table and the schema registry.",
			"license":     map[string]any{"name": "Apache-2.0", "identifier": "Apache-2.0"},
		},
		"servers":  []any{map[string]any{"url": Prefix}},
		"security": []any{map[string]any{"bearerAuth": []any{}}},
		"tags":     tags,
		"paths":    paths,
		"components": map[string]any{
			"schemas":   components,
			"responses": problemRefs,
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{"type": "http", "scheme": "bearer", "description": "A token or session credential understood by the server's Authorizer."},
			},
		},
	}, nil
}

func (s *Server) operation(g *schemaGen, rt route, types []string) (map[string]any, error) {
	op := map[string]any{
		"operationId":    rt.ID,
		"summary":        rt.Summary,
		"tags":           []any{rt.Tag},
		"x-trica-action": rt.Action,
	}
	if rt.Action == "" {
		op["security"] = []any{}
		op["x-trica-action"] = "public"
	} else {
		op["description"] = fmt.Sprintf("Requires the %q action.", rt.Action)
	}
	if rt.Mutating {
		op["x-trica-audited"] = true
	}
	params := []any{}
	for _, p := range rt.Params {
		sc := map[string]any{}
		for k, v := range p.Schema {
			sc[k] = v
		}
		if sc[enumTypes] == true {
			delete(sc, enumTypes)
			vals := make([]any, len(types))
			for i, t := range types {
				vals[i] = t
			}
			sc["enum"] = vals
		}
		pm := map[string]any{"name": p.Name, "in": p.In, "description": p.Desc, "required": p.Required || p.In == "path", "schema": sc}
		params = append(params, pm)
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	if rt.Body != nil {
		op["requestBody"] = map[string]any{"required": !rt.BodyOptional, "content": map[string]any{"application/json": map[string]any{"schema": g.ref(reflect.TypeOf(rt.Body))}}}
	}
	responses := map[string]any{}
	for _, r := range rt.Responses {
		resp := map[string]any{"description": r.Desc}
		if r.Body != nil {
			resp["content"] = map[string]any{"application/json": map[string]any{"schema": g.ref(reflect.TypeOf(r.Body))}}
		}
		responses[strconv.Itoa(r.Status)] = resp
	}
	errs := append([]int(nil), rt.Errors...)
	errs = append(errs, http.StatusInternalServerError)
	if rt.Action != "" {
		errs = append(errs, http.StatusUnauthorized, http.StatusForbidden)
	}
	for _, e := range errs {
		responses[strconv.Itoa(e)] = map[string]any{"$ref": "#/components/responses/Problem" + strconv.Itoa(e)}
	}
	op["responses"] = responses
	return op, nil
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ----------------------------------------------------- per-type components

// objectComponents builds, for every object type of the registry, the schema
// of its attributes ("Attributes.<type>") and of its objects ("Object.<type>"),
// and the union "Object" every object response refers to.
func objectComponents(g *schemaGen, reg *schema.Registry, types []string) (map[string]any, error) {
	out := map[string]any{}
	base, err := g.structSchema(reflect.TypeOf(ObjectView{}))
	if err != nil {
		return nil, err
	}
	out["ObjectBase"] = base
	var union []any
	for _, name := range types {
		t := reg.Types[name]
		props := map[string]any{}
		for _, an := range sdk.SortedKeys(t.Attributes) {
			if Sensitive(an) {
				continue
			}
			props[an] = attributeSchema(t.Attributes[an])
		}
		out["Attributes."+name] = map[string]any{
			"type":                 "object",
			"description":          "Attributes of a " + name + " object. Other attributes may appear; those of the schema are typed.",
			"properties":           props,
			"additionalProperties": true,
		}
		out["Object."+name] = map[string]any{
			"allOf": []any{
				map[string]any{"$ref": "#/components/schemas/ObjectBase"},
				map[string]any{"type": "object", "properties": map[string]any{
					"type":       map[string]any{"const": name},
					"attributes": map[string]any{"$ref": "#/components/schemas/Attributes." + name},
				}},
			},
		}
		union = append(union, map[string]any{"$ref": "#/components/schemas/Object." + name})
	}
	out["Object"] = map[string]any{
		"description":   "An object of any type of the registry; `type` selects the schema of `attributes`.",
		"oneOf":         union,
		"discriminator": map[string]any{"propertyName": "type"},
	}
	return out, nil
}

// attributeSchema maps a registry attribute to a JSON schema.
func attributeSchema(a *schema.Attribute) map[string]any {
	sc := map[string]any{}
	switch a.Kind {
	case schema.String, schema.Text, schema.IP, schema.Prefix:
		sc["type"] = "string"
	case schema.Int:
		sc["type"] = "integer"
	case schema.Float:
		sc["type"] = "number"
	case schema.Bool:
		sc["type"] = "boolean"
	case schema.Time:
		sc["type"] = "string"
		sc["format"] = "date-time"
	case schema.MAC:
		sc["type"] = "string"
		sc["pattern"] = "^([0-9a-f]{2}:){5}[0-9a-f]{2}$"
	case schema.Enum:
		sc["type"] = "string"
		vals := make([]any, len(a.Values))
		for i, v := range a.Values {
			vals[i] = v
		}
		sc["enum"] = vals
	case schema.List:
		sc["type"] = "array"
		sc["items"] = map[string]any{"type": "string"}
	}
	// schema.JSON (and anything unknown) accepts any value.
	sc["x-trica-kind"] = string(a.Kind)
	if a.Unit != "" {
		sc["x-trica-unit"] = a.Unit
	}
	if a.Label.EN != "" || a.Label.FR != "" {
		sc["title"] = a.Label.EN
		sc["x-trica-label"] = map[string]any{"en": a.Label.EN, "fr": a.Label.FR}
	}
	if a.Description != "" {
		sc["description"] = a.Description
	}
	return sc
}

// ------------------------------------------------------------ Go -> schema

// schemaGen turns Go types into JSON schemas by reflection on their json
// tags, so the document cannot drift from what the handlers encode.
type schemaGen struct {
	components map[string]any
	owner      map[string]reflect.Type
	err        error
}

func newSchemaGen() *schemaGen {
	g := &schemaGen{components: map[string]any{}, owner: map[string]reflect.Type{}}
	// Problem details are used by every error response.
	g.ref(reflect.TypeOf(Problem{}))
	return g
}

var (
	timeType       = reflect.TypeOf(time.Time{})
	rawType        = reflect.TypeOf((*interface{ MarshalJSON() ([]byte, error) })(nil)).Elem()
	objectViewType = reflect.TypeOf(ObjectView{})
	apiPkgPath     = reflect.TypeOf(Problem{}).PkgPath()
	typeRenames    = map[reflect.Type]string{
		reflect.TypeOf(sdk.Object{}):              "BatchObject",
		reflect.TypeOf(sdk.Relation{}):            "BatchRelation",
		reflect.TypeOf(sdk.Endpoint{}):            "BatchEndpoint",
		reflect.TypeOf(sdk.Health{}):              "BatchHealth",
		reflect.TypeOf(store.Relation{}):          "Relation",
		reflect.TypeOf(reconcile.Result{}):        "ApplyResult",
		reflect.TypeOf(reconcile.PlannedChange{}): "PlannedChange",
		reflect.TypeOf(search.Saved{}):            "SavedQuery",
	}
)

// componentName is the name of a struct's component schema.
func componentName(t reflect.Type) string {
	if n, ok := typeRenames[t]; ok {
		return n
	}
	name := t.Name()
	if i := strings.Index(name, "["); i >= 0 {
		// Page[github.com/.../store.Value] -> PageValue
		inner := name[i+1 : len(name)-1]
		inner = strings.TrimPrefix(inner, "*")
		if j := strings.LastIndex(inner, "."); j >= 0 {
			inner = inner[j+1:]
		}
		return name[:i] + inner
	}
	return name
}

// ref returns the schema to use where t appears: a $ref for a struct, an
// inline schema otherwise.
func (g *schemaGen) ref(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == objectViewType {
		return map[string]any{"$ref": "#/components/schemas/Object"}
	}
	if t.Kind() != reflect.Struct || t == timeType {
		return g.inline(t)
	}
	name := componentName(t)
	if prev, seen := g.owner[name]; seen {
		if prev != t && g.err == nil {
			g.err = fmt.Errorf("openapi: two Go types share the component name %q (%s and %s)", name, prev, t)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	g.owner[name] = t
	g.components[name] = map[string]any{} // placeholder against recursion
	sc, err := g.structSchema(t)
	if err != nil && g.err == nil {
		g.err = err
	}
	g.components[name] = sc
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func (g *schemaGen) inline(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Implements(rawType) || reflect.PointerTo(t).Implements(rawType) {
			return map[string]any{} // json.RawMessage: any JSON
		}
		return map[string]any{"type": "array", "items": g.ref(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.ref(t.Elem())}
	case reflect.Struct:
		return g.ref(t)
	}
	return map[string]any{} // interfaces: any JSON
}

// nullable allows null next to s (OpenAPI 3.1: JSON Schema type unions).
func nullable(s map[string]any) map[string]any {
	if ref, ok := s["$ref"]; ok {
		return map[string]any{"oneOf": []any{map[string]any{"$ref": ref}, map[string]any{"type": "null"}}}
	}
	switch ty := s["type"].(type) {
	case string:
		out := map[string]any{}
		for k, v := range s {
			out[k] = v
		}
		out["type"] = []any{ty, "null"}
		return out
	case nil:
		return s // any JSON already includes null
	}
	return s
}

// structSchema builds the object schema of a struct from its json tags.
// A field without omitempty is required; a pointer, slice or map field of
// a type outside this package may be null (Go encodes nil as null), ours are
// built non-nil by the views.
func (g *schemaGen) structSchema(t reflect.Type) (map[string]any, error) {
	props := map[string]any{}
	var required []any
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if f.Anonymous && name == "" {
				ft := f.Type
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			omit := strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero")
			sc := g.ref(f.Type)
			if !omit && t.PkgPath() != apiPkgPath {
				switch f.Type.Kind() {
				case reflect.Pointer, reflect.Slice, reflect.Map:
					if f.Type != timeType {
						sc = nullable(sc)
					}
				}
			}
			props[name] = sc
			if !omit {
				required = append(required, name)
			}
		}
	}
	walk(t)
	sort.Slice(required, func(i, j int) bool { return required[i].(string) < required[j].(string) })
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out, nil
}
