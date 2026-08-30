package browserpool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
)

type browserRuntime interface {
	provision(context.Context, string, string, browserSettings) (*managedPod, error)
	prepare(context.Context, *managedPod, string) error
	setSession(context.Context, string, string, string) error
	reset(context.Context, *managedPod) error
	refreshLease(context.Context, string, time.Time) error
	listManaged(context.Context) ([]clusterPodLease, error)
	delete(context.Context, string) error
	close() error
}

type clusterPodLease struct {
	name      string
	expiresAt string
}

type ownedPodHeartbeat struct {
	name     string
	tenantID string
}

type expiredSession struct {
	sessionID string
	endedAt   time.Time
}

type ensureCall struct {
	done     chan struct{}
	tenantID string
	lease    *Lease
	err      error
}

type releaseCall struct {
	done chan struct{}
	err  error
}

type Pool struct {
	cfg     Config
	runtime browserRuntime

	mu             sync.Mutex
	pods           map[string]*managedPod
	leases         map[string]*Lease
	deadlines      map[string]time.Time
	ensures        map[string]*ensureCall
	releases       map[string]*releaseCall
	pendingCreates int
	closed         bool
	operations     sync.WaitGroup

	lifetimeCtx context.Context
	cancel      context.CancelFunc
	reaperDone  sync.WaitGroup
	closeOnce   sync.Once
	closeErr    error

	usage         UsageRecorder
	requireUsage  bool
	limits        LimitsResolver
	requireLimits bool
}

func New(cfg Config) (*Pool, error) {
	normalized, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	restConfig, outOfCluster, err := resolveKubeconfig(normalized.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("browserpool: resolve Kubernetes config: %w", err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("browserpool: create Kubernetes client: %w", err)
	}
	return newPool(normalized, newKubernetesRuntime(normalized, client, restConfig, outOfCluster)), nil
}

func newPoolWithRuntime(cfg Config, runtime browserRuntime) *Pool {
	normalized, err := cfg.withDefaults()
	if err != nil {
		panic(err)
	}
	return newPool(normalized, runtime)
}

func newPool(cfg Config, runtime browserRuntime) *Pool {
	lifetimeCtx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		cfg:         cfg,
		runtime:     runtime,
		pods:        make(map[string]*managedPod),
		leases:      make(map[string]*Lease),
		deadlines:   make(map[string]time.Time),
		ensures:     make(map[string]*ensureCall),
		releases:    make(map[string]*releaseCall),
		lifetimeCtx: lifetimeCtx,
		cancel:      cancel,
	}
	p.reaperDone.Add(1)
	go p.runReaper()
	return p
}

// SetUsageRecorder wires durable hosted-browser metering. When required is
// true, new leases fail closed if a billable window cannot be persisted.
// Call during startup before the pool accepts requests.
func (p *Pool) SetUsageRecorder(recorder UsageRecorder, required bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usage = recorder
	p.requireUsage = required
}

// SetLimitsResolver wires tenant-plan admission. When required is true,
// allocation fails closed if the plan cannot be resolved.
func (p *Pool) SetLimitsResolver(resolver LimitsResolver, required bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.limits = resolver
	p.requireLimits = required
}

