package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/logbuffer"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

func pendingPodReason(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	for _, cond := range pod.Status.Conditions {
		if cond.Status == corev1.ConditionFalse && cond.Reason != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", cond.Type, cond.Reason))
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			msg := strings.TrimSpace(cs.State.Waiting.Message)
			if msg != "" {
				parts = append(parts, fmt.Sprintf("container %s waiting: %s (%s)", cs.Name, reason, msg))
			} else if reason != "" {
				parts = append(parts, fmt.Sprintf("container %s waiting: %s", cs.Name, reason))
			}
		}
	}
	if len(parts) == 0 {
		return "pod pending without explicit condition reason yet"
	}
	return strings.Join(parts, "; ")
}

func (b *KubernetesBackend) recentPodWarningEvents(ctx context.Context, name string) string {
	list, err := b.clientset.CoreV1().Events(b.config.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + name,
	})
	if err != nil || len(list.Items) == 0 {
		return ""
	}
	parts := make([]string, 0, 3)
	for i := len(list.Items) - 1; i >= 0 && len(parts) < 3; i-- {
		ev := list.Items[i]
		if ev.Type != corev1.EventTypeWarning {
			continue
		}
		msg := strings.TrimSpace(ev.Message)
		if msg == "" {
			msg = ev.Reason
		}
		parts = append(parts, fmt.Sprintf("%s: %s", ev.Reason, msg))
	}
	return strings.Join(parts, " | ")
}

// portForwardEntry tracks a single active port-forward to a K8s service/pod.
type portForwardEntry struct {
	localPort int
	stopCh    chan struct{}
}

// KubernetesBackend implements sandbox.Backend using Kubernetes Pods.
type KubernetesBackend struct {
	clientset     kubernetes.Interface
	metricsClient metricsv.Interface
	restConfig    *rest.Config
	config        KubernetesConfig

	logsMu sync.RWMutex
	logs   map[string]*logbuffer.Buffer // sandbox ID → log buffer

	portFwdMu    sync.Mutex
	portForwards map[string]*portForwardEntry // "svcName:port" → entry
}

// ensure interface compliance at compile time.
var _ sandbox.Backend = (*KubernetesBackend)(nil)

func (b *KubernetesBackend) Name() string { return "kubernetes" }

func (b *KubernetesBackend) RunnerCapabilities() sandbox.RunnerCapabilities {
	return sandbox.RunnerCapabilities{
		Target:    b.Name(),
		Placement: sandbox.RunnerPlacementCluster,
		Health:    sandbox.RunnerHealthKubernetesAPI,
		Features: sandbox.RunnerFeatures{
			WorkspaceSnapshot: true,
			PortExposure:      true,
			PortDetection:     true,
			ComputerUse:       true,
		},
	}
}

