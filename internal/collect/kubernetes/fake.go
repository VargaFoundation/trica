package kubernetes

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	clienttesting "k8s.io/client-go/testing"

	"github.com/VargaFoundation/trica/internal/collect"
)

//go:embed testdata
var fixtures embed.FS

// The clusters the fake can serve. Each is a directory of testdata, described
// in testdata/README.md.
const (
	// K3s131 is a 3-node K3s v1.31 cluster with Cilium and two KubeVirt
	// instances.
	K3s131 = "k3s-1.31"
	// Kubeadm129 is a 2-node kubeadm v1.29 cluster, one node not ready, without
	// KubeVirt and without ingress.
	Kubeadm129 = "kubeadm-1.29"
)

// kubevirtGroup is the API group served by the dynamic fake; every other
// object of a fixture goes to the typed fake.
const kubevirtGroup = "kubevirt.io"

// Fake is a simulated Kubernetes API server: the typed and the dynamic fake
// clients of client-go, loaded with a fixture cluster, recording every
// request they receive. It simulates a refused token, one resource that
// cannot be listed, a slow server and a cluster without KubeVirt.
type Fake struct {
	kube *kubefake.Clientset
	dyn  *dynamicfake.FakeDynamicClient
}

// FakeKubernetes returns the default fake cluster (K3s131), with objects added
// on top of the fixture. Typed objects (corev1.Pod, ...) and unstructured
// KubeVirt instances are accepted.
func FakeKubernetes(objects ...runtime.Object) *Fake {
	return FakeKubernetesVersion(K3s131, objects...)
}

// FakeKubernetesVersion is FakeKubernetes for one of the fixture clusters. It
// panics on an unknown or unreadable fixture: that is a bug in the test.
func FakeKubernetesVersion(fixture string, objects ...runtime.Object) *Fake {
	typed, virt, ver, err := loadFixture(fixture)
	if err != nil {
		panic(fmt.Sprintf("kubernetes fake: fixture %s: %v", fixture, err))
	}
	for _, o := range objects {
		if u, ok := o.(*unstructured.Unstructured); ok && u.GroupVersionKind().Group == kubevirtGroup {
			virt = append(virt, u)
			continue
		}
		typed = append(typed, o)
	}
	f := &Fake{
		kube: kubefake.NewClientset(typed...),
		dyn: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
			map[schema.GroupVersionResource]string{vmiResource: "VirtualMachineInstanceList"}, virt...),
	}
	f.kube.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = ver
	return f
}

// loadFixture reads testdata/<fixture>/*.yaml (one or several documents per
// file) and version.json. Typed objects are decoded strictly: a field the API
// does not know fails the load, so a fixture cannot drift from the schema.
func loadFixture(fixture string) (typed []runtime.Object, virt []runtime.Object, ver *version.Info, err error) {
	dir := path.Join("testdata", fixture)
	entries, err := fs.ReadDir(fixtures, dir)
	if err != nil {
		return nil, nil, nil, err
	}
	ver = &version.Info{}
	for _, e := range entries {
		data, err := fs.ReadFile(fixtures, path.Join(dir, e.Name()))
		if err != nil {
			return nil, nil, nil, err
		}
		switch path.Ext(e.Name()) {
		case ".json":
			if e.Name() == "version.json" {
				if err := json.Unmarshal(data, ver); err != nil {
					return nil, nil, nil, fmt.Errorf("%s: %w", e.Name(), err)
				}
			}
		case ".yaml":
			docs, err := parseDocuments(data)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("%s: %w", e.Name(), err)
			}
			for _, u := range docs {
				if u.GroupVersionKind().Group == kubevirtGroup {
					virt = append(virt, u)
					continue
				}
				obj, err := scheme.Scheme.New(u.GroupVersionKind())
				if err != nil {
					return nil, nil, nil, fmt.Errorf("%s: %s %s: %w", e.Name(), u.GetKind(), u.GetName(), err)
				}
				if err := runtime.DefaultUnstructuredConverter.FromUnstructuredWithValidation(u.Object, obj, true); err != nil {
					return nil, nil, nil, fmt.Errorf("%s: %s %s: %w", e.Name(), u.GetKind(), u.GetName(), err)
				}
				typed = append(typed, obj)
			}
		}
	}
	if ver.GitVersion == "" {
		return nil, nil, nil, fmt.Errorf("no version.json")
	}
	return typed, virt, ver, nil
}

// parseDocuments splits a multi-document YAML stream into unstructured
// objects (through JSON, so that numbers are int64 as on the wire).
func parseDocuments(data []byte) ([]*unstructured.Unstructured, error) {
	var out []*unstructured.Unstructured
	r := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	for {
		doc, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, err
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		js, err := utilyaml.ToJSON(doc)
		if err != nil {
			return nil, err
		}
		if string(bytes.TrimSpace(js)) == "null" {
			continue
		}
		obj, _, err := unstructured.UnstructuredJSONScheme.Decode(js, nil, nil)
		if err != nil {
			return nil, err
		}
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return nil, fmt.Errorf("decoded a %T", obj)
		}
		if u.GetAPIVersion() == "" || u.GetKind() == "" {
			return nil, fmt.Errorf("a document has no apiVersion or no kind (%q)", u.GetName())
		}
		out = append(out, u)
	}
}

