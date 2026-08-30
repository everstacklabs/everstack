package browserpool

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type kubernetesRuntime struct {
	cfg            Config
	client         kubernetes.Interface
	restConfig     *rest.Config
	outOfCluster   bool
	portForwarding portForwardManager
}

func newKubernetesRuntime(cfg Config, client kubernetes.Interface, restConfig *rest.Config, outOfCluster bool) *kubernetesRuntime {
	runtime := &kubernetesRuntime{
		cfg:          cfg,
		client:       client,
		restConfig:   restConfig,
		outOfCluster: outOfCluster,
	}
	runtime.portForwarding = portForwardManager{runtime: runtime, entries: make(map[string]*portForwardEntry), pending: make(map[string]*portForwardCall)}
	return runtime
}

// resolveKubeconfig deliberately matches the sandbox backend's resolution
// chain so local kubectl configuration works without changing production's
// in-cluster behavior.
func resolveKubeconfig(explicitPath string) (*rest.Config, bool, error) {
	if explicitPath != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", explicitPath)
		if err != nil {
			return nil, false, fmt.Errorf("browserpool: load kubeconfig %s: %w", explicitPath, err)
		}
		return cfg, true, nil
	}

	if path := os.Getenv("KUBECONFIG"); path != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", path)
		if err == nil {
			logger.WithFields("source", "KUBECONFIG", "path", path).
				Info("kubernetes sandbox: kubeconfig resolved")
			return cfg, true, nil
		}
		logger.WithFields("path", path, "error", err.Error()).
			Warn("kubernetes sandbox: KUBECONFIG set but unloadable, falling back")
	}

	if cfg, err := rest.InClusterConfig(); err == nil {
		logger.Info("kubernetes sandbox: using in-cluster config")
		return cfg, false, nil
	}

	if home := clientcmd.RecommendedHomeFile; home != "" {
		if _, statErr := os.Stat(home); statErr == nil {
			cfg, err := clientcmd.BuildConfigFromFlags("", home)
			if err == nil {
				logger.WithFields("source", "default-kubeconfig", "path", home).
					Info("kubernetes sandbox: kubeconfig resolved")
				return cfg, true, nil
			}
			return nil, false, fmt.Errorf("browserpool: loading default kubeconfig %s: %w", home, err)
		}
	}

	return nil, false, fmt.Errorf("browserpool: no kubeconfig: set sandbox.kubernetes.kubeconfig, KUBECONFIG env, or run inside a cluster")
}

func (r *kubernetesRuntime) provision(ctx context.Context, tenantID, sessionID string, settings browserSettings) (*managedPod, error) {
	var pod *corev1.Pod
	var managed *managedPod
	created := false
	for attempt := 0; attempt < 5; attempt++ {
		name, err := randomPodName()
		if err != nil {
			return nil, err
		}
		pod = r.browserPod(name, tenantID, sessionID, settings)
		managed = &managedPod{
			name:        name,
			tenantID:    tenantID,
			sessionID:   sessionID,
			state:       podDeleting,
			settings:    settings,
			labelValues: copyLabels(pod.Labels),
		}
		if _, err := r.client.CoreV1().Pods(r.cfg.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
			if apierrors.IsAlreadyExists(err) {
				continue
			}
			// Create timeouts are ambiguous: the API server may have persisted
			// the pod. Return its reserved identity so the pool quarantines it.
			return managed, fmt.Errorf("browserpool: create pod %s: %w", name, err)
		}
		created = true
		break
	}
	if !created {
		return nil, fmt.Errorf("browserpool: failed to allocate a unique pod name after 5 attempts")
	}
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	// The pool cannot track a pod until provision returns, and readiness can
	// outlast LeaseTTL. Heartbeat it here so another replica cannot mistake a
	// live provisioning pod for an orphan.
	go func() {
		defer close(heartbeatDone)
		r.runProvisioningHeartbeat(heartbeatCtx, managed.name, reaperInterval)
	}()
	defer func() {
		stopHeartbeat()
		<-heartbeatDone
	}()

	readyPod, err := r.waitForPodReady(ctx, managed.name, 3*time.Minute)
	if err != nil {
		return managed, err
	}

	cdpBaseURL, streamURL, err := r.podURLs(ctx, readyPod, settings)
	if err != nil {
		return managed, err
	}
	if err := waitForCDP(ctx, cdpBaseURL); err != nil {
		return managed, fmt.Errorf("browserpool: confirm CDP readiness for pod %s: %w", managed.name, err)
	}

	managed.state = podBound
	managed.cdpBaseURL = cdpBaseURL
	managed.streamURL = streamURL
	managed.labelValues = copyLabels(readyPod.Labels)
	return managed, nil
}

