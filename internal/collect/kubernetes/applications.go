package kubernetes

import (
	"sort"
	"strings"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// This file turns what the cluster says about its applications into the
// application layer of the inventory (ADR 0069): an application, its
// components, one instance of each per environment and namespace, the
// environments, and the endpoints the instances are reached on.
//
// Two views feed it and meet on the same instance key, application /
// component / environment / namespace, so that one instance seen by both is
// one object: the labels of the workloads (app.kubernetes.io/part-of,
// component, name, version) and, when the argocd option is on, the Argo CD
// Applications.

// argoEntry is one Argo CD Application and where it lands.
type argoEntry struct {
	app *argoApp
	pl  argoPlacement
}

func (x *builder) appRef(app string) string { return "app/" + app }

// env returns the ref of an environment, creating it. kind is set when the
// environment has none yet and the name says what it is for.
func (x *builder) env(name, kind string) string {
	ref := "env/" + name
	o := x.b.Object(ref, "environment").Identify("name", name)
	setIfAbsent(o, "kind", kind)
	return ref
}

// setIfAbsent sets an attribute only when nothing set it before, so that the
// first view of an instance (the workloads: what runs) wins over the later
// one (Argo CD: what was declared).
func setIfAbsent(o *sdk.Object, key string, v any) {
	if _, ok := o.Attributes[key]; !ok {
		o.Set(key, v)
	}
}

// environments emits this cluster's environment, ties the cluster to it, and
// puts every namespace labelled trica.io/environment in the environment the
// label names (a cluster is one environment unless a namespace says
// otherwise).
func (x *builder) environments() {
	kind := x.cfg.environmentKind
	if kind == "" {
		kind = environmentKind(x.cfg.environment)
	}
	envRef := x.env(x.cfg.environment, kind)
	x.b.Object(envRef, "environment").Set("cluster", x.cfg.cluster)
	x.b.Relate("deployed_in", clusterRef, envRef)

	nss := append([]string(nil), keys(x.namespaces)...)
	sort.Strings(nss)
	byName := map[string]map[string]string{}
	for _, ns := range x.snap.namespaces {
		byName[ns.Name] = ns.Labels
	}
	for _, name := range nss {
		e := strings.TrimSpace(byName[name][labelEnvironment])
		if e == "" {
			continue
		}
		x.nsEnv[name] = e
		x.b.Relate("deployed_in", x.namespaces[name], x.env(e, environmentKind(e)))
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// instance returns the instance of component comp of application app in
// environment env and namespace ns, creating it with its application, its
// component and its environment link.
func (x *builder) instance(app, comp, env, ns string) *sdk.Object {
	appRef := x.appRef(app)
	x.b.Object(appRef, "application").Identify("name", app)
	compRef := "component/" + app + "/" + comp
	x.b.Object(compRef, "component").In(appRef).Identify("name", comp)

	// An Application with no destination namespace has no location: "-".
	loc := slug(ns)
	if loc == "" {
		loc = "-"
	}
	key := app + "/" + comp + "/" + slug(env) + "/" + loc
	ref := "instance/" + key
	o := x.b.Object(ref, "component_instance").In(compRef).Identify("key", key).Set("environment", env)
	if !x.instances[ref] {
		x.instances[ref] = true
		x.b.Relate("deployed_in", ref, x.env(env, environmentKind(env)))
	}
	return o
}

// placeWorkload says which application and component a workload belongs to,
// in order:
//  1. the workload is owned by an Argo CD Application of a platform project:
//     application platform, component the Application's;
//  2. it carries app.kubernetes.io/part-of: that application, and the
//     component label (or the name label, or its own name) as component;
//  3. it is owned by a known Argo CD Application: that one's application,
//     and the component label if there is one, else the Application's;
//  4. it carries the Argo CD label and a component label but Argo CD is not
//     read here: the Application is named <application>-<component>-<env> by
//     convention, so the application is what is left of it;
//  5. otherwise no application: a workload nobody labelled is no noise.
func (x *builder) placeWorkload(w workloadInfo) (app, comp string, ok bool) {
	if w.kind == "job" {
		return "", "", false
	}
	l := w.labels
	owner, owned := x.argo[l[labelArgoInstance]]
	compLabel := slug(l[labelComponent])
	switch {
	case owned && owner.pl.platform:
		return platformApplication, owner.pl.component, true
	case slug(l[labelPartOf]) != "":
		app = slug(l[labelPartOf])
		switch {
		case compLabel != "":
			comp = compLabel
		case slug(l[labelName]) != "":
			comp = componentName(l[labelName], app)
		default:
			comp = componentName(w.name, app)
		}
		return app, comp, true
	case owned:
		if comp = compLabel; comp == "" {
			comp = owner.pl.component
		}
		return owner.pl.app, comp, true
	}
	if argoName := l[labelArgoInstance]; argoName != "" && compLabel != "" {
		env := x.cfg.environment
		if e, ok := x.nsEnv[w.namespace]; ok {
			env = e
		}
		name := stripEnvSuffix(slug(argoName), env)
		if rest := strings.TrimSuffix(name, "-"+compLabel); rest != name && rest != "" {
			return rest, compLabel, true
		}
	}
	return "", "", false
}

// instanceOfWorkload makes the instance a workload is, when it belongs to an
// application: it runs on the workload.
func (x *builder) instanceOfWorkload(w workloadInfo, wref string) {
	app, comp, ok := x.placeWorkload(w)
	if !ok {
		return
	}
	env := x.cfg.environment
	if e, ok := x.nsEnv[w.namespace]; ok {
		env = e
	}
	o := x.instance(app, comp, env, w.namespace)
	setIfAbsent(o, "version", workloadVersion(w))
	x.b.Relate("runs_on", o.Ref, wref)
	x.workloadInstance[wref] = o.Ref
}

// workloadVersion is the version label of the workload, else the tag of its
// first image that has one.
func workloadVersion(w workloadInfo) string {
	if v := strings.TrimSpace(w.labels[labelVersion]); v != "" {
		return shortRevision(v)
	}
	for _, img := range w.images {
		if t := imageTag(img); t != "" {
			return t
		}
	}
	return ""
}

// shortRevision shortens a commit hash, which is not a version anyone reads
// in full.
func shortRevision(v string) string {
	if fullSHA.MatchString(v) {
		return v[:7]
	}
	return v
}

// imageTag is the tag of an image reference, empty when it has none (a digest
// alone, or the implicit latest). A registry port is not a tag.
func imageTag(img string) string {
	if i := strings.Index(img, "@"); i >= 0 {
		img = img[:i]
	}
	last := img[strings.LastIndex(img, "/")+1:]
	if i := strings.LastIndex(last, ":"); i >= 0 {
		return last[i+1:]
	}
	return ""
}

// endpoints makes an endpoint of every host an ingress serves through a
// service that sends traffic to the pods of an instance, and says that the
// instance is exposed by it.
func (x *builder) endpoints(ns string, rules []map[string]any) {
	for _, r := range rules {
		host, _ := r["host"].(string)
		svc, _ := r["service"].(string)
		if host == "" || svc == "" || strings.Contains(host, "*") {
			continue
		}
		insts := x.svcInstances[x.services[ns+"/"+svc]]
		if len(insts) == 0 {
			continue
		}
		url := "https://" + host
		ref := "endpoint/" + url
		x.b.Object(ref, "endpoint").Identify("url", url).Set("protocol", "https")
		for _, inst := range insts {
			if link := inst + "|" + ref; !x.links[link] {
				x.links[link] = true
				x.b.Relate("exposed_by", inst, ref)
			}
		}
	}
}

// argoApplications reads the Argo CD Applications of the snapshot and says
// where each lands. A malformed one is a problem of the run (partial scope),
// a destination nobody mapped is only a warning: the environment is named
// after its server, and the operator can map it.
func (x *builder) argoApplications() {
	if !x.cfg.argocd {
		return
	}
	warned := map[string]bool{}
	for i := range x.snap.apps {
		u := &x.snap.apps[i]
		a, err := parseApplication(u)
		if err != nil {
			x.snap.problems.add("applications.argoproj.io %s/%s: %v", u.GetNamespace(), u.GetName(), err)
			continue
		}
		env, known := x.cfg.destinationEnvironment(a, x.cfg.endpoint)
		if !known && !warned[env] {
			warned[env] = true
			x.snap.warnings = append(x.snap.warnings, "argocd: destination "+destinationLabel(a)+" is not in argocd_clusters: its Applications are filed under the environment "+env)
		}
		x.argo[a.name] = argoEntry{app: a, pl: x.cfg.names.place(a, env)}
	}
}

func destinationLabel(a *argoApp) string {
	if a.destServer != "" {
		return a.destServer
	}
	return a.destName
}

// argoInstances makes the instance of every Argo CD Application, in the
// environment of its destination cluster. One already made from the labels of
// its workloads is completed, not duplicated.
func (x *builder) argoInstances() {
	names := make([]string, 0, len(x.argo))
	for n := range x.argo {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a, pl := x.argo[n].app, x.argo[n].pl
		o := x.instance(pl.app, pl.component, pl.environment, pl.namespace)
		o.Set("health", a.health).Set("sync_status", a.sync)
		setIfAbsent(o, "version", argoVersion(a.targetRev, a.revision))
		if !pl.platform {
			// The credentials a repository address may carry are dropped.
			setIfAbsent(x.b.Object(x.appRef(pl.app), "application"), "repository", cleanRepoURL(a.repoURL))
		}
	}
}
