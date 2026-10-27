package kubernetes

import (
	"context"
	"fmt"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// pageSize bounds one list call, so that a large cluster is read in chunks
// rather than in one response the API server has to hold in memory.
const pageSize = 500

// livePods is the field selector that leaves out the pods that have ended.
// The API server filters them before paging, so they cost neither transfer
// nor decoding.
const livePods = "status.phase!=" + string(corev1.PodSucceeded) + ",status.phase!=" + string(corev1.PodFailed)

// vmiResource is the KubeVirt resource listed when KubeVirt is installed.
var vmiResource = schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "virtualmachineinstances"}

// snapshot is everything one run read from the cluster.
type snapshot struct {
	version      string
	nodes        []corev1.Node
	namespaces   []corev1.Namespace
	pods         []corev1.Pod
	services     []corev1.Service
	deployments  []appsv1.Deployment
	statefulsets []appsv1.StatefulSet
	daemonsets   []appsv1.DaemonSet
	replicasets  []appsv1.ReplicaSet
	jobs         []batchv1.Job
	cronjobs     []batchv1.CronJob
	ingresses    []networkingv1.Ingress
	vmis         []unstructured.Unstructured
	// apps are the Argo CD Applications, read when the argocd option is on.
	apps []unstructured.Unstructured

	problems problems
	// warnings are what the run noticed and that does not make it partial:
	// something unusual, not something it could not read.
	warnings []string
}