// LeaseForSession returns a defensive copy for authenticated stream relays and
// operational inspection. Tenant identity is mandatory.
func (p *Pool) LeaseForSession(sessionID, tenantID string) (*Lease, bool) {
	if p == nil || sessionID == "" || tenantID == "" {
		return nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	lease := p.leases[sessionID]
	if lease == nil || lease.TenantID != tenantID {
		return nil, false
	}
	return cloneLease(lease), true
}

func (p *Pool) LeaseTTL() time.Duration {
	if p == nil {
		return 0
	}
	return p.cfg.LeaseTTL
}

func (p *Pool) EnsureBrowser(ctx context.Context, sessionID, tenantID string, bcfg sandbox.BrowserConfig) (*Lease, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("browserpool: tenantID must not be empty")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("browserpool: sessionID must not be empty")
	}
	settings, err := settingsFromConfig(bcfg)
	if err != nil {
		return nil, err
	}
	// Reconnecting an already-admitted session must not depend on a fresh
	// control-plane lookup. Admission, billing start, and its deadline were
	// established atomically when the lease was created.
	if lease, ok := p.LeaseForSession(sessionID, tenantID); ok {
		return lease, nil
	}
	limits, err := p.resolveTenantLimits(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if err := p.beginOperation(); err != nil {
		return nil, err
	}
	defer p.operations.Done()

	opCtx, cancel := p.operationContext(ctx)
	defer cancel()
	return p.ensureBrowser(opCtx, sessionID, tenantID, settings, limits)
}

func (p *Pool) ensureBrowser(
	ctx context.Context,
	sessionID, tenantID string,
	settings browserSettings,
	limits TenantLimits,
) (*Lease, error) {
	for {
		p.mu.Lock()
		if lease := p.leases[sessionID]; lease != nil {
			if lease.TenantID != tenantID {
				p.mu.Unlock()
				return nil, fmt.Errorf("browserpool: session %s is already leased by another tenant", sessionID)
			}
			p.mu.Unlock()
			return cloneLease(lease), nil
		}
		if release := p.releases[sessionID]; release != nil {
			done := release.done
			p.mu.Unlock()
			if err := waitForSignal(ctx, done); err != nil {
				return nil, fmt.Errorf("browserpool: wait for session release: %w", err)
			}
			continue
		}
		if call := p.ensures[sessionID]; call != nil {
			if call.tenantID != tenantID {
				p.mu.Unlock()
				return nil, fmt.Errorf("browserpool: session %s is being leased by another tenant", sessionID)
			}
			p.mu.Unlock()
			if err := waitForSignal(ctx, call.done); err != nil {
				return nil, fmt.Errorf("browserpool: wait for existing lease: %w", err)
			}
			return cloneLease(call.lease), call.err
		}
		if limits.MaxConcurrent >= 0 && p.tenantAllocationCountLocked(tenantID) >= limits.MaxConcurrent {
			p.mu.Unlock()
			return nil, fmt.Errorf(
				"browserpool: tenant %s reached concurrent browser limit %d",
				tenantID,
				limits.MaxConcurrent,
			)
		}

		call := &ensureCall{done: make(chan struct{}), tenantID: tenantID}
		p.ensures[sessionID] = call
		pod := selectIdlePod(p.pods, p.tenantLabel(), tenantID, settings)
		if pod != nil {
			pod.state = podBinding
			pod.sessionID = sessionID
			p.mu.Unlock()
			return p.bindIdlePod(ctx, call, pod, sessionID, limits)
		}
		if len(p.pods)+p.pendingCreates >= p.cfg.MaxPodsTotal {
			p.mu.Unlock()
			err := fmt.Errorf("browserpool: maximum pod capacity %d reached", p.cfg.MaxPodsTotal)
			p.completeEnsure(sessionID, call, nil, err)
			return nil, err
		}
		p.pendingCreates++
		p.mu.Unlock()

		return p.provisionPod(ctx, call, sessionID, tenantID, settings, limits)
	}
}

func (p *Pool) bindIdlePod(
	ctx context.Context,
	call *ensureCall,
	pod *managedPod,
	sessionID string,
	limits TenantLimits,
) (*Lease, error) {
	if err := p.runtime.prepare(ctx, pod, sessionID); err != nil {
		deleteErr := p.quarantineAndDelete(pod)
		combined := errors.Join(err, deleteErr)
		resultErr := fmt.Errorf("browserpool: bind idle pod %s: %w", pod.name, combined)
		p.completeEnsure(sessionID, call, nil, resultErr)
		return nil, resultErr
	}

	lease := pod.lease(sessionID)
	startedAt := time.Now()
	if err := p.startUsage(ctx, lease, startedAt); err != nil {
		deleteErr := p.quarantineAndDelete(pod)
		resultErr := fmt.Errorf("browserpool: start usage for reused pod %s: %w", pod.name, errors.Join(err, deleteErr))
		p.completeEnsure(sessionID, call, nil, resultErr)
		return nil, resultErr
	}
	p.mu.Lock()
	if p.closed {
		pod.state = podDeleting
		p.mu.Unlock()
		usageErr := p.finishUsage(ctx, sessionID, time.Now(), "pool_closed")
		deleteErr := p.deleteTrackedPod(pod)
		err := fmt.Errorf("browserpool: pool is closed")
		if deleteErr != nil || usageErr != nil {
			err = fmt.Errorf("browserpool: close while binding pod: %w", errors.Join(err, deleteErr, usageErr))
		}
		p.completeEnsure(sessionID, call, nil, err)
		return nil, err
	}
	pod.state = podBound
	pod.labelValues[p.sessionLabel()] = sessionID
	p.leases[sessionID] = cloneLease(lease)
	p.setSessionDeadlineLocked(sessionID, startedAt, limits)
	p.mu.Unlock()

	logger.WithFields("tenant_id", pod.tenantID, "session_id", sessionID, "pod", pod.name).
		Info("browserpool: reused tenant browser pod")
	p.completeEnsure(sessionID, call, lease, nil)
	return cloneLease(lease), nil
}

func (p *Pool) provisionPod(
	ctx context.Context,
	call *ensureCall,
	sessionID, tenantID string,
	settings browserSettings,
	limits TenantLimits,
) (*Lease, error) {
	pod, err := p.runtime.provision(ctx, tenantID, sessionID, settings)
	if err != nil {
		p.mu.Lock()
		p.pendingCreates--
		if pod != nil {
			pod.state = podDeleting
			p.pods[pod.name] = pod
		}
		p.mu.Unlock()
		var deleteErr error
		if pod != nil {
			deleteErr = p.deleteTrackedPod(pod)
		}
		resultErr := fmt.Errorf("browserpool: provision browser pod: %w", errors.Join(err, deleteErr))
		p.completeEnsure(sessionID, call, nil, resultErr)
		return nil, resultErr
	}

	if pod.tenantID != tenantID || pod.labelValues[p.tenantLabel()] != tenantID {
		p.mu.Lock()
		p.pendingCreates--
		pod.state = podDeleting
		p.pods[pod.name] = pod
		p.mu.Unlock()
		deleteErr := p.deleteTrackedPod(pod)
		err := fmt.Errorf("browserpool: provisioned pod %s has mismatched tenant label", pod.name)
		if deleteErr != nil {
			err = fmt.Errorf("browserpool: reject mismatched tenant pod: %w", errors.Join(err, deleteErr))
		}
		p.completeEnsure(sessionID, call, nil, err)
		return nil, err
	}

	lease := pod.lease(sessionID)
	startedAt := time.Now()
	if err := p.startUsage(ctx, lease, startedAt); err != nil {
		p.mu.Lock()
		p.pendingCreates--
		pod.state = podDeleting
		p.pods[pod.name] = pod
		p.mu.Unlock()
		deleteErr := p.deleteTrackedPod(pod)
		resultErr := fmt.Errorf("browserpool: start usage for provisioned pod %s: %w", pod.name, errors.Join(err, deleteErr))
		p.completeEnsure(sessionID, call, nil, resultErr)
		return nil, resultErr
	}
	p.mu.Lock()
	p.pendingCreates--
	if p.closed {
		pod.state = podDeleting
		p.pods[pod.name] = pod
		p.mu.Unlock()
		usageErr := p.finishUsage(ctx, sessionID, time.Now(), "pool_closed")
		deleteErr := p.deleteTrackedPod(pod)
		err := fmt.Errorf("browserpool: pool is closed")
		if deleteErr != nil || usageErr != nil {
			err = fmt.Errorf("browserpool: close while provisioning pod: %w", errors.Join(err, deleteErr, usageErr))
		}
		p.completeEnsure(sessionID, call, nil, err)
		return nil, err
	}
	pod.state = podBound
	p.pods[pod.name] = pod
	p.leases[sessionID] = cloneLease(lease)
	p.setSessionDeadlineLocked(sessionID, startedAt, limits)
	p.mu.Unlock()

	logger.WithFields("tenant_id", tenantID, "session_id", sessionID, "pod", pod.name).
		Info("browserpool: browser pod ready")
	p.completeEnsure(sessionID, call, lease, nil)
	return cloneLease(lease), nil
}

func (p *Pool) completeEnsure(sessionID string, call *ensureCall, lease *Lease, err error) {
	p.mu.Lock()
	call.lease = cloneLease(lease)
	call.err = err
	delete(p.ensures, sessionID)
	close(call.done)
	p.mu.Unlock()
}

func (p *Pool) Release(ctx context.Context, sessionID string) error {
	return p.release(ctx, sessionID, "released", time.Time{})
}

func (p *Pool) release(ctx context.Context, sessionID, reason string, billableEnd time.Time) error {
	if err := p.beginOperation(); err != nil {
		return err
	}
	defer p.operations.Done()
	opCtx, cancel := p.operationContext(ctx)
	defer cancel()

	for {
		p.mu.Lock()
		if ensure := p.ensures[sessionID]; ensure != nil {
			done := ensure.done
			p.mu.Unlock()
			if err := waitForSignal(opCtx, done); err != nil {
				return fmt.Errorf("browserpool: wait for session lease: %w", err)
			}
			continue
		}
		if release := p.releases[sessionID]; release != nil {
			p.mu.Unlock()
			if err := waitForSignal(opCtx, release.done); err != nil {
				return fmt.Errorf("browserpool: wait for existing release: %w", err)
			}
			return release.err
		}
		lease := p.leases[sessionID]
		if lease == nil {
			p.mu.Unlock()
			return nil
		}
		pod := p.pods[lease.PodName]
		if pod == nil || pod.sessionID != sessionID || pod.state != podBound {
			delete(p.leases, sessionID)
			delete(p.deadlines, sessionID)
			p.mu.Unlock()
			return fmt.Errorf("browserpool: session %s has inconsistent pod state", sessionID)
		}
		call := &releaseCall{done: make(chan struct{})}
		p.releases[sessionID] = call
		delete(p.leases, sessionID)
		delete(p.deadlines, sessionID)
		pod.state = podResetting
		p.mu.Unlock()

		releasedAt := time.Now()
		if !billableEnd.IsZero() {
			releasedAt = billableEnd
		}
		// Stop the durable billing window before reset/delete I/O. Reset may
		// consume or cancel the caller's context; usage finalization must still
		// get its own bounded opportunity to commit the already-known end time.
		usageCtx, usageCancel := context.WithTimeout(context.WithoutCancel(opCtx), 10*time.Second)
		usageErr := p.finishUsage(usageCtx, sessionID, releasedAt, reason)
		usageCancel()
		releaseErr := p.releasePod(opCtx, pod)
		err := errors.Join(releaseErr, usageErr)
		p.completeRelease(sessionID, call, err)
		return err
	}
}

func (p *Pool) releasePod(ctx context.Context, pod *managedPod) error {
	resetErr := p.resetAndUnbind(ctx, pod)
	if resetErr != nil {
		// A reset failure is a possible cross-session state leak. Remove the pod
		// from selection before deleting it, even when deletion also fails.
		deleteErr := p.quarantineAndDelete(pod)
		logger.WithFields("tenant_id", pod.tenantID, "pod", pod.name, "error", resetErr.Error()).
			Warn("browserpool: reset failed, deleting pod")
		return fmt.Errorf("browserpool: reset pod %s: %w", pod.name, errors.Join(resetErr, deleteErr))
	}

	now := time.Now()
	p.mu.Lock()
	if p.closed {
		pod.state = podDeleting
	} else {
		pod.state = podIdle
		pod.sessionID = ""
		pod.idleSince = now
		pod.labelValues[p.sessionLabel()] = ""
	}
	toDelete := p.markIdlePodsForDeletionLocked(now)
	p.mu.Unlock()

	var deleteErrors []error
	for _, candidate := range toDelete {
		if err := p.deleteTrackedPod(candidate); err != nil {
			deleteErrors = append(deleteErrors, err)
		}
	}
	if len(deleteErrors) != 0 {
		return fmt.Errorf("browserpool: trim idle pods: %w", errors.Join(deleteErrors...))
	}

	logger.WithFields("tenant_id", pod.tenantID, "pod", pod.name).
		Info("browserpool: browser pod reset and idle")
	return nil
}

func (p *Pool) resetAndUnbind(ctx context.Context, pod *managedPod) (err error) {
	// A CDP client bug must not bypass fail-closed deletion on Release.
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("browserpool: reset panicked: %v", recovered)
		}
	}()
	err = p.runtime.reset(ctx, pod)
	if err == nil {
		err = p.runtime.setSession(ctx, pod.name, pod.tenantID, "")
	}
	return err
}