func (b *KubernetesBackend) DescribePending(ctx context.Context, id string) string {
	name := podName(id)
	pod, err := b.clientset.CoreV1().Pods(b.config.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil || pod == nil {
		return ""
	}
	if pod.Status.Phase != corev1.PodPending {
		return ""
	}
	reason := pendingPodReason(pod)
	events := b.recentPodWarningEvents(ctx, name)
	if reason == "" {
		return events
	}
	if events == "" {
		return reason
	}
	return reason + " | events: " + events
}

// getOrCreateLogs returns the log buffer for a sandbox, creating one if needed.
func (b *KubernetesBackend) getOrCreateLogs(id string) *logbuffer.Buffer {
	b.logsMu.RLock()
	buf, ok := b.logs[id]
	b.logsMu.RUnlock()
	if ok {
		return buf
	}

	b.logsMu.Lock()
	defer b.logsMu.Unlock()
	if buf, ok := b.logs[id]; ok {
		return buf
	}
	buf = logbuffer.NewBuffer()
	if b.logs == nil {
		b.logs = make(map[string]*logbuffer.Buffer)
	}
	b.logs[id] = buf
	return buf
}

// Create provisions a new sandbox as a Kubernetes Pod.
// If a pod with the same name is still being deleted (e.g. from a previous
// session that was recreated), Create retries with backoff until the old pod
// is fully gone or the context expires.
func (b *KubernetesBackend) Create(ctx context.Context, id string, config sandbox.InstanceConfig) (*sandbox.Instance, error) {
	pod := b.buildPodSpec(id, config)

	// Wait for any lingering pod with the same name to finish deleting.
	// K8s returns "object is being deleted" if we try to create while the
	// old pod is still in Terminating state.
	if err := b.waitForPodDeletion(ctx, pod.Name, 30*time.Second); err != nil {
		logger.WithFields("pod", pod.Name, "error", err.Error()).
			Warn("k8s_sandbox: old pod still exists, force-deleting")
		// Force-delete the stuck pod so creation can proceed immediately.
		zero := int64(0)
		_ = b.clientset.CoreV1().Pods(b.config.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
			GracePeriodSeconds: &zero,
		})
		// Wait for the force-delete to actually take effect.
		// A 2s sleep is often not enough for stuck pods — poll until gone.
		if waitErr := b.waitForPodDeletion(ctx, pod.Name, 15*time.Second); waitErr != nil {
			logger.WithFields("pod", pod.Name, "error", waitErr.Error()).
				Warn("k8s_sandbox: pod still exists after force-delete, removing finalizers")
			// Remove finalizers that may be blocking deletion
			b.removeFinalizersIfStuck(ctx, pod.Name)
			time.Sleep(3 * time.Second)
		}
	}

	var created *corev1.Pod
	var err error
	maxRetries := 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		created, err = b.clientset.CoreV1().Pods(b.config.Namespace).Create(ctx, pod, metav1.CreateOptions{})
		if err == nil {
			break
		}
		// Retry on "object is being deleted" — the old pod hasn't fully terminated yet
		if strings.Contains(err.Error(), "object is being deleted") || strings.Contains(err.Error(), "already exists") {
			if attempt < maxRetries-1 {
				backoff := time.Duration(1<<uint(attempt)) * time.Second // 1s, 2s, 4s, 8s
				logger.WithFields("pod", pod.Name, "attempt", attempt+1, "backoff", backoff.String()).
					Info("k8s_sandbox: pod conflict, waiting for old pod to terminate")
				select {
				case <-time.After(backoff):
					continue
				case <-ctx.Done():
					return nil, fmt.Errorf("context cancelled while waiting for pod cleanup: %w", ctx.Err())
				}
			}
		}
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create pod after retries: %w", err)
	}

	// Wait for the pod to reach Running phase.
	// 4 minutes allows for cold image pulls and slower cluster scheduling.
	if err := b.waitForPodRunning(ctx, pod.Name, 4*time.Minute); err != nil {
		// Clean up the pending pod
		_ = b.clientset.CoreV1().Pods(b.config.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		return nil, fmt.Errorf("pod did not reach Running phase: %w", err)
	}

	// Apply network policy based on network mode
	if config.NetworkMode != sandbox.NetworkAllow {
		if err := b.applyNetworkPolicy(ctx, id, config); err != nil {
			logger.WithFields("sandbox_id", id, "error", err.Error()).
				Warn("k8s_sandbox: failed to apply network policy")
		}
	}

	now := time.Now()
	expiresAt := now.Add(time.Duration(config.TimeoutSeconds) * time.Second)

	// Initialize log buffer
	sl := b.getOrCreateLogs(id)
	sl.Append(logbuffer.Entry{
		Timestamp: now,
		Stream:    "system",
		Line:      fmt.Sprintf("sandbox created (image=%s, pod=%s)", config.Image, pod.Name),
	})

	logger.WithFields("sandbox_id", id, "pod", pod.Name, "image", config.Image).
		Info("k8s_sandbox: pod created and running")

	return &sandbox.Instance{
		ID:          id,
		ContainerID: string(created.UID),
		Status:      sandbox.StatusRunning,
		Config:      config,
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
		LastUsedAt:  now,
		Backend:     "kubernetes",
	}, nil
}

