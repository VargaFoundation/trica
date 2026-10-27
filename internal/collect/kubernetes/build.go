package kubernetes

import (
	"sort"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

const (
	roleLabelPrefix = "node-role.kubernetes.io/"
	clusterRef      = "cluster"
)

// distribution guesses the Kubernetes distribution from the server's version
// string, where distributions leave their mark ("v1.31.4+k3s1").
func distribution(gitVersion string) string {
	v := strings.ToLower(gitVersion)
	for _, d := range []struct{ marker, name string }{
		{"+k3s", "k3s"},
		{"+rke2", "rke2"},
		{"+k0s", "k0s"},
		{"-eks-", "eks"},
		{"-gke.", "gke"},
	} {
		if strings.Contains(v, d.marker) {
			return d.name
		}
	}
	return "kubernetes"
}

// builder turns a snapshot into a batch. It keeps, for the relations, which
// ref stands for which Kubernetes object.
type builder struct {
	b    *sdk.Batch
	snap *snapshot

	nodes      map[string]string // node name -> ref
	namespaces map[string]string // namespace name -> ref
	services   map[string]string // namespace/name -> ref
	// workloads maps a controller's UID to its ref; replicaSetOwner maps a
	// ReplicaSet's UID to its Deployment's UID, so that a pod is attached to
	// the Deployment and not to the intermediate ReplicaSet.
	workloads       map[types.UID]string
	replicaSetOwner map[types.UID]types.UID
	podWorkload     map[string]string // pod ref -> workload ref

	// The application layer (ADR 0069).
	cfg *config
	// nsEnv maps a namespace to the environment its label puts it in.
	nsEnv map[string]string
	// argo maps the name of an Argo CD Application to where it lands.
	argo map[string]argoEntry
	// workloadInstance maps a workload ref to the instance running on it;
	// svcInstances maps a service ref to the instances behind it.
	workloadInstance map[string]string
	svcInstances     map[string][]string
	// instances records the instances already linked to their environment.
	instances map[string]bool
	// links records the endpoint links already made.
	links map[string]bool
}

// buildBatch maps snap into b. Everything is sorted, so that two runs on an
// unchanged cluster emit identical batches.
func buildBatch(b *sdk.Batch, cfg *config, snap *snapshot) {
	cluster, endpoint := cfg.cluster, cfg.endpoint
	x := &builder{
		b: b, snap: snap, cfg: cfg,
		nsEnv: map[string]string{}, argo: map[string]argoEntry{}, workloadInstance: map[string]string{},
		svcInstances: map[string][]string{}, instances: map[string]bool{}, links: map[string]bool{},
		nodes: map[string]string{}, namespaces: map[string]string{}, services: map[string]string{},
		workloads: map[types.UID]string{}, replicaSetOwner: map[types.UID]types.UID{}, podWorkload: map[string]string{},
	}
	b.Object(clusterRef, "k8s_cluster").
		Identify("name", cluster).
		Set("version", snap.version).
		Set("distribution", distribution(snap.version)).
		Set("api_endpoint", endpoint)

	x.nodesAndNamespaces()
	x.environments()
	x.argoApplications()
	x.workloadObjects()
	x.pods()
	x.servicesAndIngresses()
	x.virtualMachines()
	x.argoInstances()
}

func (x *builder) nodesAndNamespaces() {
	nodes := append([]corev1.Node(nil), x.snap.nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	for _, n := range nodes {
		ref := "node/" + n.Name
		x.nodes[n.Name] = ref
		o := x.b.Object(ref, "k8s_node").In(clusterRef).
			Identify("name", n.Name).
			Identify("uid", string(n.UID)).
			Set("system_uuid", strings.TrimSpace(n.Status.NodeInfo.SystemUUID)).
			Set("kubelet_version", n.Status.NodeInfo.KubeletVersion).
			Set("os_image", n.Status.NodeInfo.OSImage).
			Set("kernel", n.Status.NodeInfo.KernelVersion)
		if roles := nodeRoles(n.Labels); len(roles) > 0 {
			o.Set("roles", roles)
		}
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady {
				o.Set("ready", c.Status == corev1.ConditionTrue)
			}
		}
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				o.Set("internal_ip", a.Address)
				break
			}
		}
		if cpu, ok := n.Status.Allocatable[corev1.ResourceCPU]; ok {
			o.Set("cpu_capacity", cpu.AsApproximateFloat64())
		}
		if mem, ok := n.Status.Allocatable[corev1.ResourceMemory]; ok {
			o.Set("memory_capacity_bytes", mem.Value())
		}
		// The node runs on a device; the server finds the device by the
		// SMBIOS UUID the kubelet reports, and the engine records the link
		// as unresolved when no device has it.
		if uuid := strings.TrimSpace(n.Status.NodeInfo.SystemUUID); uuid != "" {
			x.b.RelateEndpoints("hosts",
				sdk.Endpoint{Type: "device", Identity: map[string]string{"hw_uuid": uuid}},
				sdk.Ref(ref))
		}
	}

	nss := append([]corev1.Namespace(nil), x.snap.namespaces...)
	sort.Slice(nss, func(i, j int) bool { return nss[i].Name < nss[j].Name })
	for _, ns := range nss {
		o := x.namespace(ns.Name)
		o.Identify("uid", string(ns.UID))
		if len(ns.Labels) > 0 {
			o.Set("labels", ns.Labels)
		}
	}
}

