// Package kubernetes provides a Kubernetes-based sandbox backend that runs
// sandbox environments as Pods in a K8s cluster. Each sandbox maps to a single
// Pod with one container running "sleep infinity", and all operations (Exec,
// Shell, file I/O) are performed via the K8s exec API over SPDY.
package kubernetes

import (
	"fmt"
	"os"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// KubernetesConfig holds configuration for the Kubernetes sandbox backend.
type KubernetesConfig struct {
	// Kubeconfig is the path to a kubeconfig file. When empty, in-cluster
	// config is used (ServiceAccount token mounted in the pod).
	Kubeconfig string

	// Namespace where sandbox pods are created. Defaults to "everstack-sandboxes".
	Namespace string

	// LabelPrefix used for sandbox pod labels. Defaults to "everstack.sandbox".
	LabelPrefix string

	// ImagePullPolicy for sandbox containers. Defaults to "IfNotPresent".
	ImagePullPolicy string

	// ServiceAccount assigned to sandbox pods. Defaults to "default".
	ServiceAccount string

	// NodeSelector provides optional node targeting for sandbox pods.
	NodeSelector map[string]string

	// ImagePullSecrets are the names of Secrets in Namespace used to
	// pull private container images (e.g. ghcr.io). Empty means
	// "anonymous pull only" — works for fully public images. Without
	// this populated, sandbox pods that reference private GHCR images
	// fail with ErrImagePull / ImagePullBackOff.
	ImagePullSecrets []string
}

// defaultConfig fills in zero-value fields with sensible defaults.
func (c *KubernetesConfig) defaultConfig() {
	if c.Namespace == "" {
		c.Namespace = "everstack-sandboxes"
	}
	if c.LabelPrefix == "" {
		c.LabelPrefix = "everstack.sandbox"
	}
	if c.ImagePullPolicy == "" {
		c.ImagePullPolicy = "IfNotPresent"
	}
	if c.ServiceAccount == "" {
		c.ServiceAccount = "default"
	}
}

// New creates a new KubernetesBackend. Resolution order when Kubeconfig is
// empty: KUBECONFIG env var → in-cluster config → ~/.kube/config (default
// kubectl location). Explicit Kubeconfig path always takes precedence so
// production deployments stay deterministic. The fallback chain exists so
// running the gateway locally against a remote cluster Just Works without
// needing to set the path explicitly.
func New(cfg KubernetesConfig) (*KubernetesBackend, error) {
	cfg.defaultConfig()

	var restCfg *rest.Config
	var err error

	if cfg.Kubeconfig != "" {
		restCfg, err = clientcmd.BuildConfigFromFlags("", cfg.Kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("kubernetes sandbox: failed to load kubeconfig %s: %w", cfg.Kubeconfig, err)
		}
	} else {
		restCfg, err = resolveKubeconfig()
		if err != nil {
			return nil, fmt.Errorf("kubernetes sandbox: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes sandbox: failed to create clientset: %w", err)
	}

	// Metrics client is optional — stats degrade gracefully when metrics-server
	// is not installed.
	metricsClient, _ := metricsv.NewForConfig(restCfg)

	return &KubernetesBackend{
		clientset:     clientset,
		metricsClient: metricsClient,
		restConfig:    restCfg,
		config:        cfg,
	}, nil
}

// resolveKubeconfig walks the implicit-config chain. Order matches kubectl's
// behavior so a developer's normal `kubectl` setup also works with the
// gateway.
func resolveKubeconfig() (*rest.Config, error) {
	if path := os.Getenv("KUBECONFIG"); path != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", path)
		if err == nil {
			logger.WithFields("source", "KUBECONFIG", "path", path).
				Info("kubernetes sandbox: kubeconfig resolved")
			return cfg, nil
		}
		logger.WithFields("path", path, "error", err.Error()).
			Warn("kubernetes sandbox: KUBECONFIG set but unloadable, falling back")
	}

	if cfg, err := rest.InClusterConfig(); err == nil {
		logger.Info("kubernetes sandbox: using in-cluster config")
		return cfg, nil
	}

	if home := clientcmd.RecommendedHomeFile; home != "" {
		if _, statErr := os.Stat(home); statErr == nil {
			cfg, err := clientcmd.BuildConfigFromFlags("", home)
			if err == nil {
				logger.WithFields("source", "default-kubeconfig", "path", home).
					Info("kubernetes sandbox: kubeconfig resolved")
				return cfg, nil
			}
			return nil, fmt.Errorf("loading default kubeconfig %s: %w", home, err)
		}
	}

	return nil, fmt.Errorf("no kubeconfig: set sandbox.kubernetes.kubeconfig, KUBECONFIG env, or run inside a cluster")
}