// waitForPodRunning watches the pod until it reaches the Running phase or the
// timeout expires.
func (b *KubernetesBackend) waitForPodRunning(ctx context.Context, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lastPendingLog := time.Time{}

	watcher, err := b.clientset.CoreV1().Pods(b.config.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + name,
	})
	if err != nil {
		return fmt.Errorf("failed to watch pod: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for pod %s to start", name)
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch channel closed for pod %s", name)
			}
			if event.Type == watch.Deleted {
				return fmt.Errorf("pod %s was deleted before reaching Running", name)
			}
			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			if pod.Status.Phase == corev1.PodPending {
				if lastPendingLog.IsZero() || time.Since(lastPendingLog) >= 15*time.Second {
					lastPendingLog = time.Now()
					reason := pendingPodReason(pod)
					events := b.recentPodWarningEvents(ctx, name)
					fields := map[string]interface{}{"pod": name, "reason": reason}
					if events != "" {
						fields["events"] = events
					}
					logger.WithFields(fields).Warn("k8s_sandbox: pod still pending")
				}
			}
			if pod.Status.Phase == corev1.PodRunning && allContainersReady(pod) {
				return nil
			}
			if pod.Status.Phase == corev1.PodFailed {
				return fmt.Errorf("pod %s entered Failed phase", name)
			}
		}
	}
}

// allContainersReady returns true when every container in the pod reports Ready.
// Pod phase=Running only means the pod was scheduled, not that containers are
// accepting exec connections.
func allContainersReady(pod *corev1.Pod) bool {
	if len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}

// waitForPodDeletion polls until the named pod no longer exists. This is used
// before creating a pod to ensure a previous pod with the same name has fully
// terminated. Returns nil immediately if the pod does not exist.
func (b *KubernetesBackend) waitForPodDeletion(ctx context.Context, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	_, err := b.clientset.CoreV1().Pods(b.config.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to check pod %s before deletion wait: %w", name, err)
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for pod %s to be deleted", name)
		case <-ticker.C:
			pod, getErr := b.clientset.CoreV1().Pods(b.config.Namespace).Get(ctx, name, metav1.GetOptions{})
			if getErr != nil {
				if k8serrors.IsNotFound(getErr) {
					return nil
				}
				return fmt.Errorf("failed while polling pod %s deletion: %w", name, getErr)
			}
			fields := map[string]interface{}{"pod": name, "phase": pod.Status.Phase}
			if pod.DeletionTimestamp != nil {
				fields["deletion_timestamp"] = pod.DeletionTimestamp.Time.Format(time.RFC3339)
				logger.WithFields(fields).Debug("k8s_sandbox: waiting for pod deletion")
			} else {
				logger.WithFields(fields).Debug("k8s_sandbox: pod still exists and is not deleting")
			}
		}
	}
}

