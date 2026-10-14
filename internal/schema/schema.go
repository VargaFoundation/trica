// Package schema is the type registry: what kinds of objects and relations
// exist, which attributes they carry, and which attributes identify them.
//
// Built-in types ship embedded (builtin.yaml). User types, fields, relations
// and enumerations are stored in the database and merged on top at runtime;
// the API, the search index, the MCP tools and the UI all read the merged
// registry, which is how a custom type is "immediately available everywhere"
// without code.
package schema

import (
	_ "embed"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Kind is the value kind of an attribute.
type Kind string

const (
	String Kind = "string"
	Text   Kind = "text"
	Int    Kind = "int"
	Float  Kind = "float"
	Bool   Kind = "bool"
	Time   Kind = "time"
	IP     Kind = "ip"
	Prefix Kind = "prefix"
	MAC    Kind = "mac"
	Enum   Kind = "enum"
	List   Kind = "list"
	JSON   Kind = "json"
)

var validKinds = map[Kind]bool{String: true, Text: true, Int: true, Float: true, Bool: true,
	Time: true, IP: true, Prefix: true, MAC: true, Enum: true, List: true, JSON: true}

// IdentityStrength classifies an identifying attribute.
type IdentityStrength string

const (
	// Strong identifiers are unique per type across the whole inventory
	// (serial number, hardware UUID, MAC address, Kubernetes UID).
	Strong IdentityStrength = "strong"
	// Weak identifiers are unique per type within the parent object only
	// (an interface name) or are hints that can be reused (hostname, IP).
	Weak IdentityStrength = "weak"
)

// Label is a localised label.
type Label struct {
	EN string `yaml:"en" json:"en"`
	FR string `yaml:"fr" json:"fr"`
}

// Attribute describes one attribute of a type.
type Attribute struct {
	Name        string           `yaml:"-" json:"name"`
	Kind        Kind             `yaml:"kind" json:"kind"`
	Label       Label            `yaml:"label" json:"label"`
	Description string           `yaml:"description" json:"description"`
	Unit        string           `yaml:"unit,omitempty" json:"unit,omitempty"`
	Values      []string         `yaml:"values,omitempty" json:"values,omitempty"`
	Identity    IdentityStrength `yaml:"identity,omitempty" json:"identity,omitempty"`
	Search      bool             `yaml:"search,omitempty" json:"search,omitempty"`
	Required    bool             `yaml:"required,omitempty" json:"required,omitempty"`
	Custom      bool             `yaml:"-" json:"custom"`
}

// Type describes an object type.
type Type struct {
	Name        string                `yaml:"-" json:"name"`
	Layer       string                `yaml:"layer" json:"layer"`
	Label       Label                 `yaml:"label" json:"label"`
	Description string                `yaml:"description" json:"description"`
	Display     string                `yaml:"display" json:"display"`
	Parents     []string              `yaml:"parents,omitempty" json:"parents,omitempty"`
	Attributes  map[string]*Attribute `yaml:"attributes" json:"attributes"`
	Custom      bool                  `yaml:"-" json:"custom"`
}

// RelationType describes a relation type.
type RelationType struct {
	Name        string                `yaml:"-" json:"name"`
	Label       Label                 `yaml:"label" json:"label"`
	Inverse     Label                 `yaml:"inverse" json:"inverse"`
	Description string                `yaml:"description" json:"description"`
	From        []string              `yaml:"from" json:"from"`
	To          []string              `yaml:"to" json:"to"`
	Attributes  map[string]*Attribute `yaml:"attributes,omitempty" json:"attributes,omitempty"`
	// Impact tells the impact engine how failure travels along this relation.
	Impact string `yaml:"impact,omitempty" json:"impact,omitempty"`
	Custom bool   `yaml:"-" json:"custom"`
}

// Impact propagation modes of a relation.
const (
	// ImpactNone: failure does not travel along the relation.
	ImpactNone = ""
	// ImpactToFrom: when To fails, From fails (A hosted-by B: B down ⇒ A down).
	ImpactToFrom = "to_from"
	// ImpactFromTo: when From fails, To fails (A contains B: A down ⇒ B down).
	ImpactFromTo = "from_to"
	// ImpactSupply: To supplies From; From fails when ALL its suppliers of the
	// same relation type failed (redundant power feeds, uplinks).
	ImpactSupply = "supply"
	// ImpactLink: physical or logical link carrying connectivity; used by the
	// reachability pass, not by direct propagation.
	ImpactLink = "link"
	// ImpactDegrade: when To fails, From is degraded but not down (a soft
	// call between components, with a fallback or a retry).
	ImpactDegrade = "degrade"
)

// Registry is an immutable, merged view of all types.
type Registry struct {
	Types     map[string]*Type         `json:"types"`
	Relations map[string]*RelationType `json:"relations"`
}

//go:embed builtin.yaml
var builtinYAML []byte

type file struct {
	Types     map[string]*Type         `yaml:"types"`
	Relations map[string]*RelationType `yaml:"relations"`
}

// Builtin returns the built-in registry. It panics if the embedded file is
// invalid: that is a build defect, caught by the package tests.
func Builtin() *Registry {
	r, err := Parse(builtinYAML, false)
	if err != nil {
		panic(fmt.Sprintf("schema: builtin.yaml: %v", err))
	}
	return r
}

// Parse reads a registry from YAML. custom marks every definition as
// user-defined. A custom registry is an overlay: it may reference built-in
// types it does not define, so it is only validated once merged (Merge).
func Parse(data []byte, custom bool) (*Registry, error) {
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	r := &Registry{Types: map[string]*Type{}, Relations: map[string]*RelationType{}}
	for name, t := range f.Types {
		if t == nil {
			t = &Type{}
		}
		t.Name = name
		t.Custom = custom
		if t.Attributes == nil {
			t.Attributes = map[string]*Attribute{}
		}
		for an, a := range t.Attributes {
			a.Name = an
			a.Custom = custom
		}
		r.Types[name] = t
	}
	for name, rt := range f.Relations {
		if rt == nil {
			rt = &RelationType{}
		}
		rt.Name = name
		rt.Custom = custom
		for an, a := range rt.Attributes {
			a.Name = an
			a.Custom = custom
		}
		r.Relations[name] = rt
	}
	if custom {
		return r, nil
	}
	return r, r.Validate()
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Validate checks internal consistency.
func (r *Registry) Validate() error {
	for name, t := range r.Types {
		if !nameRe.MatchString(name) {
			return fmt.Errorf("type %q: invalid name", name)
		}
		if t.Display == "" {
			t.Display = "name"
		}
		for _, p := range t.Parents {
			if _, ok := r.Types[p]; !ok && p != "*" {
				return fmt.Errorf("type %s: unknown parent type %q", name, p)
			}
		}
		for an, a := range t.Attributes {
			if err := validAttr(an, a); err != nil {
				return fmt.Errorf("type %s: %w", name, err)
			}
		}
	}
	for name, rt := range r.Relations {
		if !nameRe.MatchString(name) {
			return fmt.Errorf("relation %q: invalid name", name)
		}
		for _, side := range [][]string{rt.From, rt.To} {
			if len(side) == 0 {
				return fmt.Errorf("relation %s: empty endpoint type list", name)
			}
			for _, tn := range side {
				if _, ok := r.Types[tn]; !ok && tn != "*" {
					return fmt.Errorf("relation %s: unknown type %q", name, tn)
				}
			}
		}
		switch rt.Impact {
		case ImpactNone, ImpactToFrom, ImpactFromTo, ImpactSupply, ImpactLink, ImpactDegrade:
		default:
			return fmt.Errorf("relation %s: unknown impact mode %q", name, rt.Impact)
		}
		for an, a := range rt.Attributes {
			if err := validAttr(an, a); err != nil {
				return fmt.Errorf("relation %s: %w", name, err)
			}
		}
	}
	return nil
}

func validAttr(name string, a *Attribute) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("attribute %q: invalid name", name)
	}
	if !validKinds[a.Kind] {
		return fmt.Errorf("attribute %s: unknown kind %q", name, a.Kind)
	}
	if a.Kind == Enum && len(a.Values) == 0 {
		return fmt.Errorf("attribute %s: enum without values", name)
	}
	if a.Identity != "" && a.Identity != Strong && a.Identity != Weak {
		return fmt.Errorf("attribute %s: identity must be strong or weak", name)
	}
	// Searchable and identity attributes feed the search index, where a
	// free-text query would reveal by presence or absence a value the API
	// never returns (issue #25).
	if Sensitive(name) && (a.Search || a.Identity != "") {
		return fmt.Errorf("attribute %s: a sensitive attribute cannot be searchable or identifying", name)
	}
	return nil
}

