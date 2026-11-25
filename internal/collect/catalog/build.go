package catalog

import (
	"strings"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Build turns a validated catalog into the objects and relations of a batch.
// Every value is declared: the batch's confidence is set by the caller.
//
// An application, a component and an environment carry the identities the
// kubernetes collector gives them, so that what is declared here and what is
// discovered there is one object (ADR 0006). A dependency or a host that is
// not in this catalog is designated by identity: the engine finds it in the
// inventory, or records an unresolved reference.
func Build(b *sdk.Batch, d *Document) {
	x := builder{b: b, envs: map[string]bool{}, linked: map[string]bool{}}
	for _, t := range d.Teams {
		o := b.Object(teamRef(t.Name), "team").Identify("name", strings.TrimSpace(t.Name))
		set(o, "email", t.Email)
		set(o, "chat", t.Chat)
	}
	for _, p := range d.People {
		e := strings.ToLower(strings.TrimSpace(p.Email))
		o := b.Object(personRef(e), "person").Identify("email", e)
		set(o, "name", p.Name)
		set(o, "phone", p.Phone)
	}
	for _, e := range d.Environments {
		set(x.env(e.Name), "kind", e.Kind)
	}
	apps := map[string]bool{}
	for _, a := range d.Applications {
		apps[a.Name] = true
	}
	for _, a := range d.Applications {
		x.application(a, apps)
	}
}

type builder struct {
	b      *sdk.Batch
	envs   map[string]bool
	linked map[string]bool
}

func teamRef(name string) string    { return "team/" + strings.TrimSpace(name) }
func personRef(email string) string { return "person/" + strings.ToLower(strings.TrimSpace(email)) }
func appRef(name string) string     { return "app/" + name }

// set sets an attribute only when the catalog gives it: an empty value is
// "not declared", not "declared empty".
func set(o *sdk.Object, key, v string) {
	if v = strings.TrimSpace(v); v != "" {
		o.Set(key, v)
	}
}

// env returns the environment object, creating it on first use.
func (x *builder) env(name string) *sdk.Object {
	name = strings.TrimSpace(name)
	return x.b.Object("env/"+name, "environment").Identify("name", name)
}

func (x *builder) application(a Application, apps map[string]bool) {
	ref := appRef(a.Name)
	o := x.b.Object(ref, "application").Identify("name", a.Name)
	set(o, "display_name", a.DisplayName)
	set(o, "description", a.Description)
	set(o, "domain", a.Domain)
	set(o, "criticality", a.Criticality)
	set(o, "lifecycle", a.Lifecycle)
	set(o, "hosting", a.Hosting)
	set(o, "vendor", a.Vendor)
	set(o, "repository", a.Repository)

	if owner := strings.TrimSpace(a.Owner); owner != "" {
		o.Set("owner", owner)
		to := teamRef(owner)
		if strings.Contains(owner, "@") {
			to = personRef(owner)
		}
		x.b.Relate("owned_by", ref, to)
	}
	if v := strings.TrimSpace(a.Vendor); v != "" {
		vref := "vendor/" + v
		x.b.Object(vref, "vendor").Identify("name", v)
		x.b.Relate("supplied_by", ref, vref)
	}
	for _, dep := range a.DependsOn {
		x.relateApp("depends_on", ref, dep, apps)
	}
	for _, dep := range a.Calls {
		x.relateApp("calls", ref, dep, apps)
	}

	for _, c := range a.Components {
		co := x.b.Object(compRef(a.Name, c.Name), "component").In(ref).Identify("name", c.Name)
		set(co, "kind", c.Kind)
		set(co, "technology", c.Technology)
	}
	for _, in := range a.Instances {
		x.instance(a.Name, in)
	}
}

func compRef(app, comp string) string { return "component/" + app + "/" + comp }

// relateApp relates an application to another, of this catalog (by ref) or
// of the inventory (by name).
func (x *builder) relateApp(typ, from, to string, apps map[string]bool) {
	k := typ + "|" + from + "|" + to
	if x.linked[k] {
		return
	}
	x.linked[k] = true
	if apps[to] {
		x.b.Relate(typ, from, appRef(to))
		return
	}
	x.b.RelateEndpoints(typ, sdk.Endpoint{Ref: from}, sdk.Endpoint{Type: "application", Identity: map[string]string{"name": to}})
}

func (x *builder) instance(app string, in Instance) {
	key := instanceKey(app, in.Component, in.Environment, in.location())
	ref := "instance/" + key
	o := x.b.Object(ref, "component_instance").In(compRef(app, in.Component)).Identify("key", key)
	o.Set("environment", strings.TrimSpace(in.Environment))
	set(o, "version", in.Version)
	env := x.env(in.Environment)
	x.b.Relate("deployed_in", ref, env.Ref)

	for _, h := range in.RunsOn {
		id := map[string]string{}
		if h.Name != "" {
			id["name"] = strings.TrimSpace(h.Name)
		}
		if h.Serial != "" {
			id["serial"] = strings.TrimSpace(h.Serial)
		}
		if h.UUID != "" {
			id["uuid"] = strings.TrimSpace(h.UUID)
		}
		x.b.RelateEndpoints("runs_on", sdk.Endpoint{Ref: ref}, sdk.Endpoint{Type: h.Type, Identity: id})
	}
	for _, e := range in.Endpoints {
		proto, _ := endpointProtocol(e)
		eref := "endpoint/" + e
		x.b.Object(eref, "endpoint").Identify("url", e).Set("protocol", proto)
		if k := "exposed_by|" + ref + "|" + eref; !x.linked[k] {
			x.linked[k] = true
			x.b.Relate("exposed_by", ref, eref)
		}
	}
}
