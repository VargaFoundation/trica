// Package kubemetrics collects the placement of a Kubernetes cluster (nodes,
// pods, and the workload that controls each pod) from the kube-state-metrics
// series stored in a Prometheus-compatible time-series database, for clusters
// trica has no Kubernetes API access to (ADR 0070).
//
// The client only sends GET <address>/api/v1/query, instant queries, for six
// kube-state-metrics families (client.go). It reads no manifest, no
// environment value and no container argument: the series carry none. See
// README.md for the account, the mapping and what is left out.
package kubemetrics

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// Kind is the collector family and the Target.Kind value that selects it.
const Kind = "kubemetrics"

// Descriptor is what the module says about itself (ADR 0055).
var Descriptor = collect.Descriptor{
	Kind:       Kind,
	Title:      "Kubernetes (kube-state-metrics)",
	Vendors:    []string{"Kubernetes", "kube-state-metrics", "VictoriaMetrics", "Prometheus"},
	Protocol:   "Prometheus HTTP API: GET /api/v1/query (instant queries) over HTTPS with basic authentication",
	Summary:    schema.Label{EN: "A Kubernetes cluster seen through its kube-state-metrics series: the cluster, its nodes (with the server each one runs on, found by SMBIOS UUID), the namespaces, the pods with the node that hosts each one, and the workload (deployment, statefulset, daemonset, cronjob or job) that controls each pod. For clusters trica cannot query directly; it reads no manifest and no environment value.", FR: "Un cluster Kubernetes vu par ses séries kube-state-metrics : le cluster, ses nœuds (avec le serveur sur lequel chacun tourne, retrouvé par son UUID SMBIOS), les namespaces, les pods avec le nœud qui héberge chacun, et la charge (deployment, statefulset, daemonset, cronjob ou job) qui contrôle chaque pod. Pour les clusters que trica ne peut pas interroger directement ; il ne lit aucun manifeste ni aucune valeur d'environnement."},
	Deployment: collect.DeploymentTarget,
	Collects:   []string{"environment", "k8s_cluster", "k8s_node", "namespace", "pod", "workload"},
	ReadOnly:   "The client sends GET requests to <address>/api/v1/query only, with an instant query for one of six kube-state-metrics families (kube_node_info, kube_pod_info, kube_pod_owner, kube_replicaset_owner, kube_job_owner, kube_pod_status_phase); any other metric, path or method is refused before it is sent. There is no range, series or labels call. The account is sent in an Authorization header, never in a URL; redirects are never followed.",
	Address:    "Required: the https base URL of a Prometheus-compatible query API, with the path prefix that serves /api/v1/query (https://metrics.example.org/inventory).",
	Username:   collect.Credential{Use: collect.UseRequired, Meaning: "account allowed to run instant queries on the kube-state-metrics series, used only by trica"},
	Secret:     collect.Credential{Use: collect.UseRequired, Meaning: "password of that account"},
	Options:    Options{},
	Tested:     []string{"kube-state-metrics of a three-node k3s v1.31 cluster stored in VictoriaMetrics behind vmauth (recorded; the VictoriaMetrics version is not visible in the answers)"},
	Evidence:   collect.EvidenceRecorded,
	Docs:       "internal/collect/kubemetrics/README.md",
}

func init() { collect.Register(Descriptor, New) }

// Options are the collector-specific settings (target.options).
type Options struct {
	// Cluster is the name of the k8s_cluster.
	Cluster string `yaml:"cluster" doc:"Required: the name of the cluster, the k8s_cluster's name. It must be the name the Argo CD view of the kubernetes collector uses for the same cluster (its environment name), so that both describe one object."`
	// Environment is the environment the cluster is deployed in.
	Environment string `yaml:"environment" doc:"Name of the environment the cluster is deployed in. Default: the cluster's name."`
	// Selector narrows every query.
	Selector string `yaml:"selector" doc:"Label matchers added to every query, for a database that holds other series than kube-state-metrics' (job=\"monitoring-kube-state-metrics\"). Only label=\"value\" matchers (=, !=, =~, !~) are accepted."`
	// FinishedPods keeps the pods that ran to completion.
	FinishedPods bool `yaml:"finished_pods" default:"false" doc:"Keep the pods that finished (phase Succeeded or Failed). By default they are left out, as the kubernetes collector does."`
}

type collector struct {
	t        collect.Target
	opts     Options
	base     *url.URL
	cluster  string
	env      string
	selector string
}

// New builds a collector for t. It checks everything that can be checked
// without a network: address, options, TLS settings.
func New(t collect.Target) (sdk.Collector, error) {
	var opts Options
	if err := t.DecodeOptions(&opts); err != nil {
		return nil, err
	}
	base, err := parseAddress(t)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(t.Username) == "" {
		return nil, fmt.Errorf("target %s: username is required", t.Source())
	}
	cluster := strings.TrimSpace(opts.Cluster)
	if cluster == "" {
		return nil, fmt.Errorf("target %s: options.cluster is required (the name of the k8s_cluster)", t.Source())
	}
	env := strings.TrimSpace(opts.Environment)
	if env == "" {
		env = cluster
	}
	sel, _, err := parseMatchers(opts.Selector)
	if err != nil {
		return nil, fmt.Errorf("target %s: options.selector: %w", t.Source(), err)
	}
	if _, err := t.HTTPClient(); err != nil {
		return nil, err
	}
	return &collector{t: t, opts: opts, base: base, cluster: cluster, env: env, selector: sel}, nil
}

