package kubernetes

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// applicationResource is the Argo CD Application custom resource, listed in
// the namespace Argo CD runs in when the target asks for it.
var applicationResource = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

const (
	// inClusterServer is how an Argo CD Application designates the cluster
	// Argo CD itself runs in.
	inClusterServer = "https://kubernetes.default.svc"
	inClusterName   = "in-cluster"

	// Labels and annotations that say which trica application and component
	// an Argo CD Application stands for, and which environment a namespace is.
	annotationApplication = "trica.io/application"
	annotationComponent   = "trica.io/component"
	labelEnvironment      = "trica.io/environment"

	labelPartOf    = "app.kubernetes.io/part-of"
	labelName      = "app.kubernetes.io/name"
	labelComponent = "app.kubernetes.io/component"
	labelVersion   = "app.kubernetes.io/version"
	// labelArgoInstance is how Argo CD marks what an Application created
	// (its default tracking method): the name of the Application.
	labelArgoInstance = "argocd.argoproj.io/instance"

	// platformApplication is the application the Applications of the platform
	// projects are filed under, so that the platform does not flood the list.
	platformApplication = "platform"
	// tenantProjectPrefix is the Argo CD project naming convention of the
	// tenants: the project tenant-<x> deploys the application <x>.
	tenantProjectPrefix = "tenant-"
	// defaultProject is Argo CD's catch-all project: it names nothing.
	defaultProject = "default"
)

// argoApp is what trica reads of an Argo CD Application.
type argoApp struct {
	name, project, namespace string
	// destServer and destName are spec.destination.server and .name.
	destServer, destName string
	repoURL, targetRev   string
	revision             string // the synced revision (commit or chart version)
	health, sync         string
	// annotated and componentHint come from trica.io/application and
	// trica.io/component, on the Application as an annotation or a label.
	annotated, componentHint string
	partOf                   string
}

// parseApplication reads an Application defensively: it is a custom resource,
// so any field may have the wrong type. A wrong type is an error, not a
// silent zero.
func parseApplication(u *unstructured.Unstructured) (*argoApp, error) {
	a := &argoApp{name: u.GetName()}
	if a.name == "" {
		return nil, fmt.Errorf("no name")
	}
	var err error
	str := func(path ...string) string {
		v, _, e := unstructured.NestedString(u.Object, path...)
		if e != nil && err == nil {
			err = fmt.Errorf("%s: %w", strings.Join(path, "."), e)
		}
		return v
	}
	a.project = str("spec", "project")
	a.namespace = str("spec", "destination", "namespace")
	a.destServer = strings.TrimSpace(str("spec", "destination", "server"))
	a.destName = strings.TrimSpace(str("spec", "destination", "name"))
	a.health = str("status", "health", "status")
	a.sync = str("status", "sync", "status")
	a.revision = str("status", "sync", "revision")

	// A multi-source Application lists spec.sources; the one that carries the
	// code (a path or a chart) stands for it, a source that only provides
	// value files (ref) does not.
	srcs, _, e := unstructured.NestedSlice(u.Object, "spec", "sources")
	if e != nil {
		return nil, fmt.Errorf("spec.sources: %w", e)
	}
	pick := -1
	var src map[string]any
	for i, s := range srcs {
		m, ok := s.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("spec.sources[%d]: not an object", i)
		}
		if src == nil {
			pick, src = i, m
		}
		if p, _ := m["path"].(string); p != "" {
			pick, src = i, m
			break
		}
		if c, _ := m["chart"].(string); c != "" {
			pick, src = i, m
			break
		}
	}
	if src == nil {
		if m, ok, e := unstructured.NestedMap(u.Object, "spec", "source"); e != nil {
			return nil, fmt.Errorf("spec.source: %w", e)
		} else if ok {
			src = m
		}
	} else if revs, _, e := unstructured.NestedStringSlice(u.Object, "status", "sync", "revisions"); e == nil && pick < len(revs) && revs[pick] != "" {
		a.revision = revs[pick]
	}
	a.repoURL, _ = src["repoURL"].(string)
	a.targetRev, _ = src["targetRevision"].(string)
	if err != nil {
		return nil, err
	}

	for _, kv := range []map[string]string{u.GetLabels(), u.GetAnnotations()} {
		if a.annotated == "" {
			a.annotated = strings.TrimSpace(kv[annotationApplication])
		}
		if a.componentHint == "" {
			a.componentHint = strings.TrimSpace(kv[annotationComponent])
		}
	}
	a.partOf = strings.TrimSpace(u.GetLabels()[labelPartOf])
	return a, nil
}

// argoPlacement is where an Application lands in trica.
type argoPlacement struct {
	app, component, environment, namespace string
	platform                               bool
}

// namer turns Argo CD Applications and workload labels into application,
// component and environment names. It holds what the options say.
type namer struct {
	platformProjects map[string]bool
}

