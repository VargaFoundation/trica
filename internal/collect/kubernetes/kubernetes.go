// Package kubernetes collects a Kubernetes cluster: its nodes, namespaces,
// workloads, pods, services, ingresses and, when KubeVirt is installed, its
// virtual machine instances.
//
// The collector is read-only in the strictest sense: it only ever issues list
// calls (and one GET on /version), through a ServiceAccount that has no verb
// but get, list and watch, and that is never granted access to Secrets. A run
// lists everything once and reports one batch; keeping informers running to
// follow the cluster in real time is a follow-up (see the README).
package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	kubeclient "k8s.io/client-go/kubernetes"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Kind is the collector kind to use in a target.
const Kind = "kubernetes"

// inClusterAddress, or an empty address, selects the in-cluster configuration
// (the pod's ServiceAccount token and the API server environment variables).
const inClusterAddress = "in-cluster"

// Descriptor is what the module says about itself (ADR 0055).
var Descriptor = collect.Descriptor{
	Kind:       Kind,
	Title:      "Kubernetes",
	Vendors:    []string{"Kubernetes", "KubeVirt", "Argo CD"},
	Protocol:   "Kubernetes API (list calls), in-cluster or through a kubeconfig",
	Summary:    schema.Label{EN: "Kubernetes clusters: nodes, namespaces, workloads, pods, services, ingresses, KubeVirt virtual machines, and the applications and environments they run (from workload labels and Argo CD).", FR: "Clusters Kubernetes : nœuds, namespaces, charges de travail, pods, services, ingresses, machines virtuelles KubeVirt, et les applications et environnements qu'ils portent (d'après les labels et Argo CD)."},
	Deployment: collect.DeploymentTarget,
	Collects:   []string{"application", "component", "component_instance", "endpoint", "environment", "ingress", "interface", "ip_address", "k8s_cluster", "k8s_node", "k8s_service", "namespace", "pod", "vm", "workload"},
	ReadOnly:   "Only list calls and one GET /version; it never reads a Secret or a ConfigMap, and its ServiceAccount has only get, list and watch (on Argo CD Applications too, when argocd is on).",
	Address:    "in-cluster, empty, or the API server URL (needs options.kubeconfig)",
	Username:   collect.Credential{Use: collect.UseUnused},
	Secret:     collect.Credential{Use: collect.UseOptional, Meaning: "Bearer token; without it the kubeconfig or the in-cluster ServiceAccount is used"},
	Options:    options{},
	Tested:     []string{"K3s 1.31", "kubeadm 1.29", "KubeVirt 1.4"},
	Evidence:   collect.EvidenceSynthesised,
	Docs:       "internal/collect/kubernetes/README.md",
}

func init() { collect.Register(Descriptor, New) }