// namespace returns the ref of a namespace, creating a bare one when an
// object refers to a namespace the list did not return (a list that failed,
// a namespace created while the run was reading).
func (x *builder) namespace(name string) *sdk.Object {
	ref, ok := x.namespaces[name]
	if !ok {
		ref = "ns/" + name
		x.namespaces[name] = ref
	}
	return x.b.Object(ref, "namespace").In(clusterRef).Identify("name", name)
}

// nodeRoles reads the roles from the node-role.kubernetes.io/<role> labels
// (the value is irrelevant: kubeadm leaves it empty, K3s sets "true").
func nodeRoles(l map[string]string) []string {
	var roles []string
	for k := range l {
		if role, ok := strings.CutPrefix(k, roleLabelPrefix); ok && role != "" {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	return roles
}

// workloadInfo is what the five controller kinds have in common.
type workloadInfo struct {
	kind, namespace, name string
	uid                   types.UID
	replicas, ready       int
	counted               bool // replicas and ready mean something (not for jobs)
	images                []string
	// labels are the workload's own labels, completed by those of its pod
	// template (a chart may put them on one only).
	labels map[string]string
}

// mergeLabels returns own completed by fallback: own wins.
func mergeLabels(own, fallback map[string]string) map[string]string {
	out := make(map[string]string, len(own)+len(fallback))
	for k, v := range fallback {
		out[k] = v
	}
	for k, v := range own {
		out[k] = v
	}
	return out
}

func (x *builder) workloadObjects() {
	var all []workloadInfo
	for _, d := range x.snap.deployments {
		all = append(all, workloadInfo{kind: "deployment", namespace: d.Namespace, name: d.Name, uid: d.UID,
			replicas: replicasOrOne(d.Spec.Replicas), ready: int(d.Status.ReadyReplicas), counted: true, images: images(d.Spec.Template.Spec.Containers),
			labels: mergeLabels(d.Labels, d.Spec.Template.Labels)})
	}
	for _, s := range x.snap.statefulsets {
		all = append(all, workloadInfo{kind: "statefulset", namespace: s.Namespace, name: s.Name, uid: s.UID,
			replicas: replicasOrOne(s.Spec.Replicas), ready: int(s.Status.ReadyReplicas), counted: true, images: images(s.Spec.Template.Spec.Containers),
			labels: mergeLabels(s.Labels, s.Spec.Template.Labels)})
	}
	for _, d := range x.snap.daemonsets {
		all = append(all, workloadInfo{kind: "daemonset", namespace: d.Namespace, name: d.Name, uid: d.UID,
			replicas: int(d.Status.DesiredNumberScheduled), ready: int(d.Status.NumberReady), counted: true, images: images(d.Spec.Template.Spec.Containers),
			labels: mergeLabels(d.Labels, d.Spec.Template.Labels)})
	}
	for _, c := range x.snap.cronjobs {
		all = append(all, workloadInfo{kind: "cronjob", namespace: c.Namespace, name: c.Name, uid: c.UID,
			images: images(c.Spec.JobTemplate.Spec.Template.Spec.Containers),
			labels: mergeLabels(c.Labels, c.Spec.JobTemplate.Spec.Template.Labels)})
	}
	jobs := map[types.UID]*batchv1.Job{}
	for i := range x.snap.jobs {
		j := &x.snap.jobs[i]
		jobs[j.UID] = j
		all = append(all, workloadInfo{kind: "job", namespace: j.Namespace, name: j.Name, uid: j.UID,
			images: images(j.Spec.Template.Spec.Containers)})
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.namespace != b.namespace {
			return a.namespace < b.namespace
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.name < b.name
	})
	for _, w := range all {
		ref := "workload/" + w.namespace + "/" + w.kind + "/" + w.name
		x.workloads[w.uid] = ref
		o := x.b.Object(ref, "workload").In(x.namespace(w.namespace).Ref).
			Identify("name", w.name).
			Identify("uid", string(w.uid)).
			Set("kind", w.kind)
		if w.counted {
			o.Set("replicas", w.replicas).Set("ready_replicas", w.ready)
		}
		setList(o, "images", w.images)
		x.instanceOfWorkload(w, ref)
	}
	// A ReplicaSet is not reported (the Deployment is what an operator
	// knows), but it is how a pod finds its Deployment.
	for _, rs := range x.snap.replicasets {
		if owner := controller(rs.OwnerReferences); owner != nil {
			x.replicaSetOwner[rs.UID] = owner.UID
		}
	}
	// A Job started by a CronJob is served by it.
	for _, w := range all {
		if w.kind != "job" {
			continue
		}
		if owner := controller(jobs[w.uid].OwnerReferences); owner != nil {
			if cron, ok := x.workloads[owner.UID]; ok {
				x.b.Relate("served_by", cron, x.workloads[w.uid])
			}
		}
	}
}

func (x *builder) pods() {
	pods := x.snap.pods // sorted in place: the services read them in order too
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})
	for _, p := range pods {
		ref := "pod/" + p.Namespace + "/" + p.Name
		restarts := int32(0)
		for _, cs := range p.Status.ContainerStatuses {
			restarts += cs.RestartCount
		}
		x.b.Object(ref, "pod").In(x.namespace(p.Namespace).Ref).
			Identify("name", p.Name).
			Identify("uid", string(p.UID)).
			Set("phase", string(p.Status.Phase)).
			Set("pod_ip", p.Status.PodIP).
			Set("node_name", p.Spec.NodeName).
			Set("restarts", int(restarts))
		setList(x.b.Object(ref, "pod"), "images", images(p.Spec.Containers))

		if node, ok := x.nodes[p.Spec.NodeName]; ok {
			x.b.Relate("hosts", node, ref)
		}
		if owner := controller(p.OwnerReferences); owner != nil {
			uid := owner.UID
			if top, ok := x.replicaSetOwner[uid]; ok {
				uid = top
			}
			if w, ok := x.workloads[uid]; ok {
				x.b.Relate("served_by", w, ref)
				x.podWorkload[ref] = w
			}
		}
	}
}

