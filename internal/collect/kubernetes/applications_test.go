package kubernetes

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// The destinations of the recorded Applications, as the fixtures spell them.
const (
	devServer  = "https://203.0.113.1:6443"
	prodServer = "https://198.51.100.1:6443"
)

// argoOptions are the options of the collector of the cluster that runs Argo
// CD, as the operator would write them.
func argoOptions(extra map[string]any) map[string]any {
	o := map[string]any{
		"cluster": "build", "environment": "build", "argocd": true,
		"argocd_clusters":          map[string]string{devServer: "dev", prodServer: "prod"},
		"argocd_platform_projects": []string{"infra"},
	}
	for k, v := range extra {
		o[k] = v
	}
	return o
}

func withOptions(t *testing.T, o map[string]any) func(*collect.Target) {
	return func(tg *collect.Target) {
		if err := tg.Options.Encode(o); err != nil {
			t.Fatal(err)
		}
	}
}

// collectBuild runs the collector on the cluster that runs Argo CD.
func collectBuild(t *testing.T, extra map[string]any) (*Fake, *sdk.Batch) {
	t.Helper()
	f := FakeKubernetesVersion(Build131)
	return f, collectOne(t, f, withOptions(t, argoOptions(extra)))
}

func instance(t *testing.T, b *sdk.Batch, key string) *sdk.Object {
	t.Helper()
	return object(t, b, "instance/"+key)
}

// instances lists the keys of the instances of a batch.
func instanceKeys(b *sdk.Batch) []string {
	var out []string
	for _, o := range b.Objects {
		if o.Type == "component_instance" {
			out = append(out, o.Identity["key"])
		}
	}
	sort.Strings(out)
	return out
}