func (p *Pool) completeRelease(sessionID string, call *releaseCall, err error) {
	p.mu.Lock()
	call.err = err
	delete(p.releases, sessionID)
	close(call.done)
	p.mu.Unlock()
}

func (p *Pool) runReaper() {
	defer p.reaperDone.Done()
	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.lifetimeCtx.Done():
			return
		case now := <-ticker.C:
			p.reap(now)
		}
	}
}

func (p *Pool) reap(now time.Time) {
	p.mu.Lock()
	toDelete := p.markIdlePodsForDeletionLocked(now)
	ownedPodNames := make(map[string]struct{}, len(p.pods))
	toRefresh := make([]ownedPodHeartbeat, 0, len(p.pods))
	boundSessions := make([]string, 0, len(p.leases))
	expiredSessions := make([]expiredSession, 0)
	for _, pod := range p.pods {
		ownedPodNames[pod.name] = struct{}{}
		if pod.state != podDeleting {
			toRefresh = append(toRefresh, ownedPodHeartbeat{name: pod.name, tenantID: pod.tenantID})
		}
		if pod.state == podDeleting && !containsPod(toDelete, pod) {
			toDelete = append(toDelete, pod)
		}
	}
	for sessionID := range p.leases {
		if deadline, ok := p.deadlines[sessionID]; ok && !deadline.After(now) {
			expiredSessions = append(expiredSessions, expiredSession{
				sessionID: sessionID,
				endedAt:   deadline,
			})
		} else {
			boundSessions = append(boundSessions, sessionID)
		}
	}
	usageRecorder := p.usage
	p.mu.Unlock()

	for _, session := range expiredSessions {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := p.release(releaseCtx, session.sessionID, "session_limit", session.endedAt)
		cancel()
		if err != nil {
			logger.WithFields("session_id", session.sessionID, "error", err.Error()).
				Warn("browserpool: failed to release session at plan deadline")
		} else {
			logger.WithFields("session_id", session.sessionID).
				Info("browserpool: released session at plan deadline")
		}
	}

	// Each replica heartbeats only the pods in its own memory. This lets every
	// replica safely discover cluster pods without deleting another live owner's
	// pods, while a crashed owner's pods naturally stop receiving heartbeats.
	var refreshes sync.WaitGroup
	for _, pod := range toRefresh {
		refreshes.Add(1)
		go func(pod ownedPodHeartbeat) {
			defer refreshes.Done()
			ctx, cancel := context.WithTimeout(context.Background(), reaperInterval)
			defer cancel()
			if err := p.runtime.refreshLease(ctx, pod.name, now.Add(p.cfg.LeaseTTL)); err != nil {
				logger.WithFields("tenant_id", pod.tenantID, "pod", pod.name, "error", err.Error()).
					Warn("browserpool: reaper failed to refresh pod lease")
			}
		}(pod)
	}
	refreshes.Wait()

	if usageRecorder != nil {
		for _, sessionID := range boundSessions {
			if err := usageRecorder.Heartbeat(p.lifetimeCtx, sessionID, now); err != nil {
				logger.WithFields("session_id", sessionID, "error", err.Error()).
					Warn("browserpool: failed to heartbeat usage window")
			}
		}
	}

	for _, pod := range toDelete {
		if err := p.deleteTrackedPod(pod); err != nil {
			logger.WithFields("tenant_id", pod.tenantID, "pod", pod.name, "error", err.Error()).
				Warn("browserpool: reaper failed to delete pod")
		}
	}

	p.reapClusterOrphans(now, ownedPodNames)
}

