package kubernetes

import (
	"fmt"
	"strings"

	"k8s.io/client-go/dynamic"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	userAgent = "trica-collector"
	// A run issues a dozen list calls in sequence; the client-side rate limit
	// of client-go (5 per second) would only slow it down.
	clientQPS   = 20
	clientBurst = 40
)

// inCluster reports whether the target runs inside the cluster it collects.
func (c *Collector) inCluster() bool {
	a := strings.TrimSpace(c.target.Address)
	return a == "" || strings.EqualFold(a, inClusterAddress)
}

// restConfig builds the client configuration from the target. It never reads
// a Secret of the cluster: the credentials are the pod's ServiceAccount, a
// kubeconfig file, or a bearer token referenced by the target's password.
func (c *Collector) restConfig() (*rest.Config, error) {
	var cfg *rest.Config
	if c.inCluster() {
		var err error
		if cfg, err = rest.InClusterConfig(); err != nil {
			return nil, fmt.Errorf("in-cluster configuration: %w", err)
		}
	} else {
		rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: strings.TrimSpace(c.opts.Kubeconfig)}
		overrides := &clientcmd.ConfigOverrides{CurrentContext: strings.TrimSpace(c.opts.Context)}
		// An address that is a URL overrides the server of the kubeconfig
		// context (a tunnel, a load balancer in front of the API servers).
		if a := strings.TrimSpace(c.target.Address); strings.Contains(a, "://") {
			overrides.ClusterInfo.Server = a
		}
		var err error
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("kubeconfig %s: %w", rules.ExplicitPath, err)
		}
	}

	if c.target.Password != "" {
		token, err := c.target.Password.Resolve()
		if err != nil {
			return nil, err
		}
		cfg.BearerToken, cfg.BearerTokenFile = token, ""
		cfg.Username, cfg.Password = "", ""
	}
	if ca := strings.TrimSpace(c.target.TLS.CAFile); ca != "" {
		cfg.CAFile, cfg.CAData = ca, nil
	}
	if c.target.TLS.Insecure {
		// client-go refuses an insecure config that also names a CA.
		cfg.Insecure = true
		cfg.CAFile, cfg.CAData = "", nil
	}
	cfg.Timeout = c.target.EffectiveTimeout()
	cfg.UserAgent = userAgent
	cfg.QPS, cfg.Burst = clientQPS, clientBurst
	return cfg, nil
}

// connectReal builds the typed and the dynamic client of the cluster. Nothing
// is sent yet: a wrong address shows on the first call, as a failed health.
func (c *Collector) connectReal() (*clients, error) {
	cfg, err := c.restConfig()
	if err != nil {
		return nil, err
	}
	kube, err := kubeclient.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &clients{kube: kube, dyn: dyn, endpoint: cfg.Host}, nil
}