func (x *builder) servicesAndIngresses() {
	svcs := append([]corev1.Service(nil), x.snap.services...)
	sort.Slice(svcs, func(i, j int) bool {
		if svcs[i].Namespace != svcs[j].Namespace {
			return svcs[i].Namespace < svcs[j].Namespace
		}
		return svcs[i].Name < svcs[j].Name
	})
	for _, s := range svcs {
		ref := "service/" + s.Namespace + "/" + s.Name
		x.services[s.Namespace+"/"+s.Name] = ref
		o := x.b.Object(ref, "k8s_service").In(x.namespace(s.Namespace).Ref).
			Identify("name", s.Name).
			Identify("uid", string(s.UID)).
			Set("type", string(s.Spec.Type)).
			Set("cluster_ip", s.Spec.ClusterIP)
		if len(s.Spec.Ports) > 0 {
			o.Set("ports", servicePorts(s.Spec.Ports))
		}
		if len(s.Spec.Selector) > 0 {
			o.Set("selector", s.Spec.Selector)
			x.exposes(ref, s)
		}
	}

	ings := append([]networkingv1.Ingress(nil), x.snap.ingresses...)
	sort.Slice(ings, func(i, j int) bool {
		if ings[i].Namespace != ings[j].Namespace {
			return ings[i].Namespace < ings[j].Namespace
		}
		return ings[i].Name < ings[j].Name
	})
	for _, ing := range ings {
		ref := "ingress/" + ing.Namespace + "/" + ing.Name
		hosts, rules := ingressRules(ing)
		o := x.b.Object(ref, "ingress").In(x.namespace(ing.Namespace).Ref).
			Identify("name", ing.Name).
			Identify("uid", string(ing.UID))
		setList(o, "hosts", hosts)
		if len(rules) > 0 {
			o.Set("rules", rules)
		}
		seen := map[string]bool{}
		for _, r := range rules {
			svc, _ := r["service"].(string)
			if svcRef, ok := x.services[ing.Namespace+"/"+svc]; ok && !seen[svcRef] {
				seen[svcRef] = true
				x.b.Relate("exposed_by", svcRef, ref)
			}
		}
		x.endpoints(ing.Namespace, rules)
	}
}

