package kubernetes

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// virtualMachines maps the KubeVirt VirtualMachineInstances (the running
// machines; a stopped VirtualMachine has no instance and nothing to observe
// but its definition) to vm objects, with their interfaces and addresses. An
// instance that cannot be read is reported and skipped, the others stand.
func (x *builder) virtualMachines() {
	vmis := append([]unstructured.Unstructured(nil), x.snap.vmis...)
	sort.Slice(vmis, func(i, j int) bool {
		if vmis[i].GetNamespace() != vmis[j].GetNamespace() {
			return vmis[i].GetNamespace() < vmis[j].GetNamespace()
		}
		return vmis[i].GetName() < vmis[j].GetName()
	})
	for i := range vmis {
		vm, err := parseVMI(&vmis[i])
		if err != nil {
			x.snap.problems.add("virtualmachineinstances %s/%s: %v", vmis[i].GetNamespace(), vmis[i].GetName(), err)
			continue
		}
		x.vm(vm)
	}
}

// vmInstance is what trica reads of a VirtualMachineInstance.
type vmInstance struct {
	namespace, name, uid string
	uuid                 string // the firmware UUID the guest sees, else the instance UID
	node                 string
	vcpus                int
	memoryBytes          int64
	osName               string
	powerState           string
	interfaces           []vmInterface
}

type vmInterface struct {
	name, mac string
	addresses []string
}

// parseVMI reads an instance defensively: the object comes from a custom
// resource, so any field may have the wrong type. A wrong type is an error,
// not a silent zero.
func parseVMI(u *unstructured.Unstructured) (*vmInstance, error) {
	vm := &vmInstance{namespace: u.GetNamespace(), name: u.GetName(), uid: string(u.GetUID())}
	if vm.name == "" || vm.uid == "" {
		return nil, fmt.Errorf("no name or no uid")
	}
	obj := u.Object

	uuid, _, err := unstructured.NestedString(obj, "spec", "domain", "firmware", "uuid")
	if err != nil {
		return nil, err
	}
	vm.uuid = strings.TrimSpace(uuid)
	if vm.uuid == "" {
		vm.uuid = vm.uid
	}
	if vm.node, _, err = unstructured.NestedString(obj, "status", "nodeName"); err != nil {
		return nil, err
	}
	phase, _, err := unstructured.NestedString(obj, "status", "phase")
	if err != nil {
		return nil, err
	}
	if vm.osName, _, err = unstructured.NestedString(obj, "status", "guestOSInfo", "prettyName"); err != nil {
		return nil, err
	}

	if vm.vcpus, err = vmiVCPUs(obj); err != nil {
		return nil, err
	}
	if vm.memoryBytes, err = vmiMemory(obj); err != nil {
		return nil, err
	}
	paused, err := vmiPaused(obj)
	if err != nil {
		return nil, err
	}
	vm.powerState = powerState(phase, paused)
	if vm.interfaces, err = vmiInterfaces(obj); err != nil {
		return nil, err
	}
	return vm, nil
}

// vmiVCPUs is cores x sockets x threads, each defaulting to 1, and 0
// (unknown) when the instance declares no cpu block.
func vmiVCPUs(obj map[string]any) (int, error) {
	cpu, found, err := unstructured.NestedMap(obj, "spec", "domain", "cpu")
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	n := 1
	for _, k := range []string{"cores", "sockets", "threads"} {
		v, ok := cpu[k]
		if !ok {
			continue
		}
		c, err := asInt(v)
		if err != nil {
			return 0, fmt.Errorf("spec.domain.cpu.%s: %w", k, err)
		}
		if c > 0 {
			n *= int(c)
		}
	}
	return n, nil
}

// vmiMemory is the guest memory, else what the pod requests (then limits).
func vmiMemory(obj map[string]any) (int64, error) {
	for _, path := range [][]string{
		{"spec", "domain", "memory", "guest"},
		{"spec", "domain", "resources", "requests", "memory"},
		{"spec", "domain", "resources", "limits", "memory"},
	} {
		s, found, err := unstructured.NestedString(obj, path...)
		if err != nil {
			return 0, err
		}
		if !found || strings.TrimSpace(s) == "" {
			continue
		}
		q, err := resource.ParseQuantity(strings.TrimSpace(s))
		if err != nil {
			return 0, fmt.Errorf("%s: %w", strings.Join(path, "."), err)
		}
		return q.Value(), nil
	}
	return 0, nil
}