// sensitiveMarkers are substrings of attribute names that hold secrets.
// The model has no secret kind (credentials are references, AGENTS.md), but
// custom types are user-defined: names are the last line of defence. A false
// positive hides one attribute, a false negative leaks a credential.
var sensitiveMarkers = []string{
	"password", "passwd", "passphrase", "secret", "token", "credential",
	"private_key", "privatekey", "api_key", "apikey", "community", "psk",
}

// Sensitive reports whether an attribute name says it holds a secret. Such an
// attribute is never searchable nor identifying; readers (API, MCP, UI) must
// never return it.
func Sensitive(name string) bool {
	n := strings.ToLower(name)
	for _, m := range sensitiveMarkers {
		if strings.Contains(n, m) {
			return true
		}
	}
	return false
}

// Merge returns a new registry with overlay applied on top of r. Overlay types
// may add attributes to existing types but never change a built-in
// attribute's kind: stored values must stay readable.
func (r *Registry) Merge(overlay *Registry) (*Registry, error) {
	out := &Registry{Types: map[string]*Type{}, Relations: map[string]*RelationType{}}
	for n, t := range r.Types {
		c := *t
		c.Attributes = map[string]*Attribute{}
		for an, a := range t.Attributes {
			ac := *a
			c.Attributes[an] = &ac
		}
		out.Types[n] = &c
	}
	for n, rt := range r.Relations {
		c := *rt
		out.Relations[n] = &c
	}
	if overlay == nil {
		return out, nil
	}
	for n, t := range overlay.Types {
		base, ok := out.Types[n]
		if !ok {
			c := *t
			out.Types[n] = &c
			continue
		}
		for an, a := range t.Attributes {
			if existing, ok := base.Attributes[an]; ok && existing.Kind != a.Kind {
				return nil, fmt.Errorf("type %s: attribute %s: cannot change kind %s to %s", n, an, existing.Kind, a.Kind)
			}
			ac := *a
			base.Attributes[an] = &ac
		}
		if len(t.Parents) > 0 {
			base.Parents = appendUnique(base.Parents, t.Parents...)
		}
	}
	for n, rt := range overlay.Relations {
		if base, ok := out.Relations[n]; ok {
			base.From = appendUnique(base.From, rt.From...)
			base.To = appendUnique(base.To, rt.To...)
			continue
		}
		c := *rt
		out.Relations[n] = &c
	}
	return out, out.Validate()
}