// exposes relates a service to the pods its selector picks, and the workloads
// that own them. Pods that finished (Jobs) do not receive traffic.
func (x *builder) exposes(svcRef string, s corev1.Service) {
	sel := labels.SelectorFromSet(s.Spec.Selector)
	workloads := map[string]bool{}
	instances := map[string]bool{}
	for _, p := range x.snap.pods {
		if p.Namespace != s.Namespace || finished(p) {
			continue
		}
		if !sel.Matches(labels.Set(p.Labels)) {
			continue
		}
		podRef := "pod/" + p.Namespace + "/" + p.Name
		x.b.Relate("served_by", svcRef, podRef)
		if w, ok := x.podWorkload[podRef]; ok {
			workloads[w] = true
		}
	}
	for _, w := range sortedKeys(workloads) {
		x.b.Relate("exposed_by", w, svcRef)
		if inst, ok := x.workloadInstance[w]; ok {
			instances[inst] = true
		}
	}
	x.svcInstances[svcRef] = sortedKeys(instances)
}

func servicePorts(ports []corev1.ServicePort) []map[string]any {
	out := make([]map[string]any, 0, len(ports))
	for _, p := range ports {
		m := map[string]any{"port": p.Port, "protocol": string(p.Protocol)}
		if p.Name != "" {
			m["name"] = p.Name
		}
		if p.TargetPort.String() != "0" {
			m["target_port"] = p.TargetPort.String()
		}
		if p.NodePort != 0 {
			m["node_port"] = p.NodePort
		}
		out = append(out, m)
	}
	return out
}

// ingressRules flattens an ingress into its host names and one entry per
// (host, path) with the backend service and port.
func ingressRules(ing networkingv1.Ingress) (hosts []string, rules []map[string]any) {
	seen := map[string]bool{}
	add := func(host, path, pathType string, b networkingv1.IngressBackend) {
		if b.Service == nil {
			return
		}
		r := map[string]any{"service": b.Service.Name}
		if host != "" {
			r["host"] = host
		}
		if path != "" {
			r["path"] = path
		}
		if pathType != "" {
			r["path_type"] = pathType
		}
		if b.Service.Port.Number != 0 {
			r["port"] = b.Service.Port.Number
		} else if b.Service.Port.Name != "" {
			r["port"] = b.Service.Port.Name
		}
		rules = append(rules, r)
	}
	if ing.Spec.DefaultBackend != nil {
		add("", "", "", *ing.Spec.DefaultBackend)
	}
	for _, rule := range ing.Spec.Rules {
		if rule.Host != "" && !seen[rule.Host] {
			seen[rule.Host] = true
			hosts = append(hosts, rule.Host)
		}
		if rule.HTTP == nil {
			continue
		}
		for _, p := range rule.HTTP.Paths {
			pt := ""
			if p.PathType != nil {
				pt = string(*p.PathType)
			}
			add(rule.Host, p.Path, pt, p.Backend)
		}
	}
	sort.Strings(hosts)
	return hosts, rules
}

// controller returns the owner reference marked as the controller, or the
// first one when none is.
func controller(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	if len(refs) > 0 {
		return &refs[0]
	}
	return nil
}

func images(cs []corev1.Container) []string {
	var out []string
	for _, c := range cs {
		if c.Image != "" {
			out = append(out, c.Image)
		}
	}
	return out
}

// replicasOrOne reads spec.replicas, which the API server defaults to 1.
func replicasOrOne(r *int32) int {
	if r == nil {
		return 1
	}
	return int(*r)
}

// setList sets a list attribute unless it is empty.
func setList(o *sdk.Object, key string, v []string) {
	if len(v) > 0 {
		o.Set(key, v)
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
