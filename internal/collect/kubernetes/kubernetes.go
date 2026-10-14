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
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Kind is the collector kind to use in a target.
const Kind = "kubernetes"

// inClusterAddress, or an empty address, selects the in-cluster configuration
// (the pod's ServiceAccount token and the API server environment variables).
const inClusterAddress = "in-cluster"

func init() { collect.Register(Kind, New) }

// options are the collector-specific settings of a target.
type options struct {
	// Cluster names the cluster: the k8s_cluster object and the scope of
	// everything below it. Required.
	Cluster string `yaml:"cluster"`
	// Kubeconfig is the path of a kubeconfig file, required unless the
	// target's address is empty or "in-cluster".
	Kubeconfig string `yaml:"kubeconfig"`
	// Context selects a context of the kubeconfig; empty is its current one.
	Context string `yaml:"context"`
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
	snap, err := gather(ctx, cl)
	if err != nil {
		return collect.Failed(c.target, Kind, ver.GitVersion, started, err)
	}
	snap.version = ver.GitVersion

	b := sdk.NewBatch(c.target.Source(), sdk.ScopeFull)
	buildBatch(b, c.opts.Cluster, cl.endpoint, snap)
	problems := snap.problems
	collect.Succeeded(b, c.target, Kind, ver.GitVersion, started, problems.warnings())
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