// removeFinalizersIfStuck patches the pod to remove all finalizers, unblocking
// deletion when a pod is stuck in Terminating due to a finalizer hold.
func (b *KubernetesBackend) removeFinalizersIfStuck(ctx context.Context, name string) {
	pod, err := b.clientset.CoreV1().Pods(b.config.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return // pod already gone
	}
	if len(pod.Finalizers) == 0 {
		return
	}
	pod.Finalizers = nil
	_, err = b.clientset.CoreV1().Pods(b.config.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
	if err != nil {
		logger.WithFields("pod", name, "error", err.Error()).
			Warn("k8s_sandbox: failed to remove finalizers from stuck pod")
	} else {
		logger.WithFields("pod", name).Info("k8s_sandbox: removed finalizers from stuck pod")
	}
}

// Destroy deletes the sandbox pod and its associated NetworkPolicy.
func (b *KubernetesBackend) Destroy(ctx context.Context, id string) error {
	// Clean up port-forwards and exposure services
	b.stopPortForwards(id)
	b.deletePortServices(ctx, id)

	// Close log buffer
	b.logsMu.Lock()
	if sl, ok := b.logs[id]; ok {
		sl.Close()
		delete(b.logs, id)
	}
	b.logsMu.Unlock()

	name := podName(id)
	gracePeriod := int64(5)

	// Delete the pod
	err := b.clientset.CoreV1().Pods(b.config.Namespace).Delete(ctx, name, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	if err != nil {
		logger.WithFields("sandbox_id", id, "error", err.Error()).
			Debug("k8s_sandbox: failed to delete pod")
	} else {
		// Wait for the pod to actually be removed. This prevents race
		// conditions when the same sandbox ID is recreated immediately after
		// destroy (K8s rejects creates while the old pod is terminating).
		if waitErr := b.waitForPodDeletion(ctx, name, 15*time.Second); waitErr != nil {
			logger.WithFields("sandbox_id", id, "error", waitErr.Error()).
				Warn("k8s_sandbox: pod delete issued but pod not yet fully removed")
		}
	}

	// Delete associated NetworkPolicy (best-effort)
	_ = b.deleteNetworkPolicy(ctx, id)

	logger.WithFields("sandbox_id", id, "pod", name).Debug("k8s_sandbox: pod destroyed")
	return err
}

// Status returns the current state of a sandbox pod.
func (b *KubernetesBackend) Status(ctx context.Context, id string) (*sandbox.Instance, error) {
	name := podName(id)
	pod, err := b.clientset.CoreV1().Pods(b.config.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}

	status := mapPodPhase(pod.Status.Phase)
	prefix := b.config.LabelPrefix

	inst := &sandbox.Instance{
		ID:          id,
		ContainerID: string(pod.UID),
		Status:      status,
		Backend:     "kubernetes",
	}

	// Reconstruct config from annotations if available
	if configJSON, ok := pod.Annotations[prefix+"/config"]; ok {
		var cfg sandbox.InstanceConfig
		if json.Unmarshal([]byte(configJSON), &cfg) == nil {
			inst.Config = cfg
		}
	}

	return inst, nil
}

// Healthy verifies that the K8s API server is reachable and the target
// namespace exists.
func (b *KubernetesBackend) Healthy(ctx context.Context) error {
	// Verify API server connectivity
	_, err := b.clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("kubernetes API unreachable: %w", err)
	}

	// Verify namespace exists
	_, err = b.clientset.CoreV1().Namespaces().Get(ctx, b.config.Namespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("namespace %s not found: %w", b.config.Namespace, err)
	}

	return nil
}

// List returns all sandbox pods managed by everstack in the configured namespace.
// Used on startup to rediscover pods from a previous run.
func (b *KubernetesBackend) List(ctx context.Context) ([]*sandbox.Instance, error) {
	prefix := b.config.LabelPrefix
	selector := prefix + "/managed-by=everstack"

	pods, err := b.clientset.CoreV1().Pods(b.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list sandbox pods: %w", err)
	}

	var instances []*sandbox.Instance
	for _, pod := range pods.Items {
		// Only include running pods
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		id := pod.Labels[prefix+"/id"]
		sessionID := pod.Labels[prefix+"/session-id"]
		tenantID := pod.Labels[prefix+"/tenant-id"]

		if id == "" || sessionID == "" {
			continue
		}

		var expiresAt time.Time
		if v, ok := pod.Annotations[prefix+"/expires-at"]; ok {
			if parsed, err := time.Parse(time.RFC3339, v); err == nil {
				expiresAt = parsed
			}
		}

		var idleRetentionSecs int
		if v, ok := pod.Annotations[prefix+"/idle-retention"]; ok {
			fmt.Sscanf(v, "%d", &idleRetentionSecs)
		}

		// Reconstruct config from annotation
		var cfg sandbox.InstanceConfig
		if configJSON, ok := pod.Annotations[prefix+"/config"]; ok {
			_ = json.Unmarshal([]byte(configJSON), &cfg)
		}
		cfg.SessionID = sessionID
		cfg.TenantID = tenantID

		// Determine image from container spec as fallback
		if cfg.Image == "" && len(pod.Spec.Containers) > 0 {
			cfg.Image = pod.Spec.Containers[0].Image
		}

		createdAt := pod.CreationTimestamp.Time

		agentID := strings.TrimSpace(pod.Labels[prefix+"/agent-id"])
		// Backward compatibility: older pods may not have persistent/agent labels.
		// Fall back to config annotation (which includes agent_id) so restored
		// troopers still idle-stop correctly after restart.
		if agentID == "" {
			agentID = strings.TrimSpace(cfg.AgentID)
		}
		persistent := pod.Labels[prefix+"/persistent"] == "true" || agentID != ""

		instances = append(instances, &sandbox.Instance{
			ID:                id,
			ContainerID:       string(pod.UID),
			Status:            sandbox.StatusRunning,
			Config:            cfg,
			CreatedAt:         createdAt,
			ExpiresAt:         expiresAt,
			LastUsedAt:        createdAt, // best-effort; will be updated on first use
			IdleRetentionSecs: idleRetentionSecs,
			Backend:           "kubernetes",
			Persistent:        persistent,
			AgentID:           agentID,
		})

		// Re-create log buffer for restored pod
		b.getOrCreateLogs(id)
	}

	return instances, nil
}

// mapPodPhase converts a K8s PodPhase to a sandbox Status.
func mapPodPhase(phase corev1.PodPhase) sandbox.Status {
	switch phase {
	case corev1.PodPending:
		return sandbox.StatusPending
	case corev1.PodRunning:
		return sandbox.StatusRunning
	case corev1.PodSucceeded, corev1.PodUnknown:
		return sandbox.StatusStopped
	case corev1.PodFailed:
		return sandbox.StatusFailed
	default:
		return sandbox.StatusStopped
	}
}

// Ensure KubernetesBackend implements optional sandbox interfaces at compile time.
var _ sandbox.PortExposer = (*KubernetesBackend)(nil)
var _ sandbox.BackendTargeter = (*KubernetesBackend)(nil)
var _ sandbox.Snapshotter = (*KubernetesBackend)(nil)
var _ sandbox.PortDetector = (*KubernetesBackend)(nil)

// Snapshot archives srcPath from inside the pod to a local destPath (tar.gz).
// Uses the K8s exec API to stream tar output directly to a file.
func (b *KubernetesBackend) Snapshot(ctx context.Context, id string, srcPath string, destPath string) error {
	return b.snapshotStream(ctx, id, srcPath, destPath)
}

// snapshotStream uses the K8s exec API to stream tar output directly to a file.
func (b *KubernetesBackend) snapshotStream(ctx context.Context, id string, srcPath string, destPath string) error {
	name := podName(id)

	outFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create snapshot file: %w", err)
	}
	defer outFile.Close()

	execReq := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(name).
		Namespace(b.config.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "sandbox",
			// Use find piped to tar to exclude the root "." entry from the
			// archive. Including "." causes chmod warnings on restore when
			// the mount-point is read-only or capabilities are dropped.
			Command: []string{"sh", "-c", fmt.Sprintf("cd '%s' && find . -mindepth 1 -print0 | tar czf - --null -T -", srcPath)},
			Stdout:  true,
			Stderr:  true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(b.restConfig, "POST", execReq.URL())
	if err != nil {
		os.Remove(destPath)
		return fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	var stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: outFile,
		Stderr: &stderr,
	})
	if err != nil {
		os.Remove(destPath)
		return fmt.Errorf("snapshot stream failed: %w (stderr: %s)", err, stderr.String())
	}

	return nil
}