func (p *Pool) reapClusterOrphans(now time.Time, ownedPodNames map[string]struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	clusterPods, err := p.runtime.listManaged(ctx)
	if err != nil {
		logger.WithFields("error", err.Error()).
			Warn("browserpool: reaper failed to list managed pods")
		return
	}

	for _, pod := range clusterPods {
		// A live replica owns every pod in its local map. Skipping those names
		// prevents a transient heartbeat failure from causing self-deletion.
		if _, owned := ownedPodNames[pod.name]; owned {
			continue
		}
		expiresAt, parseErr := time.Parse(time.RFC3339, pod.expiresAt)
		if parseErr == nil && !expiresAt.Before(now) {
			continue
		}
		if err := p.runtime.delete(ctx, pod.name); err != nil && !apierrors.IsNotFound(err) {
			logger.WithFields("pod", pod.name, "error", err.Error()).
				Warn("browserpool: reaper failed to delete expired cluster pod")
		}
	}
}

func (p *Pool) markIdlePodsForDeletionLocked(now time.Time) []*managedPod {
	pods := make([]*managedPod, 0, len(p.pods))
	for _, pod := range p.pods {
		pods = append(pods, pod)
	}
	selected := idlePodsToTrim(pods, now, p.cfg.IdleTTL, p.cfg.MaxIdlePerTenant)
	for _, pod := range selected {
		pod.state = podDeleting
	}
	return selected
}

