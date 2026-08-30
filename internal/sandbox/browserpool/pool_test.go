package browserpool

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type recordingUsageRecorder struct {
	mu       sync.Mutex
	starts   []string
	finishes []string
	reasons  []string
	started  []time.Time
	ended    []time.Time
	startErr error
}

func (r *recordingUsageRecorder) Start(_ context.Context, lease *Lease, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, lease.SessionID)
	r.started = append(r.started, at)
	return r.startErr
}

func (r *recordingUsageRecorder) Heartbeat(context.Context, string, time.Time) error {
	return nil
}

func (r *recordingUsageRecorder) Finish(_ context.Context, sessionID string, at time.Time, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishes = append(r.finishes, sessionID)
	r.reasons = append(r.reasons, reason)
	r.ended = append(r.ended, at)
	return nil
}

func TestSelectIdlePodNeverCrossesTenantBoundary(t *testing.T) {
	settings := browserSettings{headless: true, cdpPort: 9222, streamPort: 6080}
	pod := &managedPod{
		name:        "tenant-a-pod",
		tenantID:    "tenant-a",
		state:       podIdle,
		idleSince:   time.Now(),
		settings:    settings,
		labelValues: map[string]string{"everstack.browserpool/tenant-id": "tenant-a"},
	}

	got := selectIdlePod(
		map[string]*managedPod{pod.name: pod},
		"everstack.browserpool/tenant-id",
		"tenant-b",
		settings,
	)
	if got != nil {
		t.Fatalf("tenant B received tenant A pod %q", got.name)
	}
}

func TestExistingSessionLeaseNeverCrossesTenantBoundary(t *testing.T) {
	runtime := newFakeRuntime(fake.NewSimpleClientset())
	p := newPoolWithRuntime(testConfig(), runtime)
	t.Cleanup(func() { _ = p.Close() })

	if _, err := p.EnsureBrowser(context.Background(), "shared-session", "tenant-a", sandbox.BrowserConfig{Headless: true}); err != nil {
		t.Fatalf("lease tenant A browser: %v", err)
	}
	lease, err := p.EnsureBrowser(context.Background(), "shared-session", "tenant-b", sandbox.BrowserConfig{Headless: true})
	if err == nil {
		t.Fatal("expected cross-tenant session reuse to fail")
	}
	if lease != nil {
		t.Fatalf("tenant B received tenant A lease: %#v", lease)
	}
	if got := runtime.provisionCount.Load(); got != 1 {
		t.Fatalf("expected one provision, got %d", got)
	}
}

func TestLeaseLifecycleRecordsBillableWindow(t *testing.T) {
	runtime := newFakeRuntime(fake.NewSimpleClientset())
	recorder := &recordingUsageRecorder{}
	p := newPoolWithRuntime(testConfig(), runtime)
	p.SetUsageRecorder(recorder, true)
	t.Cleanup(func() { _ = p.Close() })

	if _, err := p.EnsureBrowser(context.Background(), "session-metered", "tenant-a", sandbox.BrowserConfig{Headless: true}); err != nil {
		t.Fatalf("ensure browser: %v", err)
	}
	if _, ok := p.LeaseForSession("session-metered", "tenant-b"); ok {
		t.Fatal("foreign tenant resolved browser lease")
	}
	if _, ok := p.LeaseForSession("session-metered", "tenant-a"); !ok {
		t.Fatal("owning tenant could not resolve browser lease")
	}
	if err := p.Release(context.Background(), "session-metered"); err != nil {
		t.Fatalf("release browser: %v", err)
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.starts) != 1 || recorder.starts[0] != "session-metered" {
		t.Fatalf("usage starts = %#v", recorder.starts)
	}
	if len(recorder.finishes) != 1 || recorder.finishes[0] != "session-metered" {
		t.Fatalf("usage finishes = %#v", recorder.finishes)
	}
}

func TestRequiredUsageRecorderFailsClosed(t *testing.T) {
	runtime := newFakeRuntime(fake.NewSimpleClientset())
	p := newPoolWithRuntime(testConfig(), runtime)
	p.SetUsageRecorder(nil, true)
	t.Cleanup(func() { _ = p.Close() })

	if _, err := p.EnsureBrowser(context.Background(), "session-unmetered", "tenant-a", sandbox.BrowserConfig{Headless: true}); err == nil {
		t.Fatal("expected unmetered browser lease to fail closed")
	}
}