// Restore extracts a tar.gz snapshot from the host into destPath inside the pod.
func (b *KubernetesBackend) Restore(ctx context.Context, id string, snapshotFile string, destPath string) error {
	name := podName(id)

	inFile, err := os.Open(snapshotFile)
	if err != nil {
		return fmt.Errorf("failed to open snapshot file: %w", err)
	}
	defer inFile.Close()

	// Wrap tar in sh -c to tolerate exit code 1 (warnings). GNU tar exits
	// 1 when it can't chmod the "." mount-point entry (read-only root +
	// dropped capabilities). Exit 0-1 → success, exit 2 → fatal error.
	tarCmd := fmt.Sprintf(
		"tar xzf - -C '%s' --no-overwrite-dir --no-same-owner --no-same-permissions --warning=no-timestamp 2>/dev/null; rc=$?; [ $rc -le 1 ] && exit 0 || exit $rc",
		destPath,
	)
	execReq := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(name).
		Namespace(b.config.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "sandbox",
			Command:   []string{"sh", "-c", tarCmd},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(b.restConfig, "POST", execReq.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor for restore: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  inFile,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("restore stream failed: %w (stderr: %s)", err, stderr.String())
	}

	return nil
}

// isOutOfCluster returns true when the backend was configured with an external
// kubeconfig (i.e. the gateway is not running inside the K8s cluster).
func (b *KubernetesBackend) isOutOfCluster() bool {
	return b.config.Kubeconfig != ""
}

// ExposePort creates a Service targeting the sandbox pod on the given port.
// When running out-of-cluster it creates a NodePort service so the proxy can
// reach the pod via the node IP. In-cluster it uses ClusterIP.
func (b *KubernetesBackend) ExposePort(ctx context.Context, id string, port int, protocol string) (int, error) {
	name := podName(id)
	svcName := fmt.Sprintf("%s-%d", name, port)
	prefix := b.config.LabelPrefix

	svcProtocol := corev1.ProtocolTCP
	if strings.EqualFold(protocol, "udp") {
		svcProtocol = corev1.ProtocolUDP
	}

	svcType := corev1.ServiceTypeClusterIP
	if b.isOutOfCluster() {
		svcType = corev1.ServiceTypeNodePort
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: b.config.Namespace,
			Labels: map[string]string{
				prefix + "/managed-by": "everstack",
				prefix + "/id":         id,
				prefix + "/port-svc":   fmt.Sprintf("%d", port),
			},
		},
		Spec: corev1.ServiceSpec{
			Type: svcType,
			Selector: map[string]string{
				prefix + "/id": id,
			},
			Ports: []corev1.ServicePort{
				{
					Port:     int32(port),
					Protocol: svcProtocol,
				},
			},
		},
	}

	created, err := b.clientset.CoreV1().Services(b.config.Namespace).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Service already exists (e.g. from a previous session before server restart).
			// Reuse it — the selector still targets the same pod.
			existing, getErr := b.clientset.CoreV1().Services(b.config.Namespace).Get(ctx, svcName, metav1.GetOptions{})
			if getErr != nil {
				return 0, fmt.Errorf("service already exists and failed to get it: %w", getErr)
			}
			created = existing
			logger.WithFields("sandbox_id", id, "port", port, "service", svcName).
				Info("k8s_sandbox: reusing existing service for port exposure")
		} else {
			return 0, fmt.Errorf("failed to create service: %w", err)
		}
	} else {
		logger.WithFields("sandbox_id", id, "port", port, "type", string(svcType), "service", svcName).
			Info("k8s_sandbox: service created for port exposure")
	}

	// For NodePort services, return the allocated node port
	hostPort := port
	if svcType == corev1.ServiceTypeNodePort && len(created.Spec.Ports) > 0 {
		hostPort = int(created.Spec.Ports[0].NodePort)
	}

	return hostPort, nil
}