// options are the collector-specific settings of a target.
type options struct {
	// Cluster names the cluster: the k8s_cluster object and the scope of
	// everything below it. Required.
	Cluster string `yaml:"cluster" doc:"Required. Name of the cluster: the identity of the k8s_cluster object and the scope of everything below it."`
	// Kubeconfig is the path of a kubeconfig file, required unless the
	// target's address is empty or "in-cluster".
	Kubeconfig string `yaml:"kubeconfig" doc:"Path of a kubeconfig file; required unless the address is empty or in-cluster."`
	// Context selects a context of the kubeconfig; empty is its current one.
	Context string `yaml:"context" doc:"Context of the kubeconfig; empty is its current context."`
	// FinishedPods also collects the pods that have ended (phase Succeeded or
	// Failed). Off by default: on a cluster running CI or batch jobs they
	// outnumber the live pods by far, and listing them all can outlast the
	// run's timeout.
	FinishedPods bool `yaml:"finished_pods" default:"false" doc:"Also collect the pods that have ended (Succeeded or Failed); on a cluster running CI jobs they can outnumber the live pods by far."`
	// Environment names the environment this cluster is (ADR 0069).
	Environment string `yaml:"environment" doc:"Name of the environment this cluster is; empty is the cluster's name. A namespace labelled trica.io/environment=<x> belongs to the environment <x> instead."`
	// EnvironmentKind says what the environment is for.
	EnvironmentKind string `yaml:"environment_kind" enum:"prod,staging,dev,test,build,other" doc:"What this cluster's environment is for; empty guesses it from the environment's name when that says so."`
	// Argocd reads the Argo CD Applications of the cluster.
	Argocd bool `yaml:"argocd" default:"false" doc:"Also list the Argo CD Applications (applications.argoproj.io, read-only) of this cluster: each one is an instance of a component in the environment of its destination cluster. Needs the chart's collect.kubernetes.rbac.argocd."`
	// ArgocdNamespace is where Argo CD keeps its Applications.
	ArgocdNamespace string `yaml:"argocd_namespace" default:"argocd" doc:"Namespace Argo CD keeps its Applications in."`
	// ArgocdClusters maps a destination cluster to the environment it is.
	ArgocdClusters map[string]string `yaml:"argocd_clusters" doc:"Maps an Argo CD destination (its API server URL, or its registered name) to the environment it is, for instance {\"https://10.0.2.1:6443\": dev}. The cluster Argo CD runs in (kubernetes.default.svc) is this cluster's environment. A destination not listed is an environment named after its server, and a warning."`
	// ArgocdPlatformProjects lists the Argo CD projects of the platform.
	ArgocdPlatformProjects []string `yaml:"argocd_platform_projects" doc:"Argo CD projects whose Applications are the platform itself: all are filed under the one application \"platform\" instead of one application each."`
	// PlatformNamespaces lists the namespaces of the platform's own tools.
	PlatformNamespaces []string `yaml:"platform_namespaces" doc:"Namespaces holding the platform's own tools, installed outside Argo CD (kube-system, argocd...): a labelled workload there is a component of the application \"platform\", named after its app.kubernetes.io/part-of, instead of an application of its own."`
}

// config is what a run takes from the options, normalised.
type config struct {
	cluster, endpoint string
	environment       string
	environmentKind   string
	argocd            bool
	argocdNamespace   string
	// argocdClusters is keyed by destination server (no trailing slash) or name.
	argocdClusters map[string]string
	names          namer
	platformNS     map[string]bool // options.platform_namespaces
}

func (o options) config(endpoint string) *config {
	c := &config{
		cluster: o.Cluster, endpoint: endpoint,
		environment: o.Environment, environmentKind: o.EnvironmentKind,
		argocd: o.Argocd, argocdNamespace: o.ArgocdNamespace,
		argocdClusters: map[string]string{},
		names:          namer{platformProjects: map[string]bool{}},
		platformNS:     map[string]bool{},
	}
	for _, ns := range o.PlatformNamespaces {
		if ns = strings.TrimSpace(ns); ns != "" {
			c.platformNS[ns] = true
		}
	}
	for k, v := range o.ArgocdClusters {
		c.argocdClusters[strings.TrimRight(strings.TrimSpace(k), "/")] = strings.TrimSpace(v)
	}
	for _, p := range o.ArgocdPlatformProjects {
		if p = strings.TrimSpace(p); p != "" {
			c.names.platformProjects[p] = true
		}
	}
	return c
}

// clients is what a run needs from the cluster.
type clients struct {
	kube kubeclient.Interface
	dyn  dynamic.Interface
	// endpoint is the API server URL, empty when unknown (injected clients).
	endpoint string
}

type connectFunc func() (*clients, error)

// Collector reads one cluster.
type Collector struct {
	target  collect.Target
	opts    options
	connect connectFunc
}

