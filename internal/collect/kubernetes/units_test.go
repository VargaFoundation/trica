package kubernetes

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/VargaFoundation/trica/internal/collect"
)

func TestDistribution(t *testing.T) {
	for in, want := range map[string]string{
		"v1.31.4+k3s1":            "k3s",
		"v1.30.6+rke2r1":          "rke2",
		"v1.31.2+k0s.0":           "k0s",
		"v1.30.4-eks-a737599":     "eks",
		"v1.30.5-gke.1443001":     "gke",
		"v1.29.9":                 "kubernetes",
		"v1.28.0-0.example.build": "kubernetes",
		"":                        "kubernetes",
	} {
		if got := distribution(in); got != want {
			t.Errorf("distribution(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNodeRoles(t *testing.T) {
	got := nodeRoles(map[string]string{
		"node-role.kubernetes.io/worker":        "true",
		"node-role.kubernetes.io/control-plane": "",
		"node-role.kubernetes.io/":              "ignored",
		"kubernetes.io/hostname":                "n1",
		"node.kubernetes.io/instance-type":      "k3s",
	})
	if want := []string{"control-plane", "worker"}; !reflect.DeepEqual(got, want) {
		t.Errorf("roles %v, want %v", got, want)
	}
	if nodeRoles(nil) != nil {
		t.Error("no labels, no roles")
	}
}

func TestPowerState(t *testing.T) {
	for _, tc := range []struct {
		phase  string
		paused bool
		want   string
	}{
		{"Running", false, "running"},
		{"Running", true, "paused"},
		{"Succeeded", false, "stopped"},
		{"Failed", false, "stopped"},
		{"Scheduling", false, "unknown"},
		{"", false, "unknown"},
	} {
		if got := powerState(tc.phase, tc.paused); got != tc.want {
			t.Errorf("powerState(%q, %v) = %q, want %q", tc.phase, tc.paused, got, tc.want)
		}
	}
}

func vmi(spec, status map[string]any) *unstructured.Unstructured {
	o := map[string]any{
		"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance",
		"metadata": map[string]any{"name": "vm1", "namespace": "vms", "uid": "uid-1"},
	}
	if spec != nil {
		o["spec"] = spec
	}
	if status != nil {
		o["status"] = status
	}
	return &unstructured.Unstructured{Object: o}
}

func TestParseVMI(t *testing.T) {
	cpu := func(m map[string]any) map[string]any { return map[string]any{"domain": map[string]any{"cpu": m}} }
	mem := func(path string, v any) map[string]any {
		if path == "guest" {
			return map[string]any{"domain": map[string]any{"memory": map[string]any{"guest": v}}}
		}
		return map[string]any{"domain": map[string]any{"resources": map[string]any{path: map[string]any{"memory": v}}}}
	}
	for _, tc := range []struct {
		name         string
		spec, status map[string]any
		check        func(*vmInstance) string
		wantErr      string
	}{
		{name: "bare instance", check: func(v *vmInstance) string {
			if v.uuid != "uid-1" || v.vcpus != 0 || v.memoryBytes != 0 || v.powerState != "unknown" || len(v.interfaces) != 0 {
				return "unexpected defaults"
			}
			return ""
		}},
		{name: "cores sockets threads", spec: cpu(map[string]any{"cores": int64(2), "sockets": float64(2), "threads": int64(2)}),
			check: func(v *vmInstance) string {
				if v.vcpus != 8 {
					return "vcpus != 8"
				}
				return ""
			}},
		{name: "cpu block without counts is one vcpu", spec: cpu(map[string]any{"model": "host-model"}),
			check: func(v *vmInstance) string {
				if v.vcpus != 1 {
					return "vcpus != 1"
				}
				return ""
			}},
		{name: "cpu of the wrong type", spec: cpu(map[string]any{"cores": "two"}), wantErr: "spec.domain.cpu.cores"},
		{name: "cpu not an object", spec: map[string]any{"domain": map[string]any{"cpu": "4"}}, wantErr: "cpu"},
		{name: "fractional cpu", spec: cpu(map[string]any{"cores": 1.5}), wantErr: "not an integer"},
		{name: "guest memory", spec: mem("guest", "512Mi"), check: func(v *vmInstance) string {
			if v.memoryBytes != 512<<20 {
				return "memory"
			}
			return ""
		}},
		{name: "request memory", spec: mem("requests", "2Gi"), check: func(v *vmInstance) string {
			if v.memoryBytes != 2<<30 {
				return "memory"
			}
			return ""
		}},
		{name: "limit memory", spec: mem("limits", "1G"), check: func(v *vmInstance) string {
			if v.memoryBytes != 1_000_000_000 {
				return "memory"
			}
			return ""
		}},
		{name: "memory unparsable", spec: mem("guest", "lots"), wantErr: "spec.domain.memory.guest"},
		{name: "memory of the wrong type", spec: mem("guest", int64(4)), wantErr: "memory"},
		{name: "firmware uuid wins", spec: map[string]any{"domain": map[string]any{"firmware": map[string]any{"uuid": " AAAA-1 "}}},
			check: func(v *vmInstance) string {
				if v.uuid != "AAAA-1" {
					return "uuid " + v.uuid
				}
				return ""
			}},
		{name: "paused", status: map[string]any{"phase": "Running", "conditions": []any{
			map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "Paused", "status": "True"}}},
			check: func(v *vmInstance) string {
				if v.powerState != "paused" {
					return v.powerState
				}
				return ""
			}},
		{name: "condition of the wrong type", status: map[string]any{"conditions": []any{"Ready"}}, wantErr: "status.conditions"},
		{name: "phase of the wrong type", status: map[string]any{"phase": int64(3)}, wantErr: "phase"},
		{name: "node of the wrong type", status: map[string]any{"nodeName": true}, wantErr: "nodeName"},
		{name: "guest info of the wrong type", status: map[string]any{"guestOSInfo": map[string]any{"prettyName": 1.5}}, wantErr: "prettyName"},
		{name: "interfaces not a list", status: map[string]any{"interfaces": "none"}, wantErr: "interfaces"},
		{name: "interface entry not an object", status: map[string]any{"interfaces": []any{"eth0"}}, wantErr: "entry is string"},
		{name: "firmware of the wrong type", spec: map[string]any{"domain": map[string]any{"firmware": map[string]any{"uuid": int64(7)}}}, wantErr: "uuid"},
		{name: "interfaces: addresses and macs", status: map[string]any{"interfaces": []any{
			map[string]any{"name": "default", "mac": "02-00-00-AA-00-01", "ipAddress": "192.0.2.5", "ipAddresses": []any{
				"192.0.2.5", "fe80::1", "127.0.0.1", "::1", "2001:db8::5", "not-an-ip", "0.0.0.0", "224.0.0.1", int64(7)}},
			map[string]any{"name": "", "interfaceName": "enp3s0", "mac": "garbage"},
			map[string]any{"name": "", "mac": "02:00:00:aa:00:09"},
		}}, check: func(v *vmInstance) string {
			if len(v.interfaces) != 2 {
				return "interfaces dropped or kept wrongly"
			}
			a, b := v.interfaces[0], v.interfaces[1]
			switch {
			case a.name != "default" || a.mac != "02:00:00:aa:00:01":
				return "first interface " + a.name + " " + a.mac
			case !reflect.DeepEqual(a.addresses, []string{"192.0.2.5", "2001:db8::5"}):
				return "addresses kept: " + strings.Join(a.addresses, ",")
			case b.name != "enp3s0" || b.mac != "":
				return "second interface " + b.name + " mac=" + b.mac
			}
			return ""
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := parseVMI(vmi(tc.spec, tc.status))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.check != nil {
				if msg := tc.check(v); msg != "" {
					t.Error(msg)
				}
			}
		})
	}

	t.Run("no name", func(t *testing.T) {
		u := vmi(nil, nil)
		u.SetName("")
		if _, err := parseVMI(u); err == nil {
			t.Error("an instance without a name was accepted")
		}
	})
	t.Run("no uid", func(t *testing.T) {
		u := vmi(nil, nil)
		u.SetUID("")
		if _, err := parseVMI(u); err == nil {
			t.Error("an instance without a uid was accepted")
		}
	})
}

func TestAsInt(t *testing.T) {
	for _, v := range []any{int64(3), int32(3), 3, float64(3)} {
		if n, err := asInt(v); err != nil || n != 3 {
			t.Errorf("asInt(%T) = %d, %v", v, n, err)
		}
	}
	for _, v := range []any{"3", 3.5, nil, true} {
		if _, err := asInt(v); err == nil {
			t.Errorf("asInt(%#v) accepted", v)
		}
	}
}

func TestIngressRules(t *testing.T) {
	exact := networkingv1.PathTypeExact
	ing := networkingv1.Ingress{Spec: networkingv1.IngressSpec{
		DefaultBackend: &networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
			Name: "fallback", Port: networkingv1.ServiceBackendPort{Number: 8080}}},
		Rules: []networkingv1.IngressRule{
			{Host: "b.example.test", IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{
				{Path: "/x", PathType: &exact, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "x", Port: networkingv1.ServiceBackendPort{Name: "web"}}}},
				// A resource backend (a storage bucket, a CRD) is not a service.
				{Path: "/res", Backend: networkingv1.IngressBackend{Resource: &corev1.TypedLocalObjectReference{Kind: "Bucket", Name: "b"}}},
				{Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "root"}}},
			}}}},
			{Host: "a.example.test"}, // no HTTP section
			{Host: "b.example.test"}, // the same host twice
			{IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{
				{Path: "/", Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "any-host", Port: networkingv1.ServiceBackendPort{Number: 80}}}},
			}}}},
		},
	}}
	hosts, rules := ingressRules(ing)
	if want := []string{"a.example.test", "b.example.test"}; !reflect.DeepEqual(hosts, want) {
		t.Errorf("hosts %v, want %v", hosts, want)
	}
	want := []map[string]any{
		{"service": "fallback", "port": int32(8080)},
		{"host": "b.example.test", "path": "/x", "path_type": "Exact", "service": "x", "port": "web"},
		{"host": "b.example.test", "service": "root"},
		{"path": "/", "service": "any-host", "port": int32(80)},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Errorf("rules %v, want %v", rules, want)
	}
}