func containsPod(pods []*managedPod, target *managedPod) bool {
	for _, pod := range pods {
		if pod == target {
			return true
		}
	}
	return false
}

func (p *Pool) deleteTrackedPod(pod *managedPod) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.runtime.delete(ctx, pod.name); err != nil {
		return fmt.Errorf("browserpool: delete pod %s: %w", pod.name, err)
	}
	p.mu.Lock()
	if p.pods[pod.name] == pod {
		delete(p.pods, pod.name)
	}
	p.mu.Unlock()
	logger.WithFields("tenant_id", pod.tenantID, "pod", pod.name).
		Info("browserpool: browser pod deleted")
	return nil
}

func (p *Pool) quarantineAndDelete(pod *managedPod) error {
	p.mu.Lock()
	pod.state = podDeleting
	p.pods[pod.name] = pod
	p.mu.Unlock()
	return p.deleteTrackedPod(pod)
}

func (p *Pool) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.cancel()
		p.mu.Unlock()

		p.operations.Wait()
		p.reaperDone.Wait()

		p.mu.Lock()
		pods := make([]*managedPod, 0, len(p.pods))
		sessionIDs := make([]string, 0, len(p.leases))
		for _, pod := range p.pods {
			pods = append(pods, pod)
		}
		for sessionID := range p.leases {
			sessionIDs = append(sessionIDs, sessionID)
		}
		p.mu.Unlock()

		var closeErrors []error
		closedAt := time.Now()
		for _, sessionID := range sessionIDs {
			if err := p.finishUsage(context.Background(), sessionID, closedAt, "pool_closed"); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		for _, pod := range pods {
			if err := p.deleteTrackedPod(pod); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		if err := p.runtime.close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("browserpool: close runtime: %w", err))
		}
		p.closeErr = errors.Join(closeErrors...)
	})
	return p.closeErr
}