func (r *kubernetesRuntime) runProvisioningHeartbeat(ctx context.Context, podName string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, interval)
			err := r.refreshLease(refreshCtx, podName, now.Add(r.cfg.LeaseTTL))
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.WithFields("pod", podName, "error", err.Error()).
					Warn("browserpool: failed to refresh provisioning pod lease")
			}
		}
	}
}

func (r *kubernetesRuntime) browserPod(name, tenantID, sessionID string, settings browserSettings) *corev1.Pod {
	shmLimit := resource.MustParse("256Mi")
	tmpLimit := resource.MustParse("256Mi")
	cpuRequest := resource.MustParse("500m")
	memoryRequest := resource.MustParse("512Mi")
	cpuLimit := resource.MustParse("1000m")
	memoryLimit := resource.MustParse("1Gi")
	allowPrivilegeEscalation := false
	runAsNonRoot := false

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: r.cfg.Namespace,
			Annotations: map[string]string{
				r.cfg.LabelPrefix + "/expires-at": time.Now().Add(r.cfg.LeaseTTL).Format(time.RFC3339),
			},
			Labels: map[string]string{
				r.cfg.LabelPrefix + "/tenant-id":  tenantID,
				r.cfg.LabelPrefix + "/session-id": sessionID,
				r.cfg.LabelPrefix + "/managed-by": managedByValue,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{{
				Name:            "browser",
				Image:           r.cfg.Image,
				ImagePullPolicy: browserImagePullPolicy(r.cfg.Image),
				Env: []corev1.EnvVar{
					{Name: "BROWSER_HEADLESS", Value: strconv.FormatBool(settings.headless)},
					{Name: "BROWSER_CDP_PORT", Value: strconv.Itoa(settings.cdpPort)},
					{Name: "BROWSER_STREAM_PORT", Value: strconv.Itoa(settings.streamPort)},
					{Name: "HOME", Value: "/tmp/browser-home"},
				},
				Ports: []corev1.ContainerPort{
					{Name: "cdp", ContainerPort: int32(settings.cdpPort)},
					{Name: "stream", ContainerPort: int32(settings.streamPort)},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    cpuRequest,
						corev1.ResourceMemory: memoryRequest,
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    cpuLimit,
						corev1.ResourceMemory: memoryLimit,
					},
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
						Path: "/json/version",
						Port: intstr.FromInt32(int32(settings.cdpPort)),
					}},
					InitialDelaySeconds: 2,
					PeriodSeconds:       1,
					FailureThreshold:    60,
				},
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot:             &runAsNonRoot,
					AllowPrivilegeEscalation: &allowPrivilegeEscalation,
				},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "browser-shm", MountPath: "/dev/shm"},
					{Name: "browser-tmp", MountPath: "/tmp"},
				},
			}},
			Volumes: []corev1.Volume{
				{
					Name: "browser-shm",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium:    corev1.StorageMediumMemory,
						SizeLimit: &shmLimit,
					}},
				},
				{
					Name: "browser-tmp",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
						SizeLimit: &tmpLimit,
					}},
				},
			},
		},
	}
}

// The development channel deliberately uses the mutable :browser tag. Always
// pull that tag so a node cannot silently reuse a stale browser runtime after a
// gateway rollout. Releases pass a versioned browser-vX.Y.Z tag and can safely
// reuse the node cache.
func browserImagePullPolicy(image string) corev1.PullPolicy {
	ref := strings.TrimSpace(image)
	if strings.Contains(ref, "@") {
		return corev1.PullIfNotPresent
	}

	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	if lastColon <= lastSlash {
		return corev1.PullAlways
	}
	switch ref[lastColon+1:] {
	case "browser", "latest":
		return corev1.PullAlways
	default:
		return corev1.PullIfNotPresent
	}
}