// TestArgoApplications: every Argo CD Application of the recorded cluster is
// one instance, of the component of the application it belongs to, in the
// environment of its destination cluster, with what Argo CD says of it.
func TestArgoApplications(t *testing.T) {
	f, b := collectBuild(t, nil)

	for _, tc := range []struct {
		key, env   string
		attrs      map[string]any
		app, comp  string
		deployedIn string
	}{
		// project tenant-<x>: the application is <x>, the component what is
		// left of the name without the application and the environment.
		{"orion/app/dev/orion", "dev", map[string]any{"health": "Healthy", "sync_status": "Synced", "environment": "dev"}, "orion", "app", "env/dev"},
		{"orion/app/prod/orion", "prod", nil, "orion", "app", "env/prod"},
		{"orion/keycloak/prod/orion", "prod", nil, "orion", "keycloak", "env/prod"},
		// tenant-<x>-ci is the CI of <x>, not another application; the
		// Application is in-cluster, so it is in this cluster's environment.
		{"orion/ci/build/tekton-orion", "build", nil, "orion", "ci", "env/build"},
		// a named project that is not tenant-<x>: the project is the application.
		{"hiring/hiring/prod/hiring", "prod", nil, "hiring", "hiring", "env/prod"},
		// the app.kubernetes.io/part-of label of the Application.
		{"hiring/browserfarm/dev/hiring", "dev", nil, "hiring", "browserfarm", "env/dev"},
		// a platform project: all one application.
		{"platform/ceph-csi/prod/ceph", "prod", nil, "platform", "ceph-csi", "env/prod"},
		{"platform/coredns/build/kube-system", "build", nil, "platform", "coredns", "env/build"},
		// the trica.io annotations beat every rule; the destination is given
		// by name; the application is normalised.
		{"nova-suite/worker/build/nova-staging", "build", map[string]any{"health": "Progressing", "sync_status": "OutOfSync"}, "nova-suite", "worker", "env/build"},
		// no destination namespace: no location.
		{"platform/gvisor/prod/-", "prod", nil, "platform", "gvisor", "env/prod"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			o := instance(t, b, tc.key)
			if tc.attrs != nil {
				wantAttrs(t, o, tc.attrs)
			}
			if o.Parent != "component/"+tc.app+"/"+tc.comp {
				t.Errorf("parent %q, want the component %s/%s", o.Parent, tc.app, tc.comp)
			}
			if c := object(t, b, o.Parent); c.Parent != "app/"+tc.app {
				t.Errorf("component %s is under %q", c.Ref, c.Parent)
			}
			if !relate(b, "deployed_in", o.Ref, tc.deployedIn) {
				t.Errorf("%s is not deployed in %s", o.Ref, tc.deployedIn)
			}
		})
	}

	t.Run("version is the synced commit and the target revision", func(t *testing.T) {
		v, _ := instance(t, b, "orion/app/dev/orion").Attributes["version"].(string)
		if !strings.HasPrefix(v, "deploy@") || len(v) != len("deploy@")+7 {
			t.Errorf("version %q, want deploy@<7 hex>", v)
		}
		// the same commit on dev and prod: the same version
		if p, _ := instance(t, b, "orion/app/prod/orion").Attributes["version"].(string); p != v {
			t.Errorf("prod runs %q, dev %q", p, v)
		}
		// a chart is a version, not a commit
		wantAttrs(t, instance(t, b, "nova/edge-qa/192-0-2-77/nova"), map[string]any{"version": "1.4.2"})
	})

	t.Run("a multi-source Application is read through its source with the code", func(t *testing.T) {
		o := instance(t, b, "nova/api/prod/nova")
		// revisions[1] is the sync of sources[1] (path deploy/prod, release),
		// not that of the one that only brings the value files.
		wantAttrs(t, o, map[string]any{"version": "release@3333333", "health": "Healthy"})
	})

	t.Run("the platform is one application", func(t *testing.T) {
		app := object(t, b, "app/platform")
		if _, ok := app.Attributes["repository"]; ok {
			t.Error("the platform has no repository of its own")
		}
		for _, k := range []string{"platform/harbor/build/harbor", "platform/vega-runtime/prod/vega-run"} {
			if instance(t, b, k).Parent == "" {
				t.Errorf("%s has no component", k)
			}
		}
		for _, name := range []string{"app/harbor", "app/ceph-csi", "app/coredns", "app/vega-runtime", "app/keycloak-platform"} {
			if _, ok := b.Lookup(name); ok {
				t.Errorf("%s: a platform Application got an application of its own", name)
			}
		}
	})

	t.Run("an application has a repository, without credentials", func(t *testing.T) {
		wantAttrs(t, object(t, b, "app/orion"), map[string]any{"repository": "https://github.com/example-org/orion.git"})
		raw, _ := json.Marshal(b)
		for _, leak := range []string{"EXAMPLE-NOT-A-SECRET", "git-user", "?ref=qa", "#readme"} {
			if strings.Contains(string(raw), leak) {
				t.Errorf("%q reached the batch", leak)
			}
		}
		if v, _ := object(t, b, "app/nova").Attributes["repository"].(string); v != "https://git.example.test/example-org/nova.git" {
			t.Errorf("repository %q", v)
		}
	})

	t.Run("environments", func(t *testing.T) {
		wantAttrs(t, object(t, b, "env/build"), map[string]any{"kind": "build", "cluster": "build"})
		wantAttrs(t, object(t, b, "env/dev"), map[string]any{"kind": "dev"})
		wantAttrs(t, object(t, b, "env/prod"), map[string]any{"kind": "prod"})
		if _, ok := object(t, b, "env/dev").Attributes["cluster"]; ok {
			t.Error("a cluster this collector does not read is not named as the cluster of its environment")
		}
		if !relate(b, "deployed_in", clusterRef, "env/build") {
			t.Error("the cluster is not deployed in its environment")
		}
	})

	t.Run("an unmapped destination is named after its server, and reported", func(t *testing.T) {
		wantAttrs(t, instance(t, b, "nova/edge-qa/192-0-2-77/nova"), map[string]any{"environment": "192.0.2.77"})
		if len(b.Health.Warnings) != 1 || !strings.Contains(b.Health.Warnings[0], "https://192.0.2.77:6443") ||
			!strings.Contains(b.Health.Warnings[0], "argocd_clusters") {
			t.Errorf("warnings %q", b.Health.Warnings)
		}
		// a warning is not a failure: the batch stays complete
		if b.Scope != sdk.ScopeFull || !b.Health.OK {
			t.Errorf("scope %s, ok %v", b.Scope, b.Health.OK)
		}
	})

	t.Run("it only lists the Applications of the Argo CD namespace", func(t *testing.T) {
		n := 0
		for _, r := range f.Requests() {
			if r.Resource == "applications" {
				n++
				if r.Verb != "list" || r.Namespace != "argocd" {
					t.Errorf("request %+v", r)
				}
			}
			if r.Resource == "secrets" {
				t.Errorf("Argo CD keeps the credentials of its clusters in Secrets: %+v", r)
			}
		}
		if n == 0 {
			t.Error("the Applications were not listed")
		}
	})
}