// BackendTarget returns the address the proxy should dial to reach the sandbox port.
// Out-of-cluster: starts a kubectl port-forward to the service and returns localhost:{localPort}.
// In-cluster: uses the service FQDN.
func (b *KubernetesBackend) BackendTarget(ctx context.Context, id string, port int) (string, error) {
	if !b.isOutOfCluster() {
		// In-cluster: use the service DNS name
		name := podName(id)
		svcName := fmt.Sprintf("%s-%d", name, port)
		return fmt.Sprintf("%s.%s.svc.cluster.local:%d", svcName, b.config.Namespace, port), nil
	}

	// Out-of-cluster: start a port-forward to the service so the gateway
	// can reach the pod via localhost. This avoids requiring minikube tunnel
	// or routable node IPs.
	name := podName(id)
	svcName := fmt.Sprintf("%s-%d", name, port)

	localPort, err := b.startPortForward(ctx, svcName, port)
	if err != nil {
		// Fallback to NodePort + node IP if port-forward fails
		logger.WithFields("service", svcName, "error", err.Error()).
			Warn("k8s_sandbox: port-forward failed, falling back to NodePort")
		return b.nodePortTarget(ctx, svcName)
	}

	return fmt.Sprintf("127.0.0.1:%d", localPort), nil
}

// nodePortTarget resolves the NodePort + node IP for out-of-cluster access.
func (b *KubernetesBackend) nodePortTarget(ctx context.Context, svcName string) (string, error) {
	svc, err := b.clientset.CoreV1().Services(b.config.Namespace).Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get service %s: %w", svcName, err)
	}

	if len(svc.Spec.Ports) == 0 {
		return "", fmt.Errorf("service %s has no ports", svcName)
	}
	nodePort := svc.Spec.Ports[0].NodePort

	nodeIP, err := b.getNodeIP(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get node IP: %w", err)
	}

	return fmt.Sprintf("%s:%d", nodeIP, nodePort), nil
}