func (p *Pool) startUsage(ctx context.Context, lease *Lease, at time.Time) error {
	p.mu.Lock()
	recorder := p.usage
	required := p.requireUsage
	p.mu.Unlock()
	if recorder == nil {
		if required {
			return fmt.Errorf("browserpool: usage recorder is required")
		}
		return nil
	}
	if err := recorder.Start(ctx, lease, at); err != nil {
		return fmt.Errorf("browserpool: persist usage start: %w", err)
	}
	return nil
}

func (p *Pool) finishUsage(ctx context.Context, sessionID string, at time.Time, reason string) error {
	p.mu.Lock()
	recorder := p.usage
	p.mu.Unlock()
	if recorder == nil {
		return nil
	}
	if err := recorder.Finish(ctx, sessionID, at, reason); err != nil {
		return fmt.Errorf("browserpool: persist usage finish: %w", err)
	}
	return nil
}

func (p *Pool) resolveTenantLimits(ctx context.Context, tenantID string) (TenantLimits, error) {
	p.mu.Lock()
	resolver := p.limits
	required := p.requireLimits
	p.mu.Unlock()
	if resolver == nil {
		if required {
			return TenantLimits{}, fmt.Errorf("browserpool: tenant limits resolver is required")
		}
		return unlimitedTenantLimits, nil
	}
	limits, err := resolver(ctx, tenantID)
	if err != nil {
		return TenantLimits{}, fmt.Errorf("browserpool: resolve tenant limits: %w", err)
	}
	if err := limits.Validate(); err != nil {
		return TenantLimits{}, fmt.Errorf("browserpool: invalid tenant limits: %w", err)
	}
	return limits, nil
}

func (p *Pool) tenantAllocationCountLocked(tenantID string) int {
	count := 0
	for _, lease := range p.leases {
		if lease != nil && lease.TenantID == tenantID {
			count++
		}
	}
	for _, ensure := range p.ensures {
		if ensure != nil && ensure.tenantID == tenantID {
			count++
		}
	}
	return count
}

func (p *Pool) setSessionDeadlineLocked(sessionID string, startedAt time.Time, limits TenantLimits) {
	if limits.MaxSession > 0 {
		p.deadlines[sessionID] = startedAt.Add(limits.MaxSession)
		return
	}
	delete(p.deadlines, sessionID)
}

func (p *Pool) beginOperation() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("browserpool: pool is closed")
	}
	p.operations.Add(1)
	return nil
}

func (p *Pool) operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(p.lifetimeCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (p *Pool) tenantLabel() string {
	return p.cfg.LabelPrefix + "/tenant-id"
}

func (p *Pool) sessionLabel() string {
	return p.cfg.LabelPrefix + "/session-id"
}

func waitForSignal(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