func vmiPaused(obj map[string]any) (bool, error) {
	conds, _, err := unstructured.NestedSlice(obj, "status", "conditions")
	if err != nil {
		return false, err
	}
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			return false, fmt.Errorf("status.conditions: entry is %T, not an object", c)
		}
		if m["type"] == "Paused" && m["status"] == "True" {
			return true, nil
		}
	}
	return false, nil
}

func vmiInterfaces(obj map[string]any) ([]vmInterface, error) {
	list, _, err := unstructured.NestedSlice(obj, "status", "interfaces")
	if err != nil {
		return nil, err
	}
	var out []vmInterface
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("status.interfaces: entry is %T, not an object", e)
		}
		name, _ := m["name"].(string)
		if name == "" {
			// An interface the guest agent reports without a name in the
			// spec: the guest's own name for it is the next best.
			name, _ = m["interfaceName"].(string)
		}
		if name == "" {
			continue
		}
		vi := vmInterface{name: name}
		if mac, _ := m["mac"].(string); mac != "" {
			if hw, err := net.ParseMAC(strings.TrimSpace(mac)); err == nil && len(hw) == 6 {
				vi.mac = hw.String()
			}
		}
		seen := map[string]bool{}
		add := func(a string) {
			a = strings.TrimSpace(a)
			if a != "" && !seen[a] && usableAddress(a) {
				seen[a] = true
				vi.addresses = append(vi.addresses, a)
			}
		}
		if s, ok := m["ipAddress"].(string); ok {
			add(s)
		}
		if l, ok := m["ipAddresses"].([]any); ok {
			for _, a := range l {
				if s, ok := a.(string); ok {
					add(s)
				}
			}
		}
		sort.Strings(vi.addresses)
		out = append(out, vi)
	}
	return out, nil
}

// powerState maps the instance phase to the schema's power states. An
// instance exists only while it runs, so the other phases are transitions.
func powerState(phase string, paused bool) string {
	switch phase {
	case "Running":
		if paused {
			return "paused"
		}
		return "running"
	case "Succeeded", "Failed":
		return "stopped"
	}
	return "unknown"
}

// usableAddress keeps what identifies a machine: loopback and link-local
// addresses are the same on every host and would merge unrelated interfaces.
func usableAddress(s string) bool {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	return !a.IsLoopback() && !a.IsLinkLocalUnicast() && !a.IsUnspecified() && !a.IsMulticast()
}

// asInt reads a number from decoded JSON or YAML.
func asInt(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int32:
		return int64(n), nil
	case int:
		return int64(n), nil
	case float64:
		if n != float64(int64(n)) {
			return 0, fmt.Errorf("%v is not an integer", n)
		}
		return int64(n), nil
	}
	return 0, fmt.Errorf("%T is not a number", v)
}

func (x *builder) vm(v *vmInstance) {
	ref := "vm/" + v.namespace + "/" + v.name
	o := x.b.Object(ref, "vm").
		Identify("uuid", v.uuid).
		Identify("name", v.name).
		Set("namespace", v.namespace).
		Set("power_state", v.powerState).
		Set("os_name", v.osName)
	if v.vcpus > 0 {
		o.Set("vcpus", v.vcpus)
	}
	if v.memoryBytes > 0 {
		o.Set("memory_bytes", v.memoryBytes)
	}
	if node, ok := x.nodes[v.node]; ok {
		x.b.Relate("hosts", node, ref)
	}
	for _, in := range v.interfaces {
		ifRef := ref + "/if/" + in.name
		x.b.Object(ifRef, "interface").In(ref).
			Identify("name", in.name).
			Set("kind", "virtual").
			Set("mac", in.mac)
		for _, addr := range in.addresses {
			ipRef := "ip/" + addr
			x.b.Object(ipRef, "ip_address").Identify("address", addr)
			x.b.Relate("assigned_to", ipRef, ifRef)
		}
	}
}