func appendUnique(dst []string, src ...string) []string {
	seen := map[string]bool{}
	for _, s := range dst {
		seen[s] = true
	}
	for _, s := range src {
		if !seen[s] {
			dst = append(dst, s)
			seen[s] = true
		}
	}
	return dst
}

// Type returns a type or an error naming the known ones.
func (r *Registry) Type(name string) (*Type, error) {
	t, ok := r.Types[name]
	if !ok {
		return nil, fmt.Errorf("unknown object type %q", name)
	}
	return t, nil
}

// Relation returns a relation type.
func (r *Registry) Relation(name string) (*RelationType, error) {
	rt, ok := r.Relations[name]
	if !ok {
		return nil, fmt.Errorf("unknown relation type %q", name)
	}
	return rt, nil
}

// Allows reports whether relation rt may link fromType to toType.
func (rt *RelationType) Allows(fromType, toType string) bool {
	return contains(rt.From, fromType) && contains(rt.To, toType)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s || x == "*" {
			return true
		}
	}
	return false
}

// TypeNames returns sorted type names.
func (r *Registry) TypeNames() []string {
	out := make([]string, 0, len(r.Types))
	for n := range r.Types {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// AttributeNames returns the sorted attribute names of t.
func (t *Type) AttributeNames() []string {
	out := make([]string, 0, len(t.Attributes))
	for n := range t.Attributes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// IdentityKeys returns the strong and weak identifying attributes of t.
func (t *Type) IdentityKeys() (strong, weak []string) {
	for _, n := range t.AttributeNames() {
		switch t.Attributes[n].Identity {
		case Strong:
			strong = append(strong, n)
		case Weak:
			weak = append(weak, n)
		}
	}
	return strong, weak
}

// AllowsParent reports whether p may be the parent type of t.
func (t *Type) AllowsParent(p string) bool { return contains(t.Parents, p) }

// Normalize converts v to the canonical stored form for attribute a, or
// returns an error. Canonical forms make search and identity work across
// formats: a MAC written "AA-BB-CC-DD-EE-FF", "aabb.ccdd.eeff" or
// "aa:bb:cc:dd:ee:ff" is stored once.
func Normalize(a *Attribute, v any) (any, error) {
	switch a.Kind {
	case String, Text:
		s, ok := v.(string)
		if !ok {
			return fmt.Sprint(v), nil
		}
		return s, nil
	case Int:
		switch n := v.(type) {
		case int:
			return int64(n), nil
		case int32:
			return int64(n), nil
		case int64:
			return n, nil
		case uint32:
			return int64(n), nil
		case uint64:
			return int64(n), nil
		case float64:
			if n != float64(int64(n)) {
				return nil, fmt.Errorf("%s: %v is not an integer", a.Name, v)
			}
			return int64(n), nil
		case float32:
			return int64(n), nil
		}
		return nil, fmt.Errorf("%s: %T is not an integer", a.Name, v)
	case Float:
		switch n := v.(type) {
		case float64:
			return n, nil
		case float32:
			return float64(n), nil
		case int:
			return float64(n), nil
		case int64:
			return float64(n), nil
		case uint64:
			return float64(n), nil
		}
		return nil, fmt.Errorf("%s: %T is not a number", a.Name, v)
	case Bool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("%s: %T is not a boolean", a.Name, v)
		}
		return b, nil
	case Time:
		switch t := v.(type) {
		case time.Time:
			return t.UTC().Format(time.RFC3339), nil
		case string:
			for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
				if p, err := time.Parse(layout, t); err == nil {
					return p.UTC().Format(time.RFC3339), nil
				}
			}
		}
		return nil, fmt.Errorf("%s: %v is not a time", a.Name, v)
	case IP:
		s, _ := v.(string)
		addr, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			// Accept "10.0.0.1/24": the address part is what identifies.
			if p, perr := netip.ParsePrefix(strings.TrimSpace(s)); perr == nil {
				return p.Addr().String(), nil
			}
			return nil, fmt.Errorf("%s: %q is not an IP address", a.Name, s)
		}
		return addr.Unmap().String(), nil
	case Prefix:
		s, _ := v.(string)
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a prefix", a.Name, s)
		}
		return p.Masked().String(), nil
	case MAC:
		s, _ := v.(string)
		m, err := NormalizeMAC(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.Name, err)
		}
		return m, nil
	case Enum:
		s := strings.ToLower(fmt.Sprint(v))
		for _, allowed := range a.Values {
			if s == allowed {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%s: %q is not one of %v", a.Name, s, a.Values)
	case List:
		switch l := v.(type) {
		case []string:
			out := append([]string(nil), l...)
			sort.Strings(out)
			return out, nil
		case []any:
			out := make([]string, 0, len(l))
			for _, x := range l {
				out = append(out, fmt.Sprint(x))
			}
			sort.Strings(out)
			return out, nil
		}
		return nil, fmt.Errorf("%s: %T is not a list", a.Name, v)
	case JSON:
		return v, nil
	}
	return nil, fmt.Errorf("%s: unknown kind %s", a.Name, a.Kind)
}

// NormalizeMAC returns the lowercase colon form of a MAC address written in
// any common notation (colons, dashes, Cisco dots, bare hex).
func NormalizeMAC(s string) (string, error) {
	s = strings.TrimSpace(s)
	if hw, err := net.ParseMAC(s); err == nil && len(hw) == 6 {
		return hw.String(), nil
	}
	hex := strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(s))
	if len(hex) != 12 {
		return "", fmt.Errorf("%q is not a MAC address", s)
	}
	for _, c := range hex {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", fmt.Errorf("%q is not a MAC address", s)
		}
	}
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(hex[i : i+2])
	}
	return b.String(), nil
}

// NormalizeIdentity normalises an identifier value according to the
// attribute that carries it, falling back to trimmed lowercase text. Identity
// matching is case-insensitive: "SW-A1" and "sw-a1" are the same switch.
func (t *Type) NormalizeIdentity(key, value string) (string, error) {
	if a, ok := t.Attributes[key]; ok {
		switch a.Kind {
		case MAC, IP, Prefix:
			v, err := Normalize(a, value)
			if err != nil {
				return "", err
			}
			return v.(string), nil
		}
	}
	return strings.ToLower(strings.TrimSpace(value)), nil
}