// application names the trica application an Argo CD Application belongs to,
// in order: the trica.io/application annotation or label; the platform
// projects (all one application); the app.kubernetes.io/part-of label; the
// tenant-<x> project; any other project but "default"; the Application's own
// name without its environment suffix.
func (n namer) application(a *argoApp, env string) (app string, platform bool) {
	switch {
	case slug(a.annotated) != "":
		return slug(a.annotated), n.platformProjects[a.project]
	case n.platformProjects[a.project]:
		return platformApplication, true
	case slug(a.partOf) != "":
		return slug(a.partOf), false
	}
	if x, ok := strings.CutPrefix(a.project, tenantProjectPrefix); ok {
		// tenant-<x>-ci is the CI of <x>, not another application.
		x = strings.TrimSuffix(x, "-ci")
		if s := slug(x); s != "" {
			return s, false
		}
	}
	// Any other named project groups its Applications: the project is the
	// application (an Argo CD project is how a team scopes what it deploys).
	if s := slug(a.project); s != "" && s != defaultProject {
		return s, false
	}
	return slug(stripEnvSuffix(a.name, env)), false
}

// place says where an Application lands: its application, its component (the
// Application's name without the application's prefix and the environment's
// suffix, or the whole name when nothing is left), the environment of its
// destination cluster and the namespace it deploys to.
func (n namer) place(a *argoApp, env string) argoPlacement {
	app, platform := n.application(a, env)
	comp := slug(a.componentHint)
	if comp == "" {
		comp = componentName(stripEnvSuffix(a.name, env), app)
	}
	return argoPlacement{app: app, component: comp, environment: env, namespace: a.namespace, platform: platform}
}

// componentName is name without the application's prefix; the application's
// own name when nothing is left.
func componentName(name, app string) string {
	name = slug(name)
	if rest := strings.TrimPrefix(name, app+"-"); rest != name && rest != "" {
		return rest
	}
	if name == "" {
		return app
	}
	return name
}

// stripEnvSuffix removes a trailing -<env> from an Application's name.
func stripEnvSuffix(name, env string) string {
	if env == "" {
		return name
	}
	if rest := strings.TrimSuffix(name, "-"+slug(env)); rest != "" {
		return rest
	}
	return name
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slug normalises a name for use in an identity: lower case, letters and
// digits, single dashes. Two spellings of one application must meet.
func slug(s string) string {
	return strings.Trim(notSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}

// destinationEnvironment resolves where an Application deploys to: this
// cluster (the in-cluster server, the API server of the target, or the
// cluster Argo CD is configured with), a cluster the options name, or an
// unknown one, which is named after its server and reported.
func (c *config) destinationEnvironment(a *argoApp, endpoint string) (env string, known bool) {
	server := strings.TrimRight(a.destServer, "/")
	switch {
	case a.destName == inClusterName,
		strings.EqualFold(server, inClusterServer),
		server != "" && endpoint != "" && strings.EqualFold(server, strings.TrimRight(endpoint, "/")):
		return c.environment, true
	case server == "" && a.destName == "":
		return c.environment, true
	}
	if e, ok := c.argocdClusters[server]; ok && server != "" {
		return e, true
	}
	if e, ok := c.argocdClusters[a.destName]; ok && a.destName != "" {
		return e, true
	}
	if a.destName != "" {
		return a.destName, false
	}
	if u, err := url.Parse(server); err == nil && u.Hostname() != "" {
		return u.Hostname(), false
	}
	return server, false
}

// cleanRepoURL drops what must not be kept of a repository address:
// credentials (https://user:token@host/...), a query string and a fragment.
// An scp-like address (git@host:org/repo.git) loses its user too.
func cleanRepoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		if at := strings.Index(raw, "@"); at >= 0 && !strings.Contains(raw[:at], "/") {
			raw = raw[at+1:]
		}
		if i := strings.IndexAny(raw, "?#"); i >= 0 {
			raw = raw[:i]
		}
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Do not guess at something that might hold a secret.
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// argoVersion is what an Application deploys: the target revision and the
// short commit it is synced to ("deploy@3572e14"), or just the revision when
// it is not a commit (a chart version).
func argoVersion(targetRev, revision string) string {
	revision = strings.TrimSpace(revision)
	switch {
	case revision == "":
		return strings.TrimSpace(targetRev)
	case fullSHA.MatchString(revision):
		short := revision[:7]
		if t := strings.TrimSpace(targetRev); t != "" && !fullSHA.MatchString(t) {
			return t + "@" + short
		}
		return short
	}
	return revision
}

// environmentKind guesses what an environment is for from its name; empty
// when the name says nothing.
func environmentKind(name string) string {
	n := strings.ToLower(name)
	for _, k := range []struct{ word, kind string }{
		{"prod", "prod"}, {"production", "prod"}, {"prd", "prod"},
		{"staging", "staging"}, {"stage", "staging"}, {"preprod", "staging"},
		{"dev", "dev"}, {"development", "dev"},
		{"test", "test"}, {"qa", "test"},
		{"build", "build"}, {"ci", "build"},
	} {
		if n == k.word || strings.HasSuffix(n, "-"+k.word) || strings.HasPrefix(n, k.word+"-") {
			return k.kind
		}
	}
	return ""
}

var environmentKinds = []string{"prod", "staging", "dev", "test", "build", "other"}

func validEnvironmentKind(k string) bool {
	for _, v := range environmentKinds {
		if v == k {
			return true
		}
	}
	return false
}