// New is the collect.Factory of the kubernetes kind.
func New(t collect.Target) (sdk.Collector, error) {
	c, err := newCollector(t, nil)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// NewWithClients builds a collector that reads the given clients instead of
// connecting to a cluster: the simulated lab and the tests inject the fakes of
// client-go this way. The target's options are validated as usual.
func NewWithClients(t collect.Target, kube kubeclient.Interface, dyn dynamic.Interface) (sdk.Collector, error) {
	c, err := newCollector(t, func() (*clients, error) { return &clients{kube: kube, dyn: dyn}, nil })
	if err != nil {
		return nil, err
	}
	return c, nil
}

// newCollector validates the target's options. A nil connect builds the real
// clients on the first run; tests and the simulated lab inject their own.
func newCollector(t collect.Target, connect connectFunc) (*Collector, error) {
	var o options
	if err := t.DecodeOptions(&o); err != nil {
		return nil, err
	}
	o.Cluster = strings.TrimSpace(o.Cluster)
	if o.Cluster == "" {
		return nil, fmt.Errorf("target %s: option cluster is required", t.Source())
	}
	o.Environment = strings.TrimSpace(o.Environment)
	if o.Environment == "" {
		o.Environment = o.Cluster
	}
	o.EnvironmentKind = strings.TrimSpace(o.EnvironmentKind)
	if o.EnvironmentKind != "" && !validEnvironmentKind(o.EnvironmentKind) {
		return nil, fmt.Errorf("target %s: option environment_kind %q is not one of %s", t.Source(), o.EnvironmentKind, strings.Join(environmentKinds, ", "))
	}
	if o.ArgocdNamespace = strings.TrimSpace(o.ArgocdNamespace); o.ArgocdNamespace == "" {
		o.ArgocdNamespace = "argocd"
	}
	for k, v := range o.ArgocdClusters {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("target %s: option argocd_clusters needs a destination and an environment in every entry", t.Source())
		}
	}
	c := &Collector{target: t, opts: o, connect: connect}
	if connect == nil {
		if !c.inCluster() && strings.TrimSpace(o.Kubeconfig) == "" {
			return nil, fmt.Errorf("target %s: address %q needs the option kubeconfig (or leave the address empty to run in-cluster)", t.Source(), t.Address)
		}
		c.connect = c.connectReal
	}
	return c, nil
}

// Kind implements sdk.Collector.
func (c *Collector) Kind() string { return Kind }

// Collect implements sdk.Collector: one run, one batch. A run that cannot
// reach or authenticate to the cluster emits a failed-health batch and
// returns nil: the failure is data for the health board, not a crash.
func (c *Collector) Collect(ctx context.Context, emit sdk.Emitter) error {
	started := time.Now()
	runCtx, cancel := collect.Context(ctx, c.target)
	defer cancel()
	return emit.Emit(ctx, c.run(runCtx, started))
}

func (c *Collector) run(ctx context.Context, started time.Time) *sdk.Batch {
	cl, err := c.connect()
	if err != nil {
		return collect.Failed(c.target, Kind, "", started, fmt.Errorf("connect: %w", err))
	}
	// The version call doubles as the reachability and authentication probe.
	ver, err := discovery.ToServerVersionInterfaceWithContext(cl.kube.Discovery()).ServerVersionWithContext(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return collect.Failed(c.target, Kind, "", started, fmt.Errorf("server version: %w", err))
	}
	cfg := c.opts.config(cl.endpoint)
	snap, err := gather(ctx, cl, c.opts.FinishedPods, cfg)
	if err != nil {
		return collect.Failed(c.target, Kind, ver.GitVersion, started, err)
	}
	snap.version = ver.GitVersion

	b := sdk.NewBatch(c.target.Source(), sdk.ScopeFull)
	buildBatch(b, cfg, snap)
	problems := snap.problems
	collect.Succeeded(b, c.target, Kind, ver.GitVersion, started, append(problems.warnings(), snap.warnings...))
	if len(problems) > 0 {
		// Something could not be read: what was read is kept, nothing else
		// is withdrawn on the server (ADR 0005).
		b.Scope = sdk.ScopePartial
		b.Health.OK = false
		b.Health.Error = fmt.Sprintf("%d of the reads failed: %s", len(problems), strings.Join(problems.warnings(), "; "))
	}
	return b
}

// problems are the reads of a run that failed or returned something unusable.
type problems []string

func (p *problems) add(format string, args ...any) { *p = append(*p, fmt.Sprintf(format, args...)) }

func (p problems) warnings() []string { return []string(p) }

// notInstalled reports whether err says the API does not exist on this
// cluster, which is how a cluster without KubeVirt answers a list of its
// resources. That is not a failure.
func notInstalled(err error) bool { return apierrors.IsNotFound(err) }

// fatal reports whether err ends the run: expired context, credentials
// refused.
func fatal(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("run aborted: %w", ctx.Err())
	case apierrors.IsUnauthorized(err):
		return fmt.Errorf("credentials refused: %w", err)
	}
	return nil
}