// parseAddress returns the base URL. Only https is accepted: the password
// travels in a header. A path is kept: it is the prefix a gateway serves the
// query API under.
func parseAddress(t collect.Target) (*url.URL, error) {
	addr := strings.TrimSpace(t.Address)
	if addr == "" {
		return nil, fmt.Errorf("target %s: address is required (https://metrics.example.org/inventory)", t.Source())
	}
	if !strings.Contains(addr, "://") {
		addr = "https://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("target %s: invalid address %q", t.Source(), t.Address)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("target %s: address must be https (the password is sent in a header), got %q", t.Source(), u.Scheme)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("target %s: address %q must hold no credentials, query or fragment", t.Source(), t.Address)
	}
	return &url.URL{Scheme: "https", Host: u.Host, Path: strings.TrimRight(u.Path, "/")}, nil
}

// Kind implements sdk.Collector.
func (c *collector) Kind() string { return Kind }

// Collect implements sdk.Collector: one run, one batch.
func (c *collector) Collect(ctx context.Context, emit sdk.Emitter) error {
	started := time.Now()
	rctx, cancel := collect.Context(ctx, c.t)
	defer cancel()

	fail := func(err error) error {
		return emit.Emit(ctx, collect.Failed(c.t, Kind, "", started, err))
	}
	passwd, err := c.t.Password.Resolve()
	if err != nil {
		return fail(fmt.Errorf("password: %w", err))
	}
	if passwd == "" {
		return fail(errors.New("password: the secret is empty"))
	}
	hc, err := c.t.HTTPClient()
	if err != nil {
		return fail(err)
	}
	cl := newClient(hc, c.base, strings.TrimSpace(c.t.Username), passwd, c.selector)
	snap, err := gather(rctx, cl)
	if err != nil {
		return fail(err)
	}

	b := sdk.NewBatch(c.t.Source(), sdk.ScopeFull)
	warnings := append([]string(nil), snap.warnings...)
	warnings = append(warnings, build(b, snap, c.cluster, c.env, c.opts.FinishedPods)...)
	if snap.partial || len(snap.errs) > 0 {
		b.Scope = sdk.ScopePartial
	}
	collect.Succeeded(b, c.t, Kind, "", started, warnings)
	if len(snap.errs) > 0 {
		b.Health.OK = false
		b.Health.Error = "partial collection: " + strings.Join(snap.errs, "; ")
	}
	return emit.Emit(ctx, b)
}

// snapshot holds what one run read. A nil family is one that could not be
// read (its error is in errs): the objects that depend on it are left without
// that part, and the run is partial so that nothing is withdrawn.
type snapshot struct {
	nodes, pods                 []sample
	owners, replicaSets, jobs   []sample
	phases                      []sample
	haveOwners, haveRS, haveJob bool
	havePhase                   bool

	warnings []string
	errs     []string
	partial  bool
}

// gather runs the six queries. kube_node_info and kube_pod_info are the core:
// an error or an empty answer there fails the run (an empty cluster would
// otherwise withdraw everything). The four others refine the picture.
func gather(ctx context.Context, cl *client) (*snapshot, error) {
	s := &snapshot{}
	core := func(metric string) ([]sample, error) {
		r, err := cl.query(ctx, metric)
		if err != nil {
			return nil, err
		}
		if len(r.Series) == 0 {
			return nil, fmt.Errorf("%s: no series (wrong selector, or not the kube-state-metrics of a cluster?)", metric)
		}
		s.notePartial(metric, r)
		return r.Series, nil
	}
	var err error
	if s.nodes, err = core(metricNodeInfo); err != nil {
		return nil, err
	}
	if s.pods, err = core(metricPodInfo); err != nil {
		return nil, err
	}
	optional := func(metric string) ([]sample, bool) {
		r, err := cl.query(ctx, metric)
		if err != nil {
			if ctx.Err() != nil {
				return nil, false
			}
			s.errs = append(s.errs, err.Error())
			return nil, false
		}
		s.notePartial(metric, r)
		return r.Series, true
	}
	s.owners, s.haveOwners = optional(metricPodOwner)
	s.replicaSets, s.haveRS = optional(metricRSOwner)
	s.jobs, s.haveJob = optional(metricJobOwner)
	s.phases, s.havePhase = optional(metricPodPhase)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *snapshot) notePartial(metric string, r *result) {
	if r.Partial {
		s.partial = true
		s.warnings = append(s.warnings, metric+": the server flagged the answer as partial (isPartial); nothing is withdrawn")
	}
}