// Clients returns the fake clients, to inject into New-like constructors.
func (f *Fake) Clients() (kube *kubefake.Clientset, dyn *dynamicfake.FakeDynamicClient) {
	return f.kube, f.dyn
}

// Collector returns a collector for t that reads this fake.
func (f *Fake) Collector(t collect.Target) (*Collector, error) {
	return newCollector(t, func() (*clients, error) {
		return &clients{kube: f.kube, dyn: f.dyn, endpoint: "https://192.0.2.10:6443"}, nil
	})
}

// Target returns a target for a cluster served by this fake. Options can
// override or add to the defaults; the timeout is generous unless a test
// narrows it.
func (f *Fake) Target(name, cluster string) collect.Target {
	t := collect.Target{Kind: Kind, Name: name, Address: inClusterAddress, Timeout: 10 * time.Second}
	if err := t.Options.Encode(map[string]any{"cluster": cluster}); err != nil {
		panic(err)
	}
	return t
}

// react prepends a reactor to both fake clients.
func (f *Fake) react(verb, resource string, fn clienttesting.ReactionFunc) {
	f.kube.PrependReactor(verb, resource, fn)
	f.dyn.PrependReactor(verb, resource, fn)
}

// FailList makes listing resource (for example "pods") fail with err.
func (f *Fake) FailList(resource string, err error) *Fake {
	f.react("list", resource, func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, err })
	return f
}

// FailVersion makes the call on /version fail with err.
func (f *Fake) FailVersion(err error) *Fake {
	f.react("get", "version", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, err })
	return f
}

// Unauthorized makes the API server refuse the credentials on every request.
func (f *Fake) Unauthorized() *Fake {
	f.react("*", "*", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewUnauthorized("Unauthorized")
	})
	return f
}

// Forbid makes the ServiceAccount lack the right to list resource, as an RBAC
// role that forgot it would.
func (f *Fake) Forbid(resource string) *Fake {
	gr := schema.GroupResource{Resource: resource}
	f.react("list", resource, func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(gr, "",
			fmt.Errorf("user \"system:serviceaccount:trica:trica-collector\" cannot list resource %q at the cluster scope", resource))
	})
	return f
}

// Slow makes every request take d, to exercise the run's timeout.
func (f *Fake) Slow(d time.Duration) *Fake {
	f.react("*", "*", func(clienttesting.Action) (bool, runtime.Object, error) {
		time.Sleep(d)
		return false, nil, nil
	})
	return f
}

// WithoutKubeVirt makes the cluster answer "not found" for the KubeVirt API,
// as a cluster where it is not installed does.
func (f *Fake) WithoutKubeVirt() *Fake {
	f.dyn.PrependReactor("list", "virtualmachineinstances", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(vmiResource.GroupResource(), "")
	})
	return f
}

// AddMalformedVMI adds a KubeVirt instance whose status is of the wrong type,
// which a defensive reader must report and skip.
func (f *Fake) AddMalformedVMI() *Fake {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachineInstance",
		"metadata": map[string]any{
			"name": "broken-vm", "namespace": "vms", "uid": "00000000-0000-4000-8000-0000000000ff",
		},
		"spec":   map[string]any{"domain": map[string]any{"cpu": map[string]any{"cores": "two"}}},
		"status": map[string]any{"phase": "Running", "interfaces": "not-a-list"},
	}}
	if err := f.dyn.Tracker().Add(u); err != nil {
		panic(err)
	}
	return f
}

// Request is one request received by the fake.
type Request struct {
	Verb, Resource, Namespace, Subresource string
}

// Requests returns every request the fake received, typed and dynamic, in
// arrival order within each client.
func (f *Fake) Requests() []Request {
	var out []Request
	for _, set := range [][]clienttesting.Action{f.kube.Actions(), f.dyn.Actions()} {
		for _, a := range set {
			out = append(out, Request{Verb: a.GetVerb(), Resource: a.GetResource().Resource, Namespace: a.GetNamespace(), Subresource: a.GetSubresource()})
		}
	}
	return out
}

// ReadOnly checks the collector's contract on what the fake recorded: only
// get, list and watch, and never on Secrets. It returns what it found.
func (f *Fake) ReadOnly() error {
	var bad []string
	for _, r := range f.Requests() {
		switch {
		case r.Verb != "get" && r.Verb != "list" && r.Verb != "watch":
			bad = append(bad, fmt.Sprintf("%s %s", r.Verb, r.Resource))
		case r.Resource == "secrets":
			bad = append(bad, fmt.Sprintf("%s secrets", r.Verb))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("the collector went beyond read-only: %s", strings.Join(bad, ", "))
	}
	return nil
}