// TestArgoOffByDefault: Argo CD is read when the operator says so, and the
// chart only grants the right on that condition.
func TestArgoOffByDefault(t *testing.T) {
	f := FakeKubernetesVersion(Build131)
	b := collectOne(t, f, withOptions(t, map[string]any{"cluster": "build"}))
	for _, r := range f.Requests() {
		if r.Resource == "applications" {
			t.Fatalf("Applications listed without argocd: %+v", r)
		}
	}
	for _, o := range b.Objects {
		if _, ok := o.Attributes["sync_status"]; ok {
			t.Errorf("%s has an Argo CD status", o.Ref)
		}
	}
	// What the labels say is still there: the Argo CD workloads are part-of
	// argocd, and the environment is the cluster's name by default.
	if _, ok := b.Lookup("instance/argocd/server/build/argocd"); !ok {
		t.Error("the workloads were not read for their application")
	}
	if !relate(b, "deployed_in", clusterRef, "env/build") {
		t.Error("the cluster is not in its environment")
	}
}

// TestArgoUnavailable: a read that was asked for and failed is a partial run,
// whatever the reason, and what was read is kept.
func TestArgoUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		fake func(*Fake) *Fake
	}{
		{"forbidden (the chart's argocd RBAC is off)", func(f *Fake) *Fake { return f.Forbid("applications") }},
		{"not installed", func(f *Fake) *Fake {
			return f.FailList("applications", apierrors.NewNotFound(applicationResource.GroupResource(), ""))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.fake(FakeKubernetesVersion(Build131))
			b := collectOne(t, f, withOptions(t, argoOptions(nil)))
			if b.Scope != sdk.ScopePartial || b.Health.OK {
				t.Errorf("scope %s, ok %v: a read that failed must not withdraw what it did not see", b.Scope, b.Health.OK)
			}
			if !strings.Contains(b.Health.Error, "applications.argoproj.io") {
				t.Errorf("error %q does not name the read", b.Health.Error)
			}
			// without Argo CD the platform project is unknown: the labels decide
			if _, ok := b.Lookup("instance/harbor/core/build/harbor"); !ok {
				t.Error("the labels were not read after Argo CD failed")
			}
		})
	}
}

// TestArgoMalformed: a custom resource can say anything; one that is unusable
// is reported and skipped, the others are kept.
func TestArgoMalformed(t *testing.T) {
	bad := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": "broken", "namespace": "argocd", "uid": "00000000-0000-4000-8000-0000000001ff"},
		"spec":     map[string]any{"project": "infra", "destination": "not-an-object"},
	}}
	f := FakeKubernetesVersion(Build131, bad)
	b := collectOne(t, f, withOptions(t, argoOptions(nil)))
	if b.Scope != sdk.ScopePartial || !strings.Contains(b.Health.Error, "broken") {
		t.Errorf("scope %s, error %q", b.Scope, b.Health.Error)
	}
	if _, ok := b.Lookup("instance/orion/app/dev/orion"); !ok {
		t.Error("a malformed Application stopped the others")
	}
}