func TestTenantBrowserConcurrencyLimitIsAtomic(t *testing.T) {
	runtime := newFakeRuntime(fake.NewSimpleClientset())
	p := newPoolWithRuntime(testConfig(), runtime)
	p.SetLimitsResolver(func(context.Context, string) (TenantLimits, error) {
		return TenantLimits{MaxConcurrent: 1, MaxSession: time.Hour}, nil
	}, true)
	t.Cleanup(func() { _ = p.Close() })

	if _, err := p.EnsureBrowser(context.Background(), "session-a", "tenant-a", sandbox.BrowserConfig{Headless: true}); err != nil {
		t.Fatalf("first browser lease: %v", err)
	}
	if _, err := p.EnsureBrowser(context.Background(), "session-b", "tenant-a", sandbox.BrowserConfig{Headless: true}); err == nil {
		t.Fatal("second browser lease exceeded tenant concurrency limit")
	}
	if _, err := p.EnsureBrowser(context.Background(), "session-c", "tenant-b", sandbox.BrowserConfig{Headless: true}); err != nil {
		t.Fatalf("other tenant browser lease: %v", err)
	}
}

func TestBrowserSessionLimitReleasesAndFinalizesUsage(t *testing.T) {
	runtime := newFakeRuntime(fake.NewSimpleClientset())
	recorder := &recordingUsageRecorder{}
	p := newPoolWithRuntime(testConfig(), runtime)
	p.SetUsageRecorder(recorder, true)
	p.SetLimitsResolver(func(context.Context, string) (TenantLimits, error) {
		return TenantLimits{MaxConcurrent: 1, MaxSession: time.Second}, nil
	}, true)
	t.Cleanup(func() { _ = p.Close() })

	if _, err := p.EnsureBrowser(context.Background(), "session-expiring", "tenant-a", sandbox.BrowserConfig{Headless: true}); err != nil {
		t.Fatalf("ensure browser: %v", err)
	}
	p.reap(time.Now().Add(2 * time.Second))

	if _, ok := p.LeaseForSession("session-expiring", "tenant-a"); ok {
		t.Fatal("expired browser session remained leased")
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.finishes) != 1 || recorder.reasons[0] != "session_limit" {
		t.Fatalf("usage finalizations = %#v reasons = %#v", recorder.finishes, recorder.reasons)
	}
	if got := recorder.ended[0].Sub(recorder.started[0]); got != time.Second {
		t.Fatalf("metered session window = %s, want exact plan limit %s", got, time.Second)
	}
}

func TestRequiredBrowserLimitsFailClosed(t *testing.T) {
	p := newPoolWithRuntime(testConfig(), newFakeRuntime(fake.NewSimpleClientset()))
	p.SetLimitsResolver(nil, true)
	t.Cleanup(func() { _ = p.Close() })

	if _, err := p.EnsureBrowser(context.Background(), "session-a", "tenant-a", sandbox.BrowserConfig{Headless: true}); err == nil {
		t.Fatal("expected missing tenant browser limits to fail closed")
	}
}

func TestSessionLabelUpdateVerifiesCurrentTenantLabel(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      "tenant-a-pod",
		Namespace: "test",
		Labels: map[string]string{
			"everstack.browserpool/tenant-id":  "tenant-a",
			"everstack.browserpool/session-id": "",
		},
	}})
	runtime := newKubernetesRuntime(testConfig(), client, nil, false)

	err := runtime.setSession(context.Background(), "tenant-a-pod", "tenant-b", "session-b")
	if err == nil {
		t.Fatal("expected tenant label mismatch to fail")
	}
	pod, getErr := client.CoreV1().Pods("test").Get(context.Background(), "tenant-a-pod", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("get pod: %v", getErr)
	}
	if got := pod.Labels["everstack.browserpool/session-id"]; got != "" {
		t.Fatalf("cross-tenant update changed session label to %q", got)
	}
}