// gather lists every resource the collector maps. A list that fails is
// recorded in the snapshot and the others still run; only an expired context,
// refused credentials, or every list failing ends the run with an error.
// Pods that have ended are left out unless finishedPods is set.
func gather(ctx context.Context, cl *clients, finishedPods bool, cfg *config) (*snapshot, error) {
	s := &snapshot{}
	attempted, failed := 0, 0
	var aborted error

	// step runs one list. optional marks a resource whose absence from the
	// cluster is normal (KubeVirt).
	step := func(resource string, optional bool, fn func() error) {
		if aborted != nil {
			return
		}
		err := fn()
		if aborted = fatal(ctx, err); aborted != nil {
			aborted = fmt.Errorf("listing %s: %w", resource, aborted)
			return
		}
		switch {
		case err == nil:
			attempted++
		case optional && notInstalled(err):
			// Nothing to read, nothing failed.
		default:
			attempted++
			failed++
			s.problems.add("%s: %v", resource, err)
		}
	}

	core, apps, batch, net := cl.kube.CoreV1(), cl.kube.AppsV1(), cl.kube.BatchV1(), cl.kube.NetworkingV1()
	step("nodes", false, func() (err error) {
		s.nodes, err = listAll(ctx, core.Nodes().List, func(l *corev1.NodeList) ([]corev1.Node, string) { return l.Items, l.Continue })
		return err
	})
	step("namespaces", false, func() (err error) {
		s.namespaces, err = listAll(ctx, core.Namespaces().List, func(l *corev1.NamespaceList) ([]corev1.Namespace, string) { return l.Items, l.Continue })
		return err
	})
	step("pods", false, func() (err error) {
		list := core.Pods(metav1.NamespaceAll).List
		if !finishedPods {
			list = func(ctx context.Context, o metav1.ListOptions) (*corev1.PodList, error) {
				o.FieldSelector = livePods
				return core.Pods(metav1.NamespaceAll).List(ctx, o)
			}
		}
		s.pods, err = listAll(ctx, list, func(l *corev1.PodList) ([]corev1.Pod, string) { return l.Items, l.Continue })
		if !finishedPods {
			// A server that ignores the selector (an old one, a proxy, the
			// fake of client-go) must not bring them back.
			s.pods = slices.DeleteFunc(s.pods, finished)
		}
		return err
	})
	step("services", false, func() (err error) {
		s.services, err = listAll(ctx, core.Services(metav1.NamespaceAll).List, func(l *corev1.ServiceList) ([]corev1.Service, string) { return l.Items, l.Continue })
		return err
	})
	step("deployments", false, func() (err error) {
		s.deployments, err = listAll(ctx, apps.Deployments(metav1.NamespaceAll).List, func(l *appsv1.DeploymentList) ([]appsv1.Deployment, string) { return l.Items, l.Continue })
		return err
	})
	step("statefulsets", false, func() (err error) {
		s.statefulsets, err = listAll(ctx, apps.StatefulSets(metav1.NamespaceAll).List, func(l *appsv1.StatefulSetList) ([]appsv1.StatefulSet, string) { return l.Items, l.Continue })
		return err
	})
	step("daemonsets", false, func() (err error) {
		s.daemonsets, err = listAll(ctx, apps.DaemonSets(metav1.NamespaceAll).List, func(l *appsv1.DaemonSetList) ([]appsv1.DaemonSet, string) { return l.Items, l.Continue })
		return err
	})
	step("replicasets", false, func() (err error) {
		s.replicasets, err = listAll(ctx, apps.ReplicaSets(metav1.NamespaceAll).List, func(l *appsv1.ReplicaSetList) ([]appsv1.ReplicaSet, string) { return l.Items, l.Continue })
		return err
	})
	step("jobs", false, func() (err error) {
		s.jobs, err = listAll(ctx, batch.Jobs(metav1.NamespaceAll).List, func(l *batchv1.JobList) ([]batchv1.Job, string) { return l.Items, l.Continue })
		return err
	})
	step("cronjobs", false, func() (err error) {
		s.cronjobs, err = listAll(ctx, batch.CronJobs(metav1.NamespaceAll).List, func(l *batchv1.CronJobList) ([]batchv1.CronJob, string) { return l.Items, l.Continue })
		return err
	})
	step("ingresses", false, func() (err error) {
		s.ingresses, err = listAll(ctx, net.Ingresses(metav1.NamespaceAll).List, func(l *networkingv1.IngressList) ([]networkingv1.Ingress, string) { return l.Items, l.Continue })
		return err
	})
	step("virtualmachineinstances", true, func() (err error) {
		s.vmis, err = listAll(ctx, cl.dyn.Resource(vmiResource).Namespace(metav1.NamespaceAll).List,
			func(l *unstructured.UnstructuredList) ([]unstructured.Unstructured, string) {
				return l.Items, l.GetContinue()
			})
		return err
	})

	if cfg.argocd {
		// Not optional: the operator asked for it, so an Argo CD that is not
		// there, or a ServiceAccount that may not list it, is a failed read.
		step("applications.argoproj.io", false, func() (err error) {
			s.apps, err = listAll(ctx, cl.dyn.Resource(applicationResource).Namespace(cfg.argocdNamespace).List,
				func(l *unstructured.UnstructuredList) ([]unstructured.Unstructured, string) {
					return l.Items, l.GetContinue()
				})
			return err
		})
	}

	if aborted != nil {
		return nil, aborted
	}
	if attempted > 0 && failed == attempted {
		return nil, fmt.Errorf("every list failed: %v", s.problems)
	}
	return s, nil
}

// listAll reads every page of a list call. A page that fails discards what
// was read before it: a half-read list must not pass for a complete one.
func listAll[L any, T any](
	ctx context.Context,
	list func(context.Context, metav1.ListOptions) (L, error),
	page func(L) ([]T, string),
) ([]T, error) {
	var out []T
	opts := metav1.ListOptions{Limit: pageSize}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		l, err := list(ctx, opts)
		if err != nil {
			return nil, err
		}
		items, next := page(l)
		out = append(out, items...)
		if next == "" {
			return out, ctx.Err()
		}
		opts.Continue = next
	}
}

// finished reports whether a pod has ended: it runs nothing any more.
func finished(p corev1.Pod) bool {
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
}