func (r *kubernetesRuntime) waitForPodReady(ctx context.Context, name string, timeout time.Duration) (*corev1.Pod, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pod, err := r.client.CoreV1().Pods(r.cfg.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("browserpool: get pod %s before readiness watch: %w", name, err)
	}
	if pod.Status.Phase == corev1.PodRunning && allContainersReady(pod) {
		return pod, nil
	}
	if pod.Status.Phase == corev1.PodFailed {
		return nil, fmt.Errorf("browserpool: pod %s entered Failed phase", name)
	}

	watcher, err := r.client.CoreV1().Pods(r.cfg.Namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector:   "metadata.name=" + name,
		ResourceVersion: pod.ResourceVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("browserpool: watch pod %s: %w", name, err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("browserpool: timed out waiting for pod %s to become ready: %w", name, ctx.Err())
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return nil, fmt.Errorf("browserpool: watch channel closed for pod %s", name)
			}
			if event.Type == watch.Deleted {
				return nil, fmt.Errorf("browserpool: pod %s was deleted before becoming ready", name)
			}
			if event.Type == watch.Error {
				return nil, fmt.Errorf("browserpool: watch pod %s: %w", name, apierrors.FromObject(event.Object))
			}
			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			if pod.Status.Phase == corev1.PodFailed {
				return nil, fmt.Errorf("browserpool: pod %s entered Failed phase", name)
			}
			if pod.Status.Phase == corev1.PodRunning && allContainersReady(pod) {
				return pod, nil
			}
		}
	}
}

func allContainersReady(pod *corev1.Pod) bool {
	if len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, status := range pod.Status.ContainerStatuses {
		if !status.Ready {
			return false
		}
	}
	return true
}

func (r *kubernetesRuntime) podURLs(ctx context.Context, pod *corev1.Pod, settings browserSettings) (string, string, error) {
	if !r.outOfCluster {
		// The in-cluster ServiceAccount intentionally has no port-forward RBAC.
		// Direct pod IP routing is both authorized and less fragile than SPDY.
		if pod.Status.PodIP == "" {
			return "", "", fmt.Errorf("browserpool: pod %s has no pod IP", pod.Name)
		}
		cdpURL := "http://" + net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(settings.cdpPort))
		streamURL := ""
		if !settings.headless {
			streamURL = "ws://" + net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(settings.streamPort)) + "/ws"
		}
		return cdpURL, streamURL, nil
	}

	localCDPPort, err := r.portForwarding.start(ctx, pod.Name, settings.cdpPort)
	if err != nil {
		return "", "", fmt.Errorf("browserpool: start CDP port-forward for pod %s: %w", pod.Name, err)
	}
	cdpURL := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(localCDPPort))
	streamURL := ""
	if !settings.headless {
		localStreamPort, err := r.portForwarding.start(ctx, pod.Name, settings.streamPort)
		if err != nil {
			return "", "", fmt.Errorf("browserpool: start stream port-forward for pod %s: %w", pod.Name, err)
		}
		streamURL = "ws://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(localStreamPort)) + "/ws"
	}
	return cdpURL, streamURL, nil
}

func waitForCDP(ctx context.Context, cdpBaseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cdpBaseURL+"/json/version", nil)
		if err != nil {
			return fmt.Errorf("browserpool: create CDP readiness request: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("browserpool: unexpected HTTP status %s", resp.Status)
		} else {
			lastErr = err
		}

		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("browserpool: CDP at %s did not become ready: %w", cdpBaseURL, errorsJoinContext(lastErr, ctx.Err()))
		case <-timer.C:
		}
	}
}

func errorsJoinContext(lastErr, contextErr error) error {
	if lastErr == nil {
		return fmt.Errorf("browserpool: CDP readiness context: %w", contextErr)
	}
	return fmt.Errorf("browserpool: CDP readiness failed: %w", errors.Join(lastErr, contextErr))
}