func TestBrowserPodStartsWithLeaseExpiry(t *testing.T) {
	cfg := testConfig()
	runtime := newKubernetesRuntime(cfg, fake.NewSimpleClientset(), nil, false)
	before := time.Now().Add(cfg.LeaseTTL)

	pod := runtime.browserPod("browser-pod", "tenant-a", "session-a", browserSettings{})

	if got := pod.Labels[cfg.LabelPrefix+"/managed-by"]; got != managedByValue {
		t.Fatalf("managed-by label = %q, want %q", got, managedByValue)
	}
	expiresAt, err := time.Parse(time.RFC3339, pod.Annotations[cfg.LabelPrefix+"/expires-at"])
	if err != nil {
		t.Fatalf("parse expires-at annotation: %v", err)
	}
	after := time.Now().Add(cfg.LeaseTTL)
	if expiresAt.Before(before.Truncate(time.Second)) || expiresAt.After(after) {
		t.Fatalf("expires-at = %s, want between %s and %s", expiresAt, before, after)
	}
	if got := pod.Spec.Containers[0].ImagePullPolicy; got != corev1.PullAlways {
		t.Fatalf("mutable browser image pull policy = %q, want %q", got, corev1.PullAlways)
	}
}

func TestBrowserImagePullPolicyPinsReleaseImages(t *testing.T) {
	tests := []struct {
		name  string
		image string
		want  corev1.PullPolicy
	}{
		{name: "mutable browser channel", image: "ghcr.io/everstacklabs/sandbox:browser", want: corev1.PullAlways},
		{name: "implicit latest", image: "ghcr.io/everstacklabs/sandbox", want: corev1.PullAlways},
		{name: "explicit latest", image: "ghcr.io/everstacklabs/sandbox:latest", want: corev1.PullAlways},
		{name: "versioned release", image: "ghcr.io/everstacklabs/sandbox:browser-v1.2.3", want: corev1.PullIfNotPresent},
		{name: "digest", image: "ghcr.io/everstacklabs/sandbox@sha256:abc123", want: corev1.PullIfNotPresent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := browserImagePullPolicy(tt.image); got != tt.want {
				t.Fatalf("browserImagePullPolicy(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
	}
}

func TestProvisioningPodLeaseIsRefreshedUntilReady(t *testing.T) {
	cfg := testConfig()
	client := fake.NewSimpleClientset(testClusterPod(cfg, "provisioning-pod", time.Now().Add(-time.Minute).Format(time.RFC3339)))
	runtime := newKubernetesRuntime(cfg, client, nil, false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.runProvisioningHeartbeat(ctx, "provisioning-pod", time.Millisecond)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	deadline := time.After(time.Second)
	for {
		pod, err := client.CoreV1().Pods(cfg.Namespace).Get(context.Background(), "provisioning-pod", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get provisioning pod: %v", err)
		}
		expiresAt, err := time.Parse(time.RFC3339, pod.Annotations[cfg.LabelPrefix+"/expires-at"])
		if err == nil && expiresAt.After(time.Now()) {
			return
		}
		select {
		case <-deadline:
			t.Fatal("provisioning pod lease was not refreshed")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestEnsureBrowserRejectsEmptyTenantOrSession(t *testing.T) {
	p := newPoolWithRuntime(testConfig(), newFakeRuntime(fake.NewSimpleClientset()))
	t.Cleanup(func() { _ = p.Close() })

	tests := []struct {
		name      string
		sessionID string
		tenantID  string
	}{
		{name: "empty tenant", sessionID: "session-1"},
		{name: "empty session", tenantID: "tenant-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lease, err := p.EnsureBrowser(context.Background(), tt.sessionID, tt.tenantID, sandbox.BrowserConfig{})
			if err == nil {
				t.Fatal("expected an error")
			}
			if lease != nil {
				t.Fatalf("expected no lease, got %#v", lease)
			}
		})
	}
}

func TestIdleCapTrimmingPicksOldestPod(t *testing.T) {
	now := time.Now()
	pods := []*managedPod{
		{name: "newest", tenantID: "tenant-a", state: podIdle, idleSince: now.Add(-time.Minute)},
		{name: "oldest", tenantID: "tenant-a", state: podIdle, idleSince: now.Add(-3 * time.Minute)},
		{name: "middle", tenantID: "tenant-a", state: podIdle, idleSince: now.Add(-2 * time.Minute)},
	}

	got := idlePodsToTrim(pods, now, time.Hour, 2)
	if len(got) != 1 {
		t.Fatalf("expected one pod to trim, got %d", len(got))
	}
	if got[0].name != "oldest" {
		t.Fatalf("expected oldest pod to be trimmed, got %q", got[0].name)
	}
}

func TestWithDefaultsRejectsLeaseTTLNotGreaterThanReaperInterval(t *testing.T) {
	tests := []struct {
		name     string
		leaseTTL time.Duration
	}{
		{name: "negative", leaseTTL: -time.Second},
		{name: "less than interval", leaseTTL: reaperInterval - time.Second},
		{name: "equal to interval", leaseTTL: reaperInterval},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.LeaseTTL = tt.leaseTTL
			if _, err := cfg.withDefaults(); err == nil {
				t.Fatalf("expected LeaseTTL %s to be rejected", tt.leaseTTL)
			}
		})
	}
}

func TestWithDefaultsSetsLeaseTTL(t *testing.T) {
	cfg := testConfig()
	cfg.LeaseTTL = 0

	normalized, err := cfg.withDefaults()
	if err != nil {
		t.Fatalf("apply defaults: %v", err)
	}
	if normalized.LeaseTTL != defaultLeaseTTL {
		t.Fatalf("LeaseTTL = %s, want %s", normalized.LeaseTTL, defaultLeaseTTL)
	}
}

func TestReaperDeletesLapsedOrphanPod(t *testing.T) {
	now := time.Now()
	cfg := testConfig()
	client := fake.NewSimpleClientset(testClusterPod(cfg, "lapsed-orphan", now.Add(-time.Minute).Format(time.RFC3339)))
	p := newPoolWithRuntime(cfg, newKubernetesRuntime(cfg, client, nil, false))
	t.Cleanup(func() { _ = p.Close() })

	p.reap(now)

	_, err := client.CoreV1().Pods(cfg.Namespace).Get(context.Background(), "lapsed-orphan", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected lapsed orphan to be deleted, got %v", err)
	}
}

func TestReaperKeepsAnotherReplicasLivePod(t *testing.T) {
	now := time.Now()
	cfg := testConfig()
	client := fake.NewSimpleClientset(testClusterPod(cfg, "live-remote-pod", now.Add(time.Minute).Format(time.RFC3339)))
	p := newPoolWithRuntime(cfg, newKubernetesRuntime(cfg, client, nil, false))
	t.Cleanup(func() { _ = p.Close() })

	p.reap(now)

	if _, err := client.CoreV1().Pods(cfg.Namespace).Get(context.Background(), "live-remote-pod", metav1.GetOptions{}); err != nil {
		t.Fatalf("expected another replica's live pod to remain: %v", err)
	}
}

func TestReaperNeverDeletesOwnPodWhenLeaseLooksStale(t *testing.T) {
	now := time.Now()
	cfg := testConfig()
	client := fake.NewSimpleClientset(testClusterPod(cfg, "own-stale-pod", now.Add(-time.Minute).Format(time.RFC3339)))
	client.PrependReactor("patch", "pods", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, errors.New("transient patch failure")
	})
	p := newPoolWithRuntime(cfg, newKubernetesRuntime(cfg, client, nil, false))
	t.Cleanup(func() { _ = p.Close() })
	p.mu.Lock()
	p.pods["own-stale-pod"] = &managedPod{name: "own-stale-pod", state: podBound}
	p.mu.Unlock()

	p.reap(now)

	if _, err := client.CoreV1().Pods(cfg.Namespace).Get(context.Background(), "own-stale-pod", metav1.GetOptions{}); err != nil {
		t.Fatalf("expected this pool's own pod to remain: %v", err)
	}
}

func TestReaperTreatsInvalidLeaseExpiryAsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt string
	}{
		{name: "missing"},
		{name: "unparseable", expiresAt: "not-a-time"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			podName := tt.name + "-expiry-pod"
			client := fake.NewSimpleClientset(testClusterPod(cfg, podName, tt.expiresAt))
			p := newPoolWithRuntime(cfg, newKubernetesRuntime(cfg, client, nil, false))
			t.Cleanup(func() { _ = p.Close() })

			p.reap(time.Now())

			_, err := client.CoreV1().Pods(cfg.Namespace).Get(context.Background(), podName, metav1.GetOptions{})
			if !apierrors.IsNotFound(err) {
				t.Fatalf("expected pod with %s expiry to be deleted, got %v", tt.name, err)
			}
		})
	}
}

