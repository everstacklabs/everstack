package kubernetes

import (
	"context"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Stats returns a one-shot resource usage snapshot for a sandbox pod.
// Uses the metrics-server API (k8s.io/metrics) when available.
// If metrics-server is not installed, returns zeroed stats.
func (b *KubernetesBackend) Stats(ctx context.Context, id string) (*sandbox.ContainerStats, error) {
	if b.metricsClient == nil {
		return &sandbox.ContainerStats{Timestamp: time.Now()}, nil
	}

	name := podName(id)
	podMetrics, err := b.metricsClient.MetricsV1beta1().PodMetricses(b.config.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		logger.WithFields("sandbox_id", id, "error", err.Error()).
			Debug("k8s_sandbox: metrics-server unavailable, returning zeroed stats")
		return &sandbox.ContainerStats{Timestamp: time.Now()}, nil
	}

	var cpuNanos int64
	var memBytes int64
	for _, c := range podMetrics.Containers {
		if cpu := c.Usage.Cpu(); cpu != nil {
			cpuNanos += cpu.MilliValue() // millicores
		}
		if mem := c.Usage.Memory(); mem != nil {
			memBytes += mem.Value()
		}
	}

	// Get pod spec to determine memory limit
	pod, err := b.clientset.CoreV1().Pods(b.config.Namespace).Get(ctx, name, metav1.GetOptions{})
	var memLimit int64
	if err == nil && len(pod.Spec.Containers) > 0 {
		if limit := pod.Spec.Containers[0].Resources.Limits.Memory(); limit != nil {
			memLimit = limit.Value()
		}
	}

	// CPU: millicores → approximate percent (relative to 1 core = 1000m)
	cpuPercent := float64(cpuNanos) / 10.0 // 1000m = 100%

	memPercent := 0.0
	if memLimit > 0 {
		memPercent = float64(memBytes) / float64(memLimit) * 100.0
	}

	return &sandbox.ContainerStats{
		CPUPercent:    cpuPercent,
		MemoryUsage:   memBytes,
		MemoryLimit:   memLimit,
		MemoryPercent: memPercent,
		// Network and block I/O are not available from metrics-server
		Timestamp: time.Now(),
	}, nil
}