func TestServicePorts(t *testing.T) {
	got := servicePorts([]corev1.ServicePort{
		{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(8080), NodePort: 30080},
		{Port: 53, Protocol: corev1.ProtocolUDP},
	})
	want := []map[string]any{
		{"name": "http", "port": int32(80), "protocol": "TCP", "target_port": "8080", "node_port": int32(30080)},
		{"port": int32(53), "protocol": "UDP"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ports %v, want %v", got, want)
	}
}

func TestController(t *testing.T) {
	yes, no := true, false
	refs := []metav1.OwnerReference{{Name: "plain", Controller: &no}, {Name: "ctrl", Controller: &yes}}
	if c := controller(refs); c == nil || c.Name != "ctrl" {
		t.Errorf("controller %v", c)
	}
	if c := controller(refs[:1]); c == nil || c.Name != "plain" {
		t.Errorf("without a controller flag, the first owner is the best guess: %v", c)
	}
	if controller(nil) != nil {
		t.Error("no owner")
	}
}

func TestListAllPages(t *testing.T) {
	type page struct {
		items []int
		next  string
	}
	pages := map[string]page{"": {[]int{1, 2}, "b"}, "b": {[]int{3}, "c"}, "c": {[]int{4, 5}, ""}}
	var seen []metav1.ListOptions
	list := func(_ context.Context, o metav1.ListOptions) (page, error) {
		seen = append(seen, o)
		return pages[o.Continue], nil
	}
	got, err := listAll(t.Context(), list, func(p page) ([]int, string) { return p.items, p.next })
	if err != nil || !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if len(seen) != 3 || seen[0].Limit != pageSize || seen[1].Continue != "b" || seen[2].Continue != "c" {
		t.Errorf("requests %+v: every page asks for the limit and carries the continue token", seen)
	}

	t.Run("a failing page discards the pages before it", func(t *testing.T) {
		boom := errors.New("expired continue token")
		n := 0
		got, err := listAll(t.Context(), func(_ context.Context, o metav1.ListOptions) (page, error) {
			if n++; n == 2 {
				return page{}, boom
			}
			return pages[o.Continue], nil
		}, func(p page) ([]int, string) { return p.items, p.next })
		if !errors.Is(err, boom) || got != nil {
			t.Errorf("got %v, %v: a half-read list must not pass for a complete one", got, err)
		}
	})

	t.Run("expired context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := listAll(ctx, list, func(p page) ([]int, string) { return p.items, p.next }); !errors.Is(err, context.Canceled) {
			t.Errorf("err %v", err)
		}
		// The context expires during the last call: the result is not trusted.
		ctx, cancel = context.WithCancel(t.Context())
		_, err := listAll(ctx, func(context.Context, metav1.ListOptions) (page, error) {
			cancel()
			return page{items: []int{1}}, nil
		}, func(p page) ([]int, string) { return p.items, p.next })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err %v", err)
		}
	})
}