func TestReaperRefreshesLeaseExpiryForOwnedPods(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	cfg := testConfig()
	client := fake.NewSimpleClientset(testClusterPod(cfg, "owned-pod", now.Add(-time.Minute).Format(time.RFC3339)))
	p := newPoolWithRuntime(cfg, newKubernetesRuntime(cfg, client, nil, false))
	t.Cleanup(func() { _ = p.Close() })
	p.mu.Lock()
	p.pods["owned-pod"] = &managedPod{name: "owned-pod", state: podBound}
	p.mu.Unlock()

	p.reap(now)

	pod, err := client.CoreV1().Pods(cfg.Namespace).Get(context.Background(), "owned-pod", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get owned pod: %v", err)
	}
	want := now.Add(cfg.LeaseTTL).Format(time.RFC3339)
	if got := pod.Annotations[cfg.LabelPrefix+"/expires-at"]; got != want {
		t.Fatalf("expires-at = %q, want %q", got, want)
	}
}

func testClusterPod(cfg Config, name, expiresAt string) *corev1.Pod {
	annotations := map[string]string{}
	if expiresAt != "" {
		annotations[cfg.LabelPrefix+"/expires-at"] = expiresAt
	}
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:        name,
		Namespace:   cfg.Namespace,
		Annotations: annotations,
		Labels: map[string]string{
			cfg.LabelPrefix + "/managed-by": managedByValue,
		},
	}}
}