func (r *kubernetesRuntime) prepare(ctx context.Context, pod *managedPod, sessionID string) error {
	if err := r.setSession(ctx, pod.name, pod.tenantID, sessionID); err != nil {
		return err
	}
	if r.outOfCluster {
		localCDPPort, err := r.portForwarding.start(ctx, pod.name, pod.settings.cdpPort)
		if err != nil {
			return fmt.Errorf("browserpool: restart CDP port-forward for pod %s: %w", pod.name, err)
		}
		pod.cdpBaseURL = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(localCDPPort))
		if !pod.settings.headless {
			localStreamPort, err := r.portForwarding.start(ctx, pod.name, pod.settings.streamPort)
			if err != nil {
				return fmt.Errorf("browserpool: restart stream port-forward for pod %s: %w", pod.name, err)
			}
			pod.streamURL = "ws://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(localStreamPort)) + "/ws"
		}
	}
	if err := waitForCDP(ctx, pod.cdpBaseURL); err != nil {
		return fmt.Errorf("browserpool: confirm CDP readiness for reused pod %s: %w", pod.name, err)
	}
	return nil
}

func (r *kubernetesRuntime) setSession(ctx context.Context, podName, tenantID, sessionID string) error {
	pod, err := r.client.CoreV1().Pods(r.cfg.Namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("browserpool: get pod %s before session label update: %w", podName, err)
	}
	tenantLabel := r.cfg.LabelPrefix + "/tenant-id"
	if pod.Labels[tenantLabel] != tenantID {
		return fmt.Errorf("browserpool: pod %s tenant label is %q, expected %q", podName, pod.Labels[tenantLabel], tenantID)
	}
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	pod.Labels[r.cfg.LabelPrefix+"/session-id"] = sessionID
	if _, err := r.client.CoreV1().Pods(r.cfg.Namespace).Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("browserpool: update session label on pod %s: %w", podName, err)
	}
	return nil
}

func (r *kubernetesRuntime) reset(ctx context.Context, pod *managedPod) error {
	return resetDefaultBrowserContext(ctx, pod.cdpBaseURL)
}

func (r *kubernetesRuntime) refreshLease(ctx context.Context, podName string, expiresAt time.Time) error {
	patch, err := leaseExpiryPatch(r.cfg.LabelPrefix, expiresAt)
	if err != nil {
		return fmt.Errorf("browserpool: marshal lease patch for pod %s: %w", podName, err)
	}
	// A merge patch changes only the heartbeat annotation, so it does not
	// conflict with concurrent metadata changes made by another API client.
	if _, err := r.client.CoreV1().Pods(r.cfg.Namespace).Patch(ctx, podName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("browserpool: refresh lease on Kubernetes pod %s: %w", podName, err)
	}
	return nil
}

func leaseExpiryPatch(labelPrefix string, expiresAt time.Time) ([]byte, error) {
	return json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				labelPrefix + "/expires-at": expiresAt.Format(time.RFC3339),
			},
		},
	})
}

func (r *kubernetesRuntime) listManaged(ctx context.Context) ([]clusterPodLease, error) {
	managedByLabel := r.cfg.LabelPrefix + "/managed-by"
	pods, err := r.client.CoreV1().Pods(r.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set{managedByLabel: managedByValue}.AsSelector().String(),
	})
	if err != nil {
		return nil, fmt.Errorf("browserpool: list managed Kubernetes pods: %w", err)
	}
	expiresAtAnnotation := r.cfg.LabelPrefix + "/expires-at"
	result := make([]clusterPodLease, 0, len(pods.Items))
	for _, pod := range pods.Items {
		result = append(result, clusterPodLease{
			name:      pod.Name,
			expiresAt: pod.Annotations[expiresAtAnnotation],
		})
	}
	return result, nil
}

func (r *kubernetesRuntime) delete(ctx context.Context, podName string) error {
	r.portForwarding.stopPod(podName)
	err := r.client.CoreV1().Pods(r.cfg.Namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("browserpool: delete Kubernetes pod %s: %w", podName, err)
	}
	return nil
}

func (r *kubernetesRuntime) close() error {
	r.portForwarding.stopAll()
	return nil
}

func randomPodName() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("browserpool: generate pod name: %w", err)
	}
	for index := range random {
		random[index] = alphabet[int(random[index])%len(alphabet)]
	}
	return "evs-browser-" + string(random), nil
}

func copyLabels(labels map[string]string) map[string]string {
	copy := make(map[string]string, len(labels))
	for key, value := range labels {
		copy[key] = value
	}
	return copy
}