func TestOptions(t *testing.T) {
	target := func(address string, opts map[string]any) collect.Target {
		tg := collect.Target{Kind: Kind, Name: "lab", Address: address}
		if opts != nil {
			if err := tg.Options.Encode(opts); err != nil {
				t.Fatal(err)
			}
		}
		return tg
	}
	for _, tc := range []struct {
		name    string
		target  collect.Target
		wantErr string
	}{
		{"in-cluster by default", target("", map[string]any{"cluster": "lab"}), ""},
		{"in-cluster, explicit", target("In-Cluster", map[string]any{"cluster": "lab"}), ""},
		{"kubeconfig", target("https://192.0.2.10:6443", map[string]any{"cluster": "lab", "kubeconfig": "/etc/trica/kubeconfig", "context": "lab"}), ""},
		{"finished pods", target("", map[string]any{"cluster": "lab", "finished_pods": true}), ""},
		{"finished pods, not a bool", target("", map[string]any{"cluster": "lab", "finished_pods": "sometimes"}), "into bool"},
		{"no options", target("", nil), "option cluster is required"},
		{"blank cluster", target("", map[string]any{"cluster": "  "}), "option cluster is required"},
		{"unknown option", target("", map[string]any{"cluster": "lab", "kubeconf": "x"}), "kubeconf"},
		{"address without kubeconfig", target("https://192.0.2.10:6443", map[string]any{"cluster": "lab"}), "needs the option kubeconfig"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := collect.New(tc.target)
			if tc.wantErr == "" {
				if err != nil || c == nil || c.Kind() != Kind {
					t.Fatalf("collector %v, error %v", c, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestNewWithClients(t *testing.T) {
	f := FakeKubernetes()
	kube, dyn := f.Clients()
	c, err := NewWithClients(f.Target("lab", "lab-cluster"), kube, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind() != Kind {
		t.Errorf("kind %q", c.Kind())
	}
	if _, err := NewWithClients(collect.Target{Kind: Kind, Name: "lab"}, kube, dyn); err == nil {
		t.Error("options are validated on injected clients too")
	}
}