func TestMaxPodsTotalEnforcedDuringConcurrentEnsure(t *testing.T) {
	runtime := newFakeRuntime(fake.NewSimpleClientset())
	runtime.provisionStarted = make(chan struct{}, 1)
	runtime.allowProvision = make(chan struct{})
	cfg := testConfig()
	cfg.MaxPodsTotal = 1
	p := newPoolWithRuntime(cfg, runtime)
	t.Cleanup(func() { _ = p.Close() })

	firstResult := make(chan error, 1)
	go func() {
		_, err := p.EnsureBrowser(context.Background(), "session-1", "tenant-a", sandbox.BrowserConfig{Headless: true})
		firstResult <- err
	}()

	select {
	case <-runtime.provisionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first provision did not start")
	}

	_, err := p.EnsureBrowser(context.Background(), "session-2", "tenant-b", sandbox.BrowserConfig{Headless: true})
	if err == nil || !strings.Contains(err.Error(), "maximum pod capacity") {
		t.Fatalf("expected maximum pod capacity error, got %v", err)
	}

	close(runtime.allowProvision)
	if err := <-firstResult; err != nil {
		t.Fatalf("first ensure failed: %v", err)
	}
	if got := runtime.provisionCount.Load(); got != 1 {
		t.Fatalf("expected one provision, got %d", got)
	}
}

func TestFailedProvisionKeepsCapacityReservedUntilDeletion(t *testing.T) {
	runtime := newFakeRuntime(fake.NewSimpleClientset())
	runtime.provisionErr = errors.New("readiness failed")
	runtime.deleteStarted = make(chan struct{}, 1)
	runtime.allowDelete = make(chan struct{})
	cfg := testConfig()
	cfg.MaxPodsTotal = 1
	p := newPoolWithRuntime(cfg, runtime)
	t.Cleanup(func() { _ = p.Close() })

	firstResult := make(chan error, 1)
	go func() {
		_, err := p.EnsureBrowser(context.Background(), "session-1", "tenant-a", sandbox.BrowserConfig{Headless: true})
		firstResult <- err
	}()
	select {
	case <-runtime.deleteStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("failed provision did not start deletion")
	}

	_, err := p.EnsureBrowser(context.Background(), "session-2", "tenant-b", sandbox.BrowserConfig{Headless: true})
	if err == nil || !strings.Contains(err.Error(), "maximum pod capacity") {
		t.Fatalf("expected quarantined pod to consume capacity, got %v", err)
	}
	close(runtime.allowDelete)
	if err := <-firstResult; err == nil {
		t.Fatal("expected first provision to fail")
	}
}

func TestReleaseUnknownSessionIsNoOp(t *testing.T) {
	p := newPoolWithRuntime(testConfig(), newFakeRuntime(fake.NewSimpleClientset()))
	t.Cleanup(func() { _ = p.Close() })

	if err := p.Release(context.Background(), "missing-session"); err != nil {
		t.Fatalf("release unknown session: %v", err)
	}
}