// TestSameInstanceOneObject: an instance seen by the labels of its workloads
// and by Argo CD is one object, completed by both (ADR 0069).
func TestSameInstanceOneObject(t *testing.T) {
	_, b := collectBuild(t, nil)

	// Harbor: Argo CD says it is healthy and synced, its workloads say what
	// runs and on what.
	const key = "platform/harbor/build/harbor"
	n := 0
	for _, o := range b.Objects {
		if o.Type == "component_instance" && o.Identity["key"] == key {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d objects for the instance %s, want 1", n, key)
	}
	o := instance(t, b, key)
	wantAttrs(t, o, map[string]any{"health": "Healthy", "sync_status": "Synced", "environment": "build",
		"version": "2.14.3"}) // what runs wins over what was declared
	var on []string
	for _, r := range b.Relations {
		if r.Type == "runs_on" && r.From.Ref == o.Ref {
			on = append(on, r.To.Ref)
		}
	}
	sort.Strings(on)
	want := []string{
		"workload/harbor/deployment/harbor-core", "workload/harbor/deployment/harbor-jobservice",
		"workload/harbor/deployment/harbor-portal", "workload/harbor/deployment/harbor-registry",
		"workload/harbor/statefulset/harbor-database", "workload/harbor/statefulset/harbor-redis",
		"workload/harbor/statefulset/harbor-trivy",
	}
	if !reflect.DeepEqual(on, want) {
		t.Errorf("harbor runs on\n%v\nwant\n%v", on, want)
	}
}

// TestSameInstanceAcrossClusters: the collector of the development cluster,
// which does not read Argo CD, and the collector of the cluster that does,
// give the instance the same key, so the server reconciles them to one object.
func TestSameInstanceAcrossClusters(t *testing.T) {
	_, fromBuild := collectBuild(t, nil)

	f := FakeKubernetesVersion(Dev131)
	fromDev := collectOne(t, f, withOptions(t, map[string]any{"cluster": "dev", "environment": "dev"}))

	seen := map[string]bool{}
	for _, k := range instanceKeys(fromBuild) {
		seen[k] = true
	}
	// Argo CD applications orion-app-dev ("app") and orion-keycloak-dev
	// (not labelled with a component) are what the dev workloads are.
	for _, k := range []string{"orion/app/dev/orion"} {
		if _, ok := fromDev.Lookup("instance/" + k); !ok {
			t.Fatalf("the development cluster does not report %s; it reports %v", k, instanceKeys(fromDev))
		}
		if !seen[k] {
			t.Errorf("Argo CD does not report %s; it reports %v", k, instanceKeys(fromBuild))
		}
	}
	// What runs there is on its workload.
	if !relate(fromDev, "runs_on", "instance/orion/app/dev/orion", "workload/orion/deployment/orion") {
		t.Error("the instance does not run on its workload")
	}
	// The same environment name: one environment object once reconciled.
	if object(t, fromDev, "env/dev").Identity["name"] != object(t, fromBuild, "env/dev").Identity["name"] {
		t.Error("the two collectors do not agree on the environment")
	}
	// Without a component label the Argo CD name cannot be split: no guess.
	for _, k := range instanceKeys(fromDev) {
		if strings.HasPrefix(k, "lyra/") || strings.HasPrefix(k, "lyra-") {
			t.Errorf("instance %s guessed from an Argo CD label alone", k)
		}
	}
}

// TestPlatformProjectsOption: without the option the platform is not
// grouped, Harbor is the application its labels say.
func TestPlatformProjectsOption(t *testing.T) {
	_, b := collectBuild(t, map[string]any{"argocd_platform_projects": []string{}})
	if _, ok := b.Lookup("app/platform"); ok {
		t.Error("an application platform without the option")
	}
	// The workloads say part-of harbor, and the Application is under the
	// tenant rules: its name without the environment.
	if _, ok := b.Lookup("instance/harbor/core/build/harbor"); !ok {
		t.Errorf("no harbor/core instance in %v", instanceKeys(b))
	}
}

// TestEndpoints: a host of an ingress that serves a service whose pods are an
// instance is an endpoint of it.
func TestEndpoints(t *testing.T) {
	_, b := collectBuild(t, nil)
	for _, tc := range []struct{ inst, host string }{
		{"instance/platform/harbor/build/harbor", "registry.example.test"},
		{"instance/argocd/server/build/argocd", "argo.build.example.test"},
	} {
		url := "https://" + tc.host
		e := object(t, b, "endpoint/"+url)
		if e.Identity["url"] != url {
			t.Errorf("identity %v", e.Identity)
		}
		wantAttrs(t, e, map[string]any{"protocol": "https"})
		if !relate(b, "exposed_by", tc.inst, e.Ref) {
			t.Errorf("%s is not exposed by %s", tc.inst, url)
		}
	}
	// A host whose service does not lead to an instance is not an endpoint:
	// the ingresses of an unlabelled cluster do not flood the inventory.
	f := FakeKubernetesVersion(Dev131)
	dev := collectOne(t, f, withOptions(t, map[string]any{"cluster": "dev"}))
	if n := count(dev, "endpoint"); n != 0 {
		t.Errorf("%d endpoints for workloads that belong to no application", n)
	}
}

// TestNamespaceEnvironment: a namespace labelled trica.io/environment is in
// that environment, and so are the instances in it.
func TestNamespaceEnvironment(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop-eu", UID: "00000000-0000-4000-8000-0000000002a1",
		Labels: map[string]string{labelEnvironment: "prod-eu"}}}
	one := int32(1)
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "shop-front", Namespace: "shop-eu", UID: "00000000-0000-4000-8000-0000000002a2",
		Labels: map[string]string{labelPartOf: "Shop", labelComponent: "front", labelVersion: "3.1.0"}},
		Spec: appsv1.DeploymentSpec{Replicas: &one}}
	f := FakeKubernetes(ns, dep)
	b := collectOne(t, f, withOptions(t, map[string]any{"cluster": "lab-cluster", "environment": "lab", "environment_kind": "test"}))

	if !relate(b, "deployed_in", "ns/shop-eu", "env/prod-eu") {
		t.Error("the namespace is not in the environment its label names")
	}
	wantAttrs(t, object(t, b, "env/prod-eu"), map[string]any{"kind": "prod"})
	o := instance(t, b, "shop/front/prod-eu/shop-eu")
	wantAttrs(t, o, map[string]any{"version": "3.1.0", "environment": "prod-eu"})
	if !relate(b, "deployed_in", o.Ref, "env/prod-eu") || !relate(b, "runs_on", o.Ref, "workload/shop-eu/deployment/shop-front") {
		t.Error("the instance is not in its namespace's environment, on its workload")
	}
	// The rest of the cluster is the cluster's environment, of the kind asked.
	wantAttrs(t, object(t, b, "env/lab"), map[string]any{"kind": "test", "cluster": "lab-cluster"})
	if !relate(b, "deployed_in", clusterRef, "env/lab") {
		t.Error("the cluster is not in its environment")
	}
	// The application name is normalised.
	if _, ok := b.Lookup("app/shop"); !ok {
		t.Error("part-of Shop is not the application shop")
	}
}