// startPortForward creates a kubectl-style port-forward from a local ephemeral
// port to the sandbox pod on the given container port. This avoids the need for
// minikube tunnel or routable node IPs when running out-of-cluster.
// The port-forward is cached and reused for subsequent calls with the same key.
func (b *KubernetesBackend) startPortForward(ctx context.Context, svcName string, port int) (int, error) {
	b.portFwdMu.Lock()
	defer b.portFwdMu.Unlock()

	key := fmt.Sprintf("%s:%d", svcName, port)
	if entry, ok := b.portForwards[key]; ok {
		return entry.localPort, nil
	}

	// Allocate a free local port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to get free port: %w", err)
	}
	localPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Port-forward targets a pod, not a service. Derive the pod name from the
	// service name (service = podName-port, e.g. "sbx-abc-3000").
	pName := strings.TrimSuffix(svcName, fmt.Sprintf("-%d", port))

	transport, upgrader, err := spdy.RoundTripperFor(b.restConfig)
	if err != nil {
		return 0, fmt.Errorf("failed to create SPDY transport: %w", err)
	}

	pfURL := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(b.config.Namespace).
		Name(pName).
		SubResource("portforward").
		URL()

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", pfURL)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", localPort, port)}, stopCh, readyCh, nil, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create port-forward: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fw.ForwardPorts()
	}()

	// Wait for the port-forward to become ready or fail.
	select {
	case <-readyCh:
		// ready
	case err := <-errCh:
		return 0, fmt.Errorf("port-forward failed: %w", err)
	case <-time.After(10 * time.Second):
		close(stopCh)
		return 0, fmt.Errorf("port-forward timed out after 10s")
	}

	if b.portForwards == nil {
		b.portForwards = make(map[string]*portForwardEntry)
	}
	b.portForwards[key] = &portForwardEntry{
		localPort: localPort,
		stopCh:    stopCh,
	}

	logger.WithFields("service", svcName, "port", port, "local_port", localPort).
		Info("k8s_sandbox: port-forward started")

	// Monitor for unexpected termination and clean up the cache entry.
	go func() {
		if err := <-errCh; err != nil {
			logger.WithFields("service", svcName, "port", port, "error", err.Error()).
				Warn("k8s_sandbox: port-forward terminated")
			b.portFwdMu.Lock()
			delete(b.portForwards, key)
			b.portFwdMu.Unlock()
		}
	}()

	return localPort, nil
}

// stopPortForwards tears down all active port-forwards for a given sandbox ID.
func (b *KubernetesBackend) stopPortForwards(id string) {
	b.portFwdMu.Lock()
	defer b.portFwdMu.Unlock()

	prefix := podName(id)
	for key, entry := range b.portForwards {
		if strings.HasPrefix(key, prefix) {
			close(entry.stopCh)
			delete(b.portForwards, key)
		}
	}
}

// getNodeIP returns the InternalIP of the first Ready node in the cluster.
func (b *KubernetesBackend) getNodeIP(ctx context.Context) (string, error) {
	nodes, err := b.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list nodes: %w", err)
	}

	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				return addr.Address, nil
			}
		}
	}

	return "", fmt.Errorf("no node with InternalIP found")
}

// UnexposePort deletes the ClusterIP Service for the given port.
func (b *KubernetesBackend) UnexposePort(ctx context.Context, id string, port int) error {
	name := podName(id)
	svcName := fmt.Sprintf("%s-%d", name, port)

	err := b.clientset.CoreV1().Services(b.config.Namespace).Delete(ctx, svcName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}

	logger.WithFields("sandbox_id", id, "port", port, "service", svcName).
		Debug("k8s_sandbox: port service deleted")

	return nil
}

// deletePortServices deletes all ClusterIP services associated with a sandbox.
// Called during Destroy for cleanup.
func (b *KubernetesBackend) deletePortServices(ctx context.Context, id string) {
	prefix := b.config.LabelPrefix
	selector := fmt.Sprintf("%s/id=%s,%s/port-svc", prefix, id, prefix)

	svcs, err := b.clientset.CoreV1().Services(b.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		logger.WithFields("sandbox_id", id, "error", err.Error()).
			Debug("k8s_sandbox: failed to list port services for cleanup")
		return
	}

	for _, svc := range svcs.Items {
		_ = b.clientset.CoreV1().Services(b.config.Namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{})
	}
}