func TestReleaseDeletesPodWhenResetPanics(t *testing.T) {
	runtime := newFakeRuntime(fake.NewSimpleClientset())
	runtime.resetPanics = true
	recorder := &recordingUsageRecorder{}
	p := newPoolWithRuntime(testConfig(), runtime)
	p.SetUsageRecorder(recorder, true)
	t.Cleanup(func() { _ = p.Close() })

	lease, err := p.EnsureBrowser(context.Background(), "session-1", "tenant-a", sandbox.BrowserConfig{Headless: true})
	if err != nil {
		t.Fatalf("ensure browser: %v", err)
	}
	if err := p.Release(context.Background(), "session-1"); err == nil {
		t.Fatal("expected reset panic to fail release")
	}
	if _, err := runtime.client.CoreV1().Pods("test").Get(context.Background(), lease.PodName, metav1.GetOptions{}); err == nil {
		t.Fatalf("unsafe pod %q still exists after reset panic", lease.PodName)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.finishes) != 1 || recorder.finishes[0] != "session-1" {
		t.Fatalf("billing window was not finalized before failed reset: %#v", recorder.finishes)
	}
}

func testConfig() Config {
	return Config{
		Namespace:        "test",
		Image:            "test-browser:latest",
		IdleTTL:          time.Hour,
		LeaseTTL:         defaultLeaseTTL,
		MaxIdlePerTenant: 2,
		MaxPodsTotal:     50,
		LabelPrefix:      "everstack.browserpool",
	}
}

type fakeRuntime struct {
	client           kubernetes.Interface
	provisionStarted chan struct{}
	allowProvision   chan struct{}
	provisionCount   atomic.Int32
	provisionErr     error
	deleteStarted    chan struct{}
	allowDelete      chan struct{}
	resetPanics      bool
}

func newFakeRuntime(client kubernetes.Interface) *fakeRuntime {
	return &fakeRuntime{client: client}
}

func (r *fakeRuntime) provision(ctx context.Context, tenantID, sessionID string, settings browserSettings) (*managedPod, error) {
	r.provisionCount.Add(1)
	if r.provisionStarted != nil {
		r.provisionStarted <- struct{}{}
	}
	if r.allowProvision != nil {
		select {
		case <-r.allowProvision:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	name := "fake-pod-" + sessionID
	labels := map[string]string{
		"everstack.browserpool/tenant-id":  tenantID,
		"everstack.browserpool/session-id": sessionID,
		"everstack.browserpool/managed-by": managedByValue,
	}
	_, err := r.client.CoreV1().Pods("test").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test",
			Labels:    labels,
			Annotations: map[string]string{
				"everstack.browserpool/expires-at": time.Now().Add(defaultLeaseTTL).Format(time.RFC3339),
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	pod := &managedPod{
		name:        name,
		tenantID:    tenantID,
		sessionID:   sessionID,
		state:       podBound,
		settings:    settings,
		cdpBaseURL:  "http://127.0.0.1:9222",
		labelValues: labels,
	}
	return pod, r.provisionErr
}

func (r *fakeRuntime) prepare(context.Context, *managedPod, string) error       { return nil }
func (r *fakeRuntime) setSession(context.Context, string, string, string) error { return nil }
func (r *fakeRuntime) reset(context.Context, *managedPod) error {
	if r.resetPanics {
		panic("reset failed")
	}
	return nil
}

func (r *fakeRuntime) refreshLease(ctx context.Context, podName string, expiresAt time.Time) error {
	patch, err := leaseExpiryPatch("everstack.browserpool", expiresAt)
	if err != nil {
		return err
	}
	_, err = r.client.CoreV1().Pods("test").Patch(ctx, podName, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (r *fakeRuntime) listManaged(ctx context.Context) ([]clusterPodLease, error) {
	pods, err := r.client.CoreV1().Pods("test").List(ctx, metav1.ListOptions{
		LabelSelector: "everstack.browserpool/managed-by=" + managedByValue,
	})
	if err != nil {
		return nil, err
	}
	result := make([]clusterPodLease, 0, len(pods.Items))
	for _, pod := range pods.Items {
		result = append(result, clusterPodLease{
			name:      pod.Name,
			expiresAt: pod.Annotations["everstack.browserpool/expires-at"],
		})
	}
	return result, nil
}

func (r *fakeRuntime) delete(ctx context.Context, podName string) error {
	if r.deleteStarted != nil {
		r.deleteStarted <- struct{}{}
	}
	if r.allowDelete != nil {
		select {
		case <-r.allowDelete:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err := r.client.CoreV1().Pods("test").Delete(ctx, podName, metav1.DeleteOptions{})
	return err
}

func (r *fakeRuntime) close() error { return nil }