// TestEnvironmentOptions: the default environment is the cluster's name.
func TestEnvironmentOptions(t *testing.T) {
	b := collectOne(t, FakeKubernetes())
	wantAttrs(t, object(t, b, "env/lab-cluster"), map[string]any{"cluster": "lab-cluster"})
	if !relate(b, "deployed_in", clusterRef, "env/lab-cluster") {
		t.Error("the cluster is not in the environment named after it")
	}
	for _, tc := range []struct {
		name    string
		opts    map[string]any
		wantErr string
	}{
		{"kind", map[string]any{"cluster": "c", "environment_kind": "prod"}, ""},
		{"unknown kind", map[string]any{"cluster": "c", "environment_kind": "production"}, "environment_kind"},
		{"argocd clusters", map[string]any{"cluster": "c", "argocd_clusters": map[string]string{"https://x": "dev"}}, ""},
		{"argocd clusters, empty environment", map[string]any{"cluster": "c", "argocd_clusters": map[string]string{"https://x": " "}}, "argocd_clusters"},
		{"argocd clusters, not a map", map[string]any{"cluster": "c", "argocd_clusters": "dev"}, "unmarshal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg := collect.Target{Kind: Kind, Name: "lab"}
			if err := tg.Options.Encode(tc.opts); err != nil {
				t.Fatal(err)
			}
			_, err := collect.New(tg)
			if (err == nil) != (tc.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
				t.Errorf("error %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestPlaceWorkload(t *testing.T) {
	x := &builder{
		cfg:   &config{environment: "dev", platformNS: map[string]bool{"kube-system": true}},
		nsEnv: map[string]string{"eu": "prod-eu"},
		argo: map[string]argoEntry{
			"orion-app-dev":      {pl: argoPlacement{app: "orion", component: "app"}},
			"orion-keycloak-dev": {pl: argoPlacement{app: "orion", component: "keycloak"}},
			"harbor":             {pl: argoPlacement{app: platformApplication, component: "harbor", platform: true}},
		},
	}
	for _, tc := range []struct {
		name       string
		w          workloadInfo
		app, comp  string
		wantAnyApp bool
	}{
		{"part-of and component", workloadInfo{kind: "deployment", name: "x", labels: map[string]string{labelPartOf: "Argo CD", labelComponent: "server"}}, "argo-cd", "server", true},
		{"part-of in a platform namespace: a platform component", workloadInfo{kind: "daemonset", name: "cilium", namespace: "kube-system", labels: map[string]string{labelPartOf: "Cilium", labelName: "cilium-agent"}}, platformApplication, "cilium", true},
		{"part-of in another namespace: its own application", workloadInfo{kind: "daemonset", name: "cilium", namespace: "net", labels: map[string]string{labelPartOf: "Cilium"}}, "cilium", "cilium", true},
		{"part-of and name", workloadInfo{kind: "deployment", name: "w", labels: map[string]string{labelPartOf: "stack", labelName: "stack-api"}}, "stack", "api", true},
		{"part-of alone: the workload's name", workloadInfo{kind: "statefulset", name: "stack-db", labels: map[string]string{labelPartOf: "stack"}}, "stack", "db", true},
		{"part-of the name of the workload", workloadInfo{kind: "daemonset", name: "stack", labels: map[string]string{labelPartOf: "stack"}}, "stack", "stack", true},
		{"a platform Application beats part-of", workloadInfo{kind: "deployment", name: "harbor-core", labels: map[string]string{labelPartOf: "harbor", labelComponent: "core", labelArgoInstance: "harbor"}}, platformApplication, "harbor", true},
		{"a tenant Application: its application, the component label", workloadInfo{kind: "deployment", name: "orion-boondmock", labels: map[string]string{labelComponent: "boondmock", labelArgoInstance: "orion-app-dev"}}, "orion", "boondmock", true},
		{"a tenant Application: its component", workloadInfo{kind: "statefulset", name: "orion-keycloak", labels: map[string]string{labelArgoInstance: "orion-keycloak-dev"}}, "orion", "keycloak", true},
		{"Argo CD not read: the convention", workloadInfo{kind: "deployment", name: "n", namespace: "n", labels: map[string]string{labelComponent: "web", labelArgoInstance: "nova-web-dev"}}, "nova", "web", true},
		{"Argo CD not read, no component: no guess", workloadInfo{kind: "deployment", name: "n", labels: map[string]string{labelArgoInstance: "nova-web-dev"}}, "", "", false},
		{"nothing: no application", workloadInfo{kind: "deployment", name: "n", labels: map[string]string{labelName: "n"}}, "", "", false},
		{"a job is never an instance", workloadInfo{kind: "job", name: "n", labels: map[string]string{labelPartOf: "stack"}}, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, comp, ok := x.placeWorkload(tc.w)
			if ok != tc.wantAnyApp || app != tc.app || comp != tc.comp {
				t.Errorf("got %q/%q (%v), want %q/%q (%v)", app, comp, ok, tc.app, tc.comp, tc.wantAnyApp)
			}
		})
	}
}

func TestNamer(t *testing.T) {
	n := namer{platformProjects: map[string]bool{"infra": true}}
	for _, tc := range []struct {
		name string
		a    argoApp
		env  string
		want argoPlacement
	}{
		{"annotation first", argoApp{name: "x-y-dev", project: "tenant-z", partOf: "p", annotated: "My App"}, "dev", argoPlacement{app: "my-app", component: "x-y"}},
		{"component annotation", argoApp{name: "x-y-dev", annotated: "a", componentHint: "Web UI"}, "dev", argoPlacement{app: "a", component: "web-ui"}},
		{"platform project", argoApp{name: "ceph-csi-prod", project: "infra", partOf: "p"}, "prod", argoPlacement{app: "platform", component: "ceph-csi", platform: true}},
		{"part-of", argoApp{name: "bf-dev", project: "tenant-t", partOf: "Hiring"}, "dev", argoPlacement{app: "hiring", component: "bf"}},
		{"tenant project", argoApp{name: "orion-keycloak-prod", project: "tenant-orion"}, "prod", argoPlacement{app: "orion", component: "keycloak"}},
		{"tenant CI", argoApp{name: "orion-ci", project: "tenant-orion-ci"}, "build", argoPlacement{app: "orion", component: "ci"}},
		{"named project", argoApp{name: "hiring-prod", project: "hiring"}, "prod", argoPlacement{app: "hiring", component: "hiring"}},
		{"named project groups its Applications", argoApp{name: "attendee-prod", project: "hiring"}, "prod", argoPlacement{app: "hiring", component: "attendee"}},
		{"named project, prefix dropped", argoApp{name: "hiring-status-dev", project: "hiring"}, "dev", argoPlacement{app: "hiring", component: "status"}},
		{"default project: the name", argoApp{name: "echo-dev", project: "default"}, "dev", argoPlacement{app: "echo", component: "echo"}},
		{"name, other environment suffix kept", argoApp{name: "hiring-staging", project: ""}, "prod", argoPlacement{app: "hiring-staging", component: "hiring-staging"}},
		{"namespace is carried", argoApp{name: "a-b-dev", project: "tenant-a", namespace: "ns"}, "dev", argoPlacement{app: "a", component: "b", namespace: "ns"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := n.place(&tc.a, tc.env)
			tc.want.environment = tc.env
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDestinationEnvironment(t *testing.T) {
	c := (options{
		Cluster: "build", Environment: "build",
		ArgocdClusters: map[string]string{"https://10.0.0.2:6443/": " dev ", "prod-cluster": "prod"},
	}).config("https://192.0.2.10:6443")
	for _, tc := range []struct {
		name      string
		a         argoApp
		env       string
		wantKnown bool
	}{
		{"in-cluster server", argoApp{destServer: "https://kubernetes.default.svc"}, "build", true},
		{"in-cluster, any case, trailing slash", argoApp{destServer: "HTTPS://Kubernetes.default.svc/"}, "build", true},
		{"in-cluster by name", argoApp{destName: "in-cluster"}, "build", true},
		{"this cluster's own address", argoApp{destServer: "https://192.0.2.10:6443/"}, "build", true},
		{"no destination at all", argoApp{}, "build", true},
		{"mapped by server (the option's trailing slash is ignored)", argoApp{destServer: "https://10.0.0.2:6443"}, "dev", true},
		{"mapped by registered name", argoApp{destName: "prod-cluster"}, "prod", true},
		{"unmapped server: its host", argoApp{destServer: "https://10.0.0.9:6443"}, "10.0.0.9", false},
		{"unmapped name", argoApp{destName: "edge"}, "edge", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, known := c.destinationEnvironment(&tc.a, c.endpoint)
			if env != tc.env || known != tc.wantKnown {
				t.Errorf("got %q (%v), want %q (%v)", env, known, tc.env, tc.wantKnown)
			}
		})
	}
}

func TestCleanRepoURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/example-org/a.git":                     "https://github.com/example-org/a.git",
		"https://user:EXAMPLE-NOT-A-SECRET@git.example.test/a.git": "https://git.example.test/a.git",
		"https://EXAMPLE-NOT-A-SECRET@git.example.test/a.git":      "https://git.example.test/a.git",
		"https://git.example.test/a.git?token=EXAMPLE#frag":        "https://git.example.test/a.git",
		"ssh://git@git.example.test/a.git":                         "ssh://git.example.test/a.git",
		"git@git.example.test:example-org/a.git":                   "git.example.test:example-org/a.git",
		"git.example.test:example-org/a.git?x=EXAMPLE":             "git.example.test:example-org/a.git",
		"  ": "",
		"https://user:EXAMPLE-NOT-A-SECRET@host:notaport/a.git": "",
	} {
		got := cleanRepoURL(in)
		if got != want {
			t.Errorf("cleanRepoURL(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(got, "EXAMPLE") {
			t.Errorf("cleanRepoURL(%q) = %q keeps a credential", in, got)
		}
	}
}

func TestArgoVersion(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct{ target, rev, want string }{
		{"main", sha, "main@0123456"},
		{"HEAD", sha, "HEAD@0123456"},
		{"", sha, "0123456"},
		{sha, sha, "0123456"},
		{"1.4.2", "1.4.2", "1.4.2"},
		{"main", "", "main"},
		{"", "", ""},
	} {
		if got := argoVersion(tc.target, tc.rev); got != tc.want {
			t.Errorf("argoVersion(%q, %q) = %q, want %q", tc.target, tc.rev, got, tc.want)
		}
	}
}

func TestImageTag(t *testing.T) {
	for in, want := range map[string]string{
		"nginx:1.25":                           "1.25",
		"registry.example.test:5000/team/a:v2": "v2",
		"registry.example.test:5000/team/a":    "",
		"nginx":                                "",
		"a/b:1.0@sha256:abcdef":                "1.0",
		"a/b@sha256:abcdef":                    "",
	} {
		if got := imageTag(in); got != want {
			t.Errorf("imageTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnvironmentKind(t *testing.T) {
	for in, want := range map[string]string{
		"prod": "prod", "Production": "prod", "eu-prod": "prod", "staging": "staging", "dev": "dev", "dev-eu": "dev",
		"test": "test", "build": "build", "lab": "", "device": "", "prodigy": "",
	} {
		if got := environmentKind(in); got != want {
			t.Errorf("environmentKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Nova Suite": "nova-suite", "  a__b ": "a-b", "-": "", "": "", "192.0.2.77": "192-0-2-77"} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseApplicationErrors(t *testing.T) {
	for name, obj := range map[string]map[string]any{
		"no name":     {"metadata": map[string]any{}},
		"destination": {"metadata": map[string]any{"name": "a"}, "spec": map[string]any{"destination": "x"}},
		"sources":     {"metadata": map[string]any{"name": "a"}, "spec": map[string]any{"sources": "x"}},
		"a source":    {"metadata": map[string]any{"name": "a"}, "spec": map[string]any{"sources": []any{"x"}}},
		"source":      {"metadata": map[string]any{"name": "a"}, "spec": map[string]any{"source": "x"}},
		"status":      {"metadata": map[string]any{"name": "a"}, "status": map[string]any{"health": "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			if a, err := parseApplication(&unstructured.Unstructured{Object: obj}); err == nil {
				t.Errorf("parsed %+v", a)
			}
		})
	}
	a, err := parseApplication(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "a", "labels": map[string]any{labelPartOf: " P "}, "annotations": map[string]any{annotationApplication: " X "}},
	}})
	if err != nil || a.partOf != "P" || a.annotated != "X" {
		t.Errorf("%+v, %v", a, err)
	}
}
