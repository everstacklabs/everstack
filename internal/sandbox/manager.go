package sandbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/edition"
	"github.com/everstacklabs/everstack/internal/github"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/semaphore"
)

// ErrSandboxBillingRequired is returned before compute allocation when a
// managed tenant has exhausted its starter credit without active billing. A
// nil resolver keeps Community Edition and local development independent of
// central billing.
var ErrSandboxBillingRequired = errors.New("sandbox starter credit is exhausted; add or restore billing to continue compute")

// ErrConcurrentSandboxLimit is returned before allocating compute when the
// current instance has used every concurrent sandbox slot in its plan.
var ErrConcurrentSandboxLimit = errors.New("concurrent sandbox limit reached")

// SandboxManager is a process-level registry mapping sessions to sandbox instances.
// Analogous to runtime.SessionManager for agents.
type SandboxManager struct {
	backend            Backend
	globalConfig       GlobalSandboxConfig
	instances          map[string]*Instance // sessionID → Instance
	instancesBySandbox map[string]*Instance // sandboxID → Instance
	mu                 sync.RWMutex
	reaper             *Reaper
	lastUsedFlushStop  context.CancelFunc // stops the standalone last_used_at flusher
	lastTouchWrite     sync.Map           // sandboxID -> time.Time of last write-through
	db                 *sqlx.DB           // optional; when set, instances are persisted to the DB
	retentionResolver  RetentionResolver
	maxPortsPerSandbox int // max exposed ports per sandbox; 0 = unlimited
	lazyGitClone       bool
	lazyGitClonePct    int
	repoLocksMu        sync.Mutex
	repoLocks          map[string]*sync.Mutex

	// Buffered event writer (replaces per-call goroutines).
	eventCh     chan eventEnvelope
	eventCancel context.CancelFunc
	eventDone   chan struct{}

	// GitHub App client for git operations (clone repos). Set via SetGitHubApp().
	githubApp *github.App

	// createSem limits concurrent sandbox creations to prevent system overload.
	createSem *semaphore.Weighted

	// Persistent trooper tracking: agentID → Instance.
	// Multiple sessions can share one trooper.
	troopers             map[string]*Instance
	trooperLimitResolver TrooperLimitResolver

	// tenantCapResolver returns per-tenant resource caps from
	// runtime_config. When set, clampToGlobalLimits clamps to the
	// minimum of (global cap, tenant cap) for each field. Returning
	// a zero GlobalSandboxConfig means "no per-tenant override". The
	// runtime_config service is plumbed through this function rather
	// than imported directly to avoid an import cycle.
	//
	// Stored via atomic.Pointer rather than guarded by m.mu so it can
	// be read from inside getOrCreateImpl (which already holds m.mu as
	// a writer). A previous version used m.mu.RLock here, producing a
	// deadlock — Go's sync.RWMutex is NOT re-entrant; a writer that
	// tries to RLock the same mutex blocks forever and the runtime
	// does not detect it.
	tenantCapResolver atomic.Pointer[func(tenantID string) GlobalSandboxConfig]

	// tenantTierResolver returns the plan tier ("free", "basic", "pro",
	// "enterprise") for a tenant. Used by recordUsageSnapshot to apply
	// the configured TierMultipliers to the billed cost — without this,
	// pro/enterprise customers are charged list price even though
	// SandboxPricingConfig.TierMultipliers promises a discount. Nil
	// resolver means "treat every tenant as free", which is the safe
	// no-op default for self-hosted deployments where tiers aren't a
	// concept.
	tenantTierResolver atomic.Pointer[func(tenantID string) string]

	// sandboxBillingResolver reports whether a managed tenant may allocate
	// paid sandbox compute. Nil means this is a self-hosted/local manager where
	// central Everstack billing does not apply.
	sandboxBillingResolver atomic.Pointer[func(tenantID string) bool]

	// managedMachineProfiles requires every newly allocated managed sandbox to
	// match the fixed, publicly priced resource tuples. It remains false for
	// self-hosted runtimes, where customers provide and pay for the compute.
	managedMachineProfiles atomic.Bool

	// registry is the shared cross-replica metadata store for sandbox
	// routing/recovery. Writes are best-effort (logged on failure, do
	// not fail the operation). Reads provide fast lookups before the
	// Postgres fallback. Defaults to a no-op LocalRegistry; replaced
	// with a RedisRegistry via SetRegistry when Redis is configured.
	registry Registry

	// r2Snapshots is the object-storage snapshot store (R2/S3/MinIO)
	// used for host-loss-survival. Mutex guards the pointer + the
	// scheduler's cancel func; the store implementations themselves
	// are safe for concurrent use. Defaults to a Disabled no-op; the
	// SetSnapshotStore call (during startup, when R2 is configured)
	// swaps in a real store and starts the periodic scheduler.
	r2Snapshots      snapshot.Store
	r2SnapshotCancel context.CancelFunc
	snapshotGCCancel context.CancelFunc
	r2SnapshotMu     sync.RWMutex
	// Backend discovery runs before SetDB. Legacy collision cleanup is deferred
	// until the DB is attached so an open billing window can be closed first.
	pendingLegacyCleanup []string
}

// NewManager creates a new SandboxManager with the given backend and global config.
func NewManager(backend Backend, config GlobalSandboxConfig) *SandboxManager {
	maxCreates := config.MaxConcurrentCreates
	if maxCreates <= 0 {
		maxCreates = 5
	}
	m := &SandboxManager{
		backend:            backend,
		globalConfig:       config,
		instances:          make(map[string]*Instance),
		instancesBySandbox: make(map[string]*Instance),
		troopers:           make(map[string]*Instance),
		lazyGitClone:       envBool("EVS_SANDBOX_GIT_LAZY_CLONE", true),
		lazyGitClonePct:    envPercent("EVS_SANDBOX_GIT_LAZY_CLONE_PERCENT", 100),
		repoLocks:          make(map[string]*sync.Mutex),
		eventCh:            make(chan eventEnvelope, 256),
		createSem:          semaphore.NewWeighted(int64(maxCreates)),
		registry:           NewLocalRegistry(),
	}

	m.restoreInstances()

	// The legacy reaper and the desired-state reconciler are mutually
	// exclusive lifecycle drivers. Running both raced: the reaper's
	// stuck-state pass force-failed rows the reconciler was still
	// converging, and its stop pass wrote 'stopped' while the
	// reconciler wrote 'sleeping'.
	if !ReconcilerEnabled() {
		m.reaper = NewReaper(m, 60*time.Second)
	} else {
		logger.Info("sandbox_manager: legacy reaper disabled; reconciler owns the lifecycle")
	}
	// The last_used_at flusher used to live inside the reaper sweep,
	// which silently broke idle detection when the reaper was disabled.
	// It runs unconditionally now.
	m.startLastUsedFlusher()
	m.startEventWriter()

	logger.WithFields(
		"backend", backend.Name(),
		"max_sandboxes", config.MaxSandboxes,
		"lazy_git_clone", m.lazyGitClone,
		"lazy_git_clone_pct", m.lazyGitClonePct,
	).
		Info("sandbox_manager: started")

	return m
}

// SetRegistry replaces the default no-op registry with a shared
// (typically Redis-backed) implementation. Safe to call once during
// startup before the manager handles traffic. Subsequent writes
// (create, status change, terminate) and reads (GetLinked fallback)
// will route through the new registry.
func (m *SandboxManager) SetRegistry(r Registry) {
	if r == nil {
		r = NewLocalRegistry()
	}
	m.registry = r
}

// registryPut writes an Instance projection to the shared registry on
// a short timeout so Redis hiccups never stall a sandbox lifecycle
// step. Failures are logged at debug level — Postgres remains the
// durable source of truth.
func (m *SandboxManager) registryPut(parent context.Context, inst *Instance) {
	if m.registry == nil || inst == nil || inst.ID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
	defer cancel()
	if err := m.registry.Put(ctx, EntryFromInstance(inst)); err != nil {
		logger.WithFields("sandbox_id", inst.ID, "error", err.Error()).
			Debug("sandbox_manager: registry put failed")
	}
}

// registryDelete drops the registry entry for a destroyed sandbox.
// Best-effort — Postgres row is the canonical terminated record.
func (m *SandboxManager) registryDelete(parent context.Context, sandboxID string) {
	if m.registry == nil || sandboxID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
	defer cancel()
	if err := m.registry.Delete(ctx, sandboxID); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Debug("sandbox_manager: registry delete failed")
	}
}

// registryLinkSession publishes an additional session_id → sandbox_id
// mapping. Used when a caller links to an existing sandbox.
func (m *SandboxManager) registryLinkSession(parent context.Context, sessionID, sandboxID string) {
	if m.registry == nil || sessionID == "" || sandboxID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
	defer cancel()
	if err := m.registry.LinkSession(ctx, sessionID, sandboxID); err != nil {
		logger.WithFields("session_id", sessionID, "sandbox_id", sandboxID, "error", err.Error()).
			Debug("sandbox_manager: registry link-session failed")
	}
}

// SetDB sets the database connection for persisting sandbox instance records.
// Also syncs any previously restored in-memory instances to the DB and
// proactively recreates keep-warm sandboxes that may have been lost.
func (m *SandboxManager) SetDB(db *sqlx.DB) {
	m.db = db
	m.hydrateRestoredInstanceMetadataFromDB()
	m.cleanupDeferredLegacyShadows()

	// Persist any instances that were restored from Docker labels before DB was set.
	// Skip pending placeholders — they are in-flight creation attempts that
	// haven't completed yet. Persisting them leaves stale "pending" rows in
	// the DB if the creation later fails.
	m.mu.RLock()
	instances := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		if inst.Status == StatusPending {
			continue
		}
		instances = append(instances, inst)
	}
	m.mu.RUnlock()

	for _, inst := range instances {
		m.persistInstance(inst)
	}

	// Restore in-memory TCP proxies for port mappings whose sandboxes survived
	// the restart. Close any that belong to sandboxes that are gone.
	m.restoreOrCloseStalePortMappings()

	m.warmInstanceCache()

	// Warm-on-boot: proactively recreate keep-warm sandboxes in the background.
	go m.warmKeepAliveSandboxes()

	// Pre-pull configured warm images so the first sandbox create does not
	// pay a cold image-pull cost. No-op for backends that do not implement
	// the ImageWarmer interface (Kubernetes, Firecracker).
	go m.warmImages()
}

// warmInstanceCache loads active sandbox instances from the database into
// the in-memory cache so the first shell reconnect after a gateway restart
// doesn't fall through to a slow DB lookup with a 3-second timeout.
func (m *SandboxManager) warmInstanceCache() {
	if m.db == nil {
		return
	}
	type row struct {
		ID             string         `db:"id"`
		SessionID      string         `db:"session_id"`
		InstanceID     sql.NullString `db:"instance_id"`
		ContainerID    sql.NullString `db:"container_id"`
		Backend        string         `db:"backend"`
		Status         string         `db:"status"`
		LifecycleState sql.NullString `db:"lifecycle_state"`
		CreatedAt      time.Time      `db:"created_at"`
		BillingStarted sql.NullTime   `db:"billing_started_at"`
		BillingEnded   sql.NullTime   `db:"billing_ended_at"`
		LastUsedAt     *time.Time     `db:"last_used_at"`
		Name           string         `db:"name"`
		AgentID        sql.NullString `db:"agent_id"`
		Persistent     sql.NullBool   `db:"persistent"`
		Config         []byte         `db:"config"`
		Image          string         `db:"image"`
		ShortCode      sql.NullString `db:"short_code"`
		AgentTarget    sql.NullString `db:"agent_target"`
	}
	var rows []row
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const q = `
			SELECT id, session_id, instance_id, container_id, backend, status, lifecycle_state,
			       created_at, billing_started_at, billing_ended_at, last_used_at, name, agent_id, persistent, config, image, short_code, agent_target
		FROM sandbox_instances
		WHERE status IN ('running', 'idle', 'stopped', 'sleeping')
		  AND COALESCE(lifecycle_state, '') NOT IN ('terminated', 'failed')
		  AND destroyed_at IS NULL`
	if err := m.db.SelectContext(ctx, &rows, q); err != nil {
		logger.WithFields("error", err.Error()).Warn("sandbox_manager: warmInstanceCache query failed")
		return
	}

	var seeded int
	toRoute := make([]*Instance, 0)
	m.mu.Lock()
	for _, r := range rows {
		if _, exists := m.instances[r.SessionID]; exists {
			continue
		}
		var cfg InstanceConfig
		if len(r.Config) > 0 {
			_ = json.Unmarshal(r.Config, &cfg)
		}
		if cfg.Image == "" {
			cfg.Image = r.Image
		}
		lastUsedAt := time.Now()
		if r.LastUsedAt != nil && !r.LastUsedAt.IsZero() {
			lastUsedAt = *r.LastUsedAt
		}
		inst := &Instance{
			ID:               r.ID,
			InstanceID:       r.InstanceID.String,
			ContainerID:      r.ContainerID.String,
			Backend:          r.Backend,
			Status:           statusForLifecycle(r.LifecycleState.String, Status(r.Status)),
			LifecycleState:   r.LifecycleState.String,
			CreatedAt:        r.CreatedAt,
			BillingStartedAt: nullTimeValue(r.BillingStarted),
			BillingEndedAt:   nullTimeValue(r.BillingEnded),
			LastUsedAt:       lastUsedAt,
			Config:           cfg,
			Name:             r.Name,
			AgentID:          r.AgentID.String,
			Persistent:       r.Persistent.Bool,
			ShortCode:        r.ShortCode.String,
			AgentTarget:      r.AgentTarget.String,
		}
		m.instances[r.SessionID] = inst
		m.instancesBySandbox[r.ID] = inst
		if inst.AgentTarget != "" {
			toRoute = append(toRoute, inst)
		}
		seeded++
	}
	m.mu.Unlock()

	for _, inst := range toRoute {
		m.seedRouteFromInstance(inst)
	}
	if seeded > 0 {
		logger.WithFields("count", seeded).Info("sandbox_manager: warmed instance cache from DB")
	}
}

// warmImages pre-pulls each entry in globalConfig.WarmImages. Runs serially
// to avoid saturating the docker daemon / registry; total time is bounded
// by a 10m context. Failures are logged, not fatal — the first user create
// will retry the pull on demand.
func (m *SandboxManager) warmImages() {
	images := m.globalConfig.WarmImages
	if len(images) == 0 {
		return
	}
	warmer, ok := m.backend.(ImageWarmer)
	if !ok {
		logger.WithFields("backend", m.backend.Name(), "image_count", len(images)).
			Debug("sandbox_manager: backend does not support image warming, skipping")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	start := time.Now()
	logger.WithFields("backend", m.backend.Name(), "image_count", len(images)).
		Info("sandbox_manager: warming images")
	for _, img := range images {
		if img == "" {
			continue
		}
		imgStart := time.Now()
		if err := warmer.EnsureImage(ctx, img); err != nil {
			logger.WithFields("image", img, "error", err.Error(), "duration_ms", time.Since(imgStart).Milliseconds()).
				Warn("sandbox_manager: warm image failed")
			continue
		}
		logger.WithFields("image", img, "duration_ms", time.Since(imgStart).Milliseconds()).
			Info("sandbox_manager: warm image ready")
	}
	logger.WithFields("total_duration_ms", time.Since(start).Milliseconds()).
		Info("sandbox_manager: image warming complete")
}

// warmKeepAliveSandboxes queries the DB for sandboxes with active webhook/cron
// triggers and ensures they are running. For Docker containers that survived
// restart, it just sets the KeepWarm flag. For lost containers/VMs, it
// recreates them in the background bounded by the creation semaphore.
func (m *SandboxManager) warmKeepAliveSandboxes() {
	if m.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	type triggerRow struct {
		ID        string          `db:"id"`
		SessionID string          `db:"session_id"`
		TenantID  string          `db:"tenant_id"`
		Config    json.RawMessage `db:"config"`
	}

	const q = `
		SELECT DISTINCT si.id, si.session_id, si.tenant_id, si.config
		FROM sandbox_instances si
		WHERE si.lifecycle_state IN ('running', 'stopped')
		  AND (si.agent_id IS NULL OR si.agent_id = '')
		  AND (
			EXISTS (SELECT 1 FROM sandbox_webhooks w WHERE w.sandbox_id = si.id AND w.enabled AND w.auto_recreate)
			OR EXISTS (SELECT 1 FROM sandbox_crons c WHERE c.sandbox_id = si.id AND c.enabled AND c.auto_recreate)
		  )`

	var rows []triggerRow
	if err := m.db.SelectContext(ctx, &rows, q); err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_manager: warm-on-boot query failed")
		return
	}

	if len(rows) == 0 {
		return
	}

	var needRecreate []triggerRow
	m.mu.Lock()
	for _, r := range rows {
		if inst, ok := m.instancesBySandbox[r.ID]; ok {
			// Container survived restart — just mark as keep-warm
			inst.KeepWarm = true
		} else {
			needRecreate = append(needRecreate, r)
		}
	}
	m.mu.Unlock()

	if len(needRecreate) == 0 {
		logger.WithFields("marked_warm", len(rows)).
			Info("sandbox_manager: warm-on-boot complete (all containers survived)")
		return
	}

	logger.WithFields("need_recreate", len(needRecreate), "survived", len(rows)-len(needRecreate)).
		Info("sandbox_manager: warm-on-boot recreating lost keep-warm sandboxes")

	for _, r := range needRecreate {
		r := r
		go func() {
			recreateCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			inst, err := m.GetOrRecreate(recreateCtx, r.SessionID, r.TenantID, r.Config)
			if err != nil {
				logger.WithFields("sandbox_id", r.ID, "session_id", r.SessionID, "error", err.Error()).
					Warn("sandbox_manager: warm-on-boot failed to recreate sandbox")
				return
			}
			logger.WithFields("sandbox_id", inst.ID, "session_id", r.SessionID).
				Info("sandbox_manager: warm-on-boot recreated sandbox")
		}()
	}
}

// hydrateRestoredInstanceMetadataFromDB fills missing in-memory metadata for
// backend-restored instances (for example Docker labels from older versions may
// not include sandbox name). This runs once when DB is wired.
func (m *SandboxManager) hydrateRestoredInstanceMetadataFromDB() {
	if m.db == nil {
		return
	}

	type row struct {
		Name             string     `db:"name"`
		BillingStartedAt *time.Time `db:"billing_started_at"`
		BillingEndedAt   *time.Time `db:"billing_ended_at"`
		LastUsedAt       *time.Time `db:"last_used_at"`
		IdleRetentionSec int        `db:"idle_retention_secs"`
		KeepWarm         bool       `db:"keep_warm"`
		AgentID          *string    `db:"agent_id"`
		Persistent       bool       `db:"persistent"`
	}

	const q = `
		SELECT COALESCE(name, '') AS name, billing_started_at, billing_ended_at, last_used_at, COALESCE(idle_retention_secs, 0) AS idle_retention_secs, COALESCE(keep_warm, false) AS keep_warm,
		       agent_id, COALESCE(persistent, false) AS persistent
		FROM sandbox_instances
		WHERE id = $1`

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, inst := range m.instances {
		if inst == nil || inst.ID == "" {
			continue
		}

		var r row
		queryCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		err := m.db.GetContext(queryCtx, &r, q, inst.ID)
		cancel()
		if err != nil {
			continue
		}

		if strings.TrimSpace(inst.Name) == "" && strings.TrimSpace(inst.Config.Name) != "" {
			inst.Name = strings.TrimSpace(inst.Config.Name)
		}
		if strings.TrimSpace(inst.Name) == "" && strings.TrimSpace(r.Name) != "" {
			inst.Name = strings.TrimSpace(r.Name)
		}
		if strings.TrimSpace(inst.Config.Name) == "" && strings.TrimSpace(inst.Name) != "" {
			inst.Config.Name = inst.Name
		}
		// Prefer the durable current-window boundary. A restored backend with
		// no boundary starts at the time we observed it running; CreatedAt is
		// identity metadata and must never become a retroactive billing clock.
		if r.BillingStartedAt != nil && !r.BillingStartedAt.IsZero() {
			inst.BillingStartedAt = r.BillingStartedAt.UTC()
		} else if inst.Status == StatusRunning && inst.BillingStartedAt.IsZero() {
			observedAt := time.Now().UTC()
			recoverCtx, recoverCancel := context.WithTimeout(context.Background(), time.Second)
			startedAt, recoverErr := m.openRecoveredBillingWindow(recoverCtx, inst.ID, observedAt)
			recoverCancel()
			if recoverErr != nil {
				logger.WithFields("sandbox_id", inst.ID, "error", recoverErr.Error()).
					Warn("sandbox_manager: restored running sandbox has no billable window")
			} else {
				inst.BillingStartedAt = startedAt
			}
		}
		if r.BillingEndedAt != nil && !r.BillingEndedAt.IsZero() {
			inst.BillingEndedAt = r.BillingEndedAt.UTC()
		}
		if inst.LastUsedAt.IsZero() && r.LastUsedAt != nil {
			inst.LastUsedAt = *r.LastUsedAt
		}
		if inst.IdleRetentionSecs == 0 && r.IdleRetentionSec > 0 {
			inst.IdleRetentionSecs = r.IdleRetentionSec
		}
		if r.KeepWarm {
			inst.KeepWarm = true
		}
		if r.Persistent {
			inst.Persistent = true
			if r.AgentID != nil && *r.AgentID != "" {
				inst.AgentID = *r.AgentID
				m.troopers[*r.AgentID] = inst
			}
		}
	}
}

// openRecoveredBillingWindow establishes a non-retroactive billing boundary
// for a backend resource discovered running without one. A concurrent writer's
// existing boundary wins; the sandbox identity's CreatedAt is never consulted.
func (m *SandboxManager) openRecoveredBillingWindow(ctx context.Context, sandboxID string, observedAt time.Time) (time.Time, error) {
	var startedAt time.Time
	err := m.db.GetContext(ctx, &startedAt, `
		UPDATE sandbox_instances
		SET billing_started_at = $2, billing_ended_at = NULL, updated_at = NOW()
		WHERE id = $1
		  AND billing_started_at IS NULL
		  AND destroyed_at IS NULL
		RETURNING billing_started_at`, sandboxID, observedAt.UTC())
	if err == nil {
		return startedAt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	if err := m.db.GetContext(ctx, &startedAt, `
		SELECT billing_started_at
		FROM sandbox_instances
		WHERE id = $1
		  AND billing_started_at IS NOT NULL
		  AND destroyed_at IS NULL`, sandboxID); err != nil {
		return time.Time{}, err
	}
	return startedAt, nil
}

// SetRetentionResolver sets the resolver used to determine idle retention
// duration per tenant. When nil the global default is used.
func (m *SandboxManager) SetRetentionResolver(r RetentionResolver) {
	m.retentionResolver = r
}

// SetTrooperLimitResolver sets the resolver for trooper plan limits.
func (m *SandboxManager) SetTrooperLimitResolver(r TrooperLimitResolver) {
	m.trooperLimitResolver = r
}

// IsTrooperFeatureEnabled returns true if the tenant has the persistent_troopers
// feature flag enabled in their license. When no resolver is wired (dev mode),
// defaults to true so developers can use troopers without a license service.
// Dev builds (-tags dev) bypass the feature gate; there is deliberately no env
// override (licensing backdoor, see docs/design/editions-and-billing.md D8).
func (m *SandboxManager) IsTrooperFeatureEnabled(tenantID string) bool {
	if m.trooperLimitResolver == nil {
		return true // dev mode: everything on
	}
	if edition.IsDev() {
		return true
	}
	return m.trooperLimitResolver.IsTrooperFeatureEnabled(tenantID)
}

// IsBrowserHeadedEnabled returns true if the tenant has the browser_headed
// feature flag enabled. When no resolver is wired (dev mode), defaults to
// false — headed mode requires explicit opt-in even in dev.
func (m *SandboxManager) IsBrowserHeadedEnabled(tenantID string) bool {
	if m.trooperLimitResolver == nil {
		return false // dev mode: headed browser off by default
	}
	return m.trooperLimitResolver.IsBrowserHeadedEnabled(tenantID)
}

// GetOrCreateTrooper returns the persistent trooper for an agent, creating
// or reviving it as needed. Multiple sessions share the same trooper.
func (m *SandboxManager) GetOrCreateTrooper(ctx context.Context, agentID, sessionID, tenantID string, config SandboxConfig) (*Instance, error) {
	if err := m.RequireSandboxBilling(tenantID); err != nil {
		return nil, err
	}
	// Phase 1: Read-lock check for existing trooper
	m.mu.RLock()
	if inst, ok := m.troopers[agentID]; ok {
		if inst.LifecycleState == LifecycleRunning || inst.Status == StatusRunning {
			sandboxID := inst.ID
			m.mu.RUnlock()
			m.seedRouteFromInstance(inst)

			// Verify the pod/container actually exists — the in-memory state
			// can be stale if the pod was OOM-killed, evicted, or deleted.
			if _, err := m.backend.Status(ctx, sandboxID); err != nil {
				logger.WithFields(
					"sandbox_id", sandboxID,
					"agent_id", agentID,
					"error", err.Error(),
				).Warn("sandbox_manager: trooper pod is gone unexpectedly")

				// Check if a snapshot exists from a previous stop cycle.
				// If so, mark as stopped and let the revive path handle it
				// to preserve as much workspace state as possible.
				var snapshotRef string
				if m.db != nil {
					_ = m.db.QueryRowxContext(ctx,
						`SELECT COALESCE(workspace_snapshot_ref, '') FROM sandbox_instances WHERE id = $1`,
						sandboxID).Scan(&snapshotRef)
				}
				if snapshotRef != "" {
					logger.WithFields("sandbox_id", sandboxID, "snapshot", snapshotRef).
						Info("sandbox_manager: found existing snapshot, marking stopped for revive")
					// Mark as stopped so revive path can pick it up
					if m.db != nil {
						m.db.ExecContext(ctx,
							`UPDATE sandbox_instances SET lifecycle_state = 'stopped', status = 'stopped', updated_at = NOW() WHERE id = $1`,
							sandboxID)
					}
					// Patch stored config with current browser sidecar setting
					// so the revived pod includes/excludes the sidecar correctly.
					if config.BrowserSidecar != nil {
						_ = m.PatchInstanceConfig(ctx, sandboxID, func(cfg *InstanceConfig) {
							cfg.BrowserSidecar = config.BrowserSidecar
						})
					}
					m.mu.Lock()
					inst.LifecycleState = LifecycleStopped
					inst.Status = StatusStopped
					m.mu.Unlock()
					// Revive from snapshot
					if revived, reviveErr := m.ReviveSandbox(ctx, sandboxID); reviveErr == nil {
						m.mu.Lock()
						m.instances[sessionID] = revived
						m.mu.Unlock()
						return revived, nil
					}
					logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
						Warn("sandbox_manager: revive from snapshot failed, falling through to recreation")
				}

				// No snapshot or revive failed — clean up and recreate.
				// This is the last resort and will lose workspace state.
				m.mu.Lock()
				delete(m.instances, sessionID)
				delete(m.instancesBySandbox, sandboxID)
				delete(m.troopers, agentID)
				m.mu.Unlock()

				if m.db != nil {
					m.db.ExecContext(ctx,
						`UPDATE sandbox_instances SET lifecycle_state = 'terminated', status = 'terminated', updated_at = NOW() WHERE id = $1`,
						sandboxID)
				}

				// Fall through to Phase 2 (creation)
			} else {
				// Pod is alive — register session and return
				m.mu.Lock()
				m.instances[sessionID] = inst
				m.mu.Unlock()
				m.touchLastUsed(sessionID)
				return inst, nil
			}
		} else if inst.LifecycleState == LifecycleStopped {
			// Stopped — try to revive from snapshot to preserve /workspace state.
			// If revive fails, fall through to recreation as a last resort.
			// The agent needs a working sandbox even if it means losing workspace state.
			sandboxID := inst.ID
			m.mu.RUnlock()
			// Patch stored config with current browser sidecar setting
			// so the revived pod includes/excludes the sidecar correctly.
			if config.BrowserSidecar != nil {
				_ = m.PatchInstanceConfig(ctx, sandboxID, func(cfg *InstanceConfig) {
					cfg.BrowserSidecar = config.BrowserSidecar
				})
			}
			if _, err := m.ReviveSandbox(ctx, sandboxID); err != nil {
				logger.WithFields(
					"sandbox_id", sandboxID,
					"agent_id", agentID,
					"error", err.Error(),
				).Warn("sandbox_manager: revive failed for persistent trooper, falling through to fresh creation")

				// Clean up the failed sandbox so we can recreate
				m.mu.Lock()
				delete(m.instances, sessionID)
				delete(m.instancesBySandbox, sandboxID)
				delete(m.troopers, agentID)
				m.mu.Unlock()

				if m.db != nil {
					m.db.ExecContext(ctx,
						`UPDATE sandbox_instances SET lifecycle_state = 'terminated', status = 'terminated', updated_at = NOW() WHERE id = $1`,
						sandboxID)
				}

				// Brief wait for backend cleanup
				time.Sleep(2 * time.Second)
				// Fall through to Phase 2 (creation)
			} else {
				m.mu.Lock()
				inst = m.troopers[agentID] // re-read after revive
				if inst != nil {
					m.instances[sessionID] = inst
				}
				m.mu.Unlock()
				if inst == nil {
					return nil, fmt.Errorf("trooper %s disappeared after revive", sandboxID)
				}
				return inst, nil
			}
		} else {
			m.mu.RUnlock()
		}
	} else {
		m.mu.RUnlock()
	}

	requestedSandboxID := fmt.Sprintf("wks_%s", agentID)
	if err := m.RequireConcurrentSandboxSlot(ctx, tenantID, requestedSandboxID); err != nil {
		return nil, err
	}

	// Phase 2: Write-lock for creation (double-check)
	m.mu.Lock()
	// Healing path: adopt an already-running canonical wks_<agentID> instance
	// even if legacy bugs left Persistent/AgentID unset. This prevents a
	// destructive recreate of the same pod ID that can wipe /workspace state.
	sandboxID := fmt.Sprintf("wks_%s", agentID)
	if existing, ok := m.instancesBySandbox[sandboxID]; ok && existing.Status == StatusRunning {
		// Verify the pod is actually alive before adopting
		m.seedRouteFromInstance(existing)
		if _, err := m.backend.Status(ctx, sandboxID); err == nil {
			existing.Persistent = true
			existing.AgentID = agentID
			existing.LifecycleState = LifecycleRunning
			m.instances[sessionID] = existing
			m.troopers[agentID] = existing
			m.mu.Unlock()
			m.persistInstance(existing)
			m.touchLastUsed(sessionID)
			return existing, nil
		}
		// Pod is gone — remove stale entry
		delete(m.instancesBySandbox, sandboxID)
	}
	if inst, ok := m.troopers[agentID]; ok && (inst.LifecycleState == LifecycleRunning || inst.Status == StatusRunning) {
		// Already verified in Phase 1 and cleaned up if dead — skip
		m.seedRouteFromInstance(inst)
		if _, err := m.backend.Status(ctx, inst.ID); err == nil {
			m.instances[sessionID] = inst
			m.mu.Unlock()
			return inst, nil
		}
		// Stale — clean up
		delete(m.troopers, agentID)
		delete(m.instancesBySandbox, inst.ID)
	}

	// Pod-restart fallback (issue #6): the in-memory caches above are
	// empty for any trooper this gateway pod didn't directly create.
	// Look the canonical wks_<agentID> row up in DB before falling
	// through to creation — otherwise every gateway restart would
	// duplicate every trooper. The actual VM lives on fcagent and
	// outlives the gateway pod; we just need to re-adopt it here.
	if m.db != nil {
		if dbInst := m.lookupTrooperByAgentIDFromDB(ctx, agentID); dbInst != nil {
			// Found a row. If it's running and the backend confirms
			// the VM is alive, adopt it. If it's in a non-terminal
			// non-running state (sleeping, etc.), drop the lock and
			// trigger a revive — same path as a Phase 1 stale hit.
			if dbInst.LifecycleState == LifecycleRunning || dbInst.Status == StatusRunning {
				m.seedRouteFromInstance(dbInst)
				if _, err := m.backend.Status(ctx, dbInst.ID); err == nil {
					m.instances[sessionID] = dbInst
					m.instancesBySandbox[dbInst.ID] = dbInst
					m.troopers[agentID] = dbInst
					m.mu.Unlock()
					m.touchLastUsed(sessionID)
					logger.WithFields(
						"sandbox_id", dbInst.ID,
						"agent_id", agentID,
						"session_id", sessionID,
					).Info("sandbox_manager: adopted existing trooper from DB after gateway restart")
					return dbInst, nil
				}
				// Backend says VM is gone — fall through to recreate.
				logger.WithFields(
					"sandbox_id", dbInst.ID,
					"agent_id", agentID,
				).Warn("sandbox_manager: DB has running trooper but backend doesn't; will recreate")
			} else if dbInst.LifecycleState == LifecycleStopped || dbInst.LifecycleState == "sleeping" {
				sandboxIDFromDB := dbInst.ID
				m.mu.Unlock()
				if config.BrowserSidecar != nil {
					_ = m.PatchInstanceConfig(ctx, sandboxIDFromDB, func(cfg *InstanceConfig) {
						cfg.BrowserSidecar = config.BrowserSidecar
					})
				}
				if revived, reviveErr := m.ReviveSandbox(ctx, sandboxIDFromDB); reviveErr == nil {
					m.mu.Lock()
					m.instances[sessionID] = revived
					m.instancesBySandbox[revived.ID] = revived
					m.troopers[agentID] = revived
					m.mu.Unlock()
					logger.WithFields(
						"sandbox_id", revived.ID,
						"agent_id", agentID,
					).Info("sandbox_manager: revived existing trooper from DB after gateway restart")
					return revived, nil
				}
				// Revive failed — fall through to recreate. Re-acquire lock.
				m.mu.Lock()
			}
			// dbInst is terminated/failed or revive fell through; let
			// the creation path below handle it. (Terminated rows have
			// the same canonical ID, so the placeholder INSERT below
			// will hit ON CONFLICT and re-use the slot.)
		}
	}

	// Check trooper limit for this tenant
	if err := m.checkTrooperLimit(ctx, tenantID); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if limit := m.ConcurrentSandboxLimit(tenantID); limit != UnlimitedSandboxLimit {
		used := 0
		for _, candidate := range m.instancesBySandbox {
			if sandboxReservesConcurrentSlot(candidate, tenantID, sandboxID) {
				used++
			}
		}
		if used >= limit {
			m.mu.Unlock()
			return nil, fmt.Errorf(
				"%w: %d of %d slots are allocated; stop or sleep a sandbox before starting another",
				ErrConcurrentSandboxLimit,
				used,
				limit,
			)
		}
	}

	// Clamp config to trooper tier limits, then to global+tenant caps.
	config = m.ClampToTrooperLimits(config, tenantID)
	config = m.clampToGlobalLimitsForTenant(config, tenantID)
	if err := m.ValidateSandboxMachineProfile(config, tenantID); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	name := config.Name
	if name == "" {
		name = fmt.Sprintf("trooper-%s", agentID[:min(8, len(agentID))])
	}

	image := config.Image
	if image == "" {
		image = m.globalConfig.DefaultImage
	}

	instanceConfig := InstanceConfig{
		Image:          image,
		CPULimit:       config.CPULimit,
		MemoryMB:       config.MemoryMB,
		DiskMB:         config.DiskMB,
		TimeoutSeconds: config.TimeoutSeconds,
		NetworkMode:    NetworkMode(config.NetworkMode),
		AllowedHosts:   config.AllowedHosts,
		EnvVars:        config.EnvVars,
		WorkDir:        resolveWorkDir(config.WorkDir),
		TenantID:       tenantID,
		SessionID:      sessionID,
		Name:           name,
		DNSServers:     m.globalConfig.DNSServers,
		SSHEnabled:     config.SSHEnabled,
		AgentID:        agentID,
		BrowserSidecar: config.BrowserSidecar,
	}

	// Reserve slot
	placeholder := &Instance{
		ID:             sandboxID,
		Status:         StatusPending,
		Config:         instanceConfig,
		CreatedAt:      time.Now(),
		LastUsedAt:     time.Now(),
		Backend:        m.backend.Name(),
		Name:           name,
		LifecycleState: LifecycleRunning,
		Persistent:     true,
		AgentID:        agentID,
	}
	m.instances[sessionID] = placeholder
	m.instancesBySandbox[sandboxID] = placeholder
	m.troopers[agentID] = placeholder
	m.mu.Unlock()

	// Acquire creation semaphore
	if err := m.createSem.Acquire(ctx, 1); err != nil {
		m.mu.Lock()
		delete(m.instances, sessionID)
		delete(m.instancesBySandbox, sandboxID)
		delete(m.troopers, agentID)
		m.mu.Unlock()
		return nil, fmt.Errorf("trooper creation cancelled: %w", err)
	}
	defer m.createSem.Release(1)

	inst, err := m.backend.Create(ctx, sandboxID, instanceConfig)
	if err != nil {
		m.mu.Lock()
		delete(m.instances, sessionID)
		delete(m.instancesBySandbox, sandboxID)
		delete(m.troopers, agentID)
		m.mu.Unlock()
		return nil, fmt.Errorf("failed to create trooper: %w", err)
	}

	// Finalize instance
	inst.LastUsedAt = time.Now()
	inst.BillingStartedAt = inst.LastUsedAt.UTC()
	inst.BillingEndedAt = time.Time{}
	inst.LifecycleState = LifecycleRunning
	inst.Persistent = true
	inst.AgentID = agentID

	retentionSecs := m.resolveRetention(tenantID, config.IdleRetentionSeconds)
	inst.IdleRetentionSecs = retentionSecs

	m.mu.Lock()
	m.instances[sessionID] = inst
	m.instancesBySandbox[sandboxID] = inst
	m.troopers[agentID] = inst
	m.mu.Unlock()

	m.persistInstance(inst)

	logger.WithFields(
		"sandbox_id", sandboxID,
		"session_id", sessionID,
		"agent_id", agentID,
		"tenant_id", tenantID,
	).Info("sandbox_manager: persistent trooper created")

	return inst, nil
}

// checkPersistentAgentLimit verifies that the tenant hasn't exceeded their
// persistent agent limit. Must be called under m.mu.Lock.
// Replaces the former checkTrooperLimit — both persistent agents and legacy
// troopers count against the same PERSISTENT_AGENTS quota.
func (m *SandboxManager) checkPersistentAgentLimit(ctx context.Context, tenantID string) error {
	if m.trooperLimitResolver == nil || edition.IsDev() {
		return nil // dev mode: no limits
	}

	maxPersistent := m.trooperLimitResolver.ResolveMaxTroopers(tenantID)
	if maxPersistent == -1 {
		return nil // unlimited
	}

	// Count persistent agents for this tenant from DB
	if m.db != nil {
		var count int
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		const q = `SELECT COUNT(*) FROM sandbox_instances WHERE tenant_id = $1 AND persistent = true AND lifecycle_state NOT IN ('terminated')`
		if err := m.db.GetContext(queryCtx, &count, q, tenantID); err != nil {
			return fmt.Errorf("failed to check persistent agent limit: %w", err)
		}
		if count >= maxPersistent {
			return fmt.Errorf("persistent agent limit reached: %d/%d (upgrade at https://everstack.dev/pricing)", count, maxPersistent)
		}
		return nil
	}

	// Fallback: count from in-memory map
	count := 0
	for _, inst := range m.troopers {
		if inst.Config.TenantID == tenantID {
			count++
		}
	}
	if count >= maxPersistent {
		return fmt.Errorf("persistent agent limit reached: %d/%d", count, maxPersistent)
	}
	return nil
}

// checkTrooperLimit is a backwards-compatible alias for checkPersistentAgentLimit.
// Deprecated: Use checkPersistentAgentLimit instead.
func (m *SandboxManager) checkTrooperLimit(ctx context.Context, tenantID string) error {
	return m.checkPersistentAgentLimit(ctx, tenantID)
}

// ClampToTrooperLimits caps CPU/memory/disk at the plan's largest supported
// persistent-agent size. Smaller fixed sizes remain selectable. In shared
// cloud mode the tenant tier resolver is the source of truth even though an
// instance-wide license monitor is intentionally absent.
func (m *SandboxManager) ClampToTrooperLimits(config SandboxConfig, tenantID string) SandboxConfig {
	tier := "enterprise"
	if m.trooperLimitResolver == nil {
		if m.tenantTierResolver.Load() != nil {
			tier = m.resolveTenantTier(tenantID)
		}
	} else {
		tier = m.trooperLimitResolver.ResolvePlanTier(tenantID)
	}
	cpu, memMB, diskMB := ResolveTrooperResources(tier)
	if config.CPULimit <= 0 || config.CPULimit > cpu {
		config.CPULimit = cpu
	}
	if config.MemoryMB <= 0 || config.MemoryMB > memMB {
		config.MemoryMB = memMB
	}
	if config.DiskMB <= 0 || config.DiskMB > diskMB {
		config.DiskMB = diskMB
	}
	return config
}

// restoreInstances discovers existing sandbox instances from the backend
// and repopulates the instances map. Called once during startup before the
// reaper begins, so expired restored instances get cleaned up on the first sweep.
func (m *SandboxManager) restoreInstances() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	instances, err := m.backend.List(ctx)
	if err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_manager: failed to restore instances from backend")
		return
	}

	var cleanupLegacy []string

	for _, inst := range instances {
		sessionID := inst.Config.SessionID
		if sessionID == "" {
			continue
		}
		if strings.TrimSpace(inst.Name) == "" && strings.TrimSpace(inst.Config.Name) != "" {
			inst.Name = strings.TrimSpace(inst.Config.Name)
		}
		if strings.TrimSpace(inst.Config.Name) == "" && strings.TrimSpace(inst.Name) != "" {
			inst.Config.Name = inst.Name
		}
		// Legacy collision guard:
		// - Old troopers could exist as non-persistent sbx_trp-* instances.
		// - New troopers use persistent wks_* instances.
		// If both restore with the same session ID, prefer the persistent/wks one.
		if existing, ok := m.instances[sessionID]; ok {
			switch {
			case existing.Persistent && !inst.Persistent:
				// keep existing mapping; legacy duplicate should be cleaned up.
				if strings.HasPrefix(inst.ID, "sbx_") {
					cleanupLegacy = append(cleanupLegacy, inst.ID)
				}
			case !existing.Persistent && inst.Persistent:
				if strings.HasPrefix(existing.ID, "sbx_") {
					cleanupLegacy = append(cleanupLegacy, existing.ID)
				}
				m.instances[sessionID] = inst
			default:
				// deterministic tie-break when persistence matches
				if strings.HasPrefix(inst.ID, "wks_") && !strings.HasPrefix(existing.ID, "wks_") {
					if strings.HasPrefix(existing.ID, "sbx_") {
						cleanupLegacy = append(cleanupLegacy, existing.ID)
					}
					m.instances[sessionID] = inst
				} else if strings.HasPrefix(existing.ID, "wks_") && strings.HasPrefix(inst.ID, "sbx_") {
					cleanupLegacy = append(cleanupLegacy, inst.ID)
				}
			}
		} else {
			m.instances[sessionID] = inst
		}
		m.instancesBySandbox[inst.ID] = inst
		if inst.Persistent && inst.AgentID != "" {
			m.troopers[inst.AgentID] = inst
		}
	}

	// DB is intentionally unavailable during backend discovery. Defer cleanup
	// until SetDB so any pre-existing compute window is metered before destroy.
	m.pendingLegacyCleanup = cleanupLegacy

	if len(instances) > 0 {
		logger.WithFields("count", len(m.instances)).
			Info("sandbox_manager: restored sandbox instances from previous run")
	}
}

func (m *SandboxManager) cleanupDeferredLegacyShadows() {
	if len(m.pendingLegacyCleanup) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(m.pendingLegacyCleanup))
	for _, sandboxID := range m.pendingLegacyCleanup {
		if sandboxID == "" {
			continue
		}
		if _, ok := seen[sandboxID]; ok {
			continue
		}
		seen[sandboxID] = struct{}{}

		destroyCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := m.BackendDestroy(destroyCtx, sandboxID)
		cancel()
		if err != nil {
			logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
				Warn("sandbox_manager: failed to destroy legacy shadow sandbox")
			continue
		}

		m.mu.Lock()
		if cur, ok := m.instancesBySandbox[sandboxID]; ok {
			if live, liveOK := m.instances[cur.Config.SessionID]; liveOK && live.ID == sandboxID {
				delete(m.instances, cur.Config.SessionID)
			}
		}
		delete(m.instancesBySandbox, sandboxID)
		m.mu.Unlock()
		logger.WithFields("sandbox_id", sandboxID).
			Info("sandbox_manager: destroyed legacy shadow sandbox")
	}
	m.pendingLegacyCleanup = nil
}

// GetLinked finds an existing running sandbox by its session ID and registers
// the caller's session as an additional consumer. This allows multiple agents
// to share the same sandbox instance (e.g. when an agent links to a
// pre-existing sandbox created from the Sandboxes page).
//
// Lookup falls through three layers so a control-plane restart doesn't
// break linked agents whose sandbox is still alive on the host:
//  1. In-memory map (fastest, hot path)
//  2. Shared registry (Redis when configured) — survives process restart
//     and is shared across replicas.
//  3. Postgres `sandbox_instances` — durable source of truth.
//
// When layers 2 or 3 hit, the in-process Instance handle is reconstituted
// and re-registered in the in-memory map so subsequent calls are fast.
// Only when *all three* misses do we fail — that means the linked sandbox
// truly does not exist.
func (m *SandboxManager) GetLinked(ctx context.Context, linkedSessionID, callerSessionID, callerTenantID string) (*Instance, error) {
	m.mu.RLock()
	inst, ok := m.instances[linkedSessionID]
	m.mu.RUnlock()

	if !ok || inst.Status != StatusRunning {
		rehydrated, err := m.rehydrateLinkedSandbox(ctx, linkedSessionID, callerTenantID)
		if err != nil {
			return nil, err
		}
		inst = rehydrated
	}

	// Tenant isolation check
	if inst.Config.TenantID != callerTenantID {
		return nil, fmt.Errorf("linked sandbox belongs to a different tenant")
	}

	// Register caller's session → same instance
	m.mu.Lock()
	m.instances[callerSessionID] = inst
	m.mu.Unlock()

	m.registryLinkSession(ctx, callerSessionID, inst.ID)
	m.touchLastUsed(callerSessionID)
	return inst, nil
}

// rehydrateLinkedSandbox attempts to rebuild an in-process Instance handle
// for a linked sandbox whose owning session is no longer in the in-memory
// map. Tries the shared registry first, then falls back to Postgres. If
// either source has a row, the backend is probed to confirm the sandbox
// is still alive before the Instance is reconstituted and re-registered.
//
// Returns an error only when the sandbox is truly gone (all three layers
// agree). Callers (notably the persistent-agent reconciler) treat this as
// a recoverable-but-loud condition, not a silent reset.
func (m *SandboxManager) rehydrateLinkedSandbox(ctx context.Context, linkedSessionID, callerTenantID string) (*Instance, error) {
	// Layer 2: shared registry.
	if m.registry != nil {
		regCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		entry, err := m.registry.GetBySessionID(regCtx, linkedSessionID)
		cancel()
		if err == nil && entry != nil {
			if entry.TenantID != "" && entry.TenantID != callerTenantID {
				return nil, fmt.Errorf("linked sandbox belongs to a different tenant")
			}
			if inst, ok := m.adoptFromBackend(ctx, entry.SandboxID, linkedSessionID, entry); ok {
				return inst, nil
			}
		} else if err != nil && !errors.Is(err, ErrRegistryMiss) {
			logger.WithFields("linked_session_id", linkedSessionID, "error", err.Error()).
				Debug("sandbox_manager: registry lookup failed, falling back to DB")
		}
	}

	// Layer 3: Postgres.
	if m.db != nil {
		row, err := m.loadInstanceRowBySessionID(ctx, linkedSessionID)
		if err == nil && row != nil {
			if row.TenantID != "" && row.TenantID != callerTenantID {
				return nil, fmt.Errorf("linked sandbox belongs to a different tenant")
			}
			if inst, ok := m.adoptFromBackend(ctx, row.SandboxID, linkedSessionID, row); ok {
				return inst, nil
			}
		} else if err != nil {
			logger.WithFields("linked_session_id", linkedSessionID, "error", err.Error()).
				Debug("sandbox_manager: DB rehydrate lookup failed")
		}
	}

	return nil, fmt.Errorf("no running sandbox for linked session %s", linkedSessionID)
}

// adoptFromBackend probes the backend for sandboxID, and if it reports
// running, reconstitutes an Instance from the entry projection plus the
// backend's authoritative status, registers it under linkedSessionID,
// seeds the route table for fcagent, and writes back to the registry.
// Returns (instance, true) on success.
func (m *SandboxManager) adoptFromBackend(parent context.Context, sandboxID, linkedSessionID string, entry *RegistryEntry) (*Instance, bool) {
	if sandboxID == "" {
		return nil, false
	}
	if entry != nil && entry.AgentTarget != "" {
		m.SeedRoute(sandboxID, entry.AgentTarget)
	}
	probeCtx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	inst, err := m.backend.Status(probeCtx, sandboxID)
	if err != nil || inst == nil {
		if err != nil {
			logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
				Debug("sandbox_manager: backend probe during rehydrate failed")
		}
		return nil, false
	}
	if inst.Status != StatusRunning {
		return nil, false
	}
	// Backfill the projection fields that Status may not include.
	if entry != nil {
		if inst.ID == "" {
			inst.ID = sandboxID
		}
		if inst.Config.TenantID == "" {
			inst.Config.TenantID = entry.TenantID
		}
		if inst.Config.SessionID == "" {
			inst.Config.SessionID = entry.SessionID
		}
		if inst.AgentTarget == "" {
			inst.AgentTarget = entry.AgentTarget
		}
		if inst.AgentID == "" {
			inst.AgentID = entry.AgentID
		}
		if inst.Backend == "" {
			inst.Backend = entry.BackendType
		}
		if inst.ShortCode == "" {
			inst.ShortCode = entry.ShortCode
		}
	}
	if inst.LastUsedAt.IsZero() {
		inst.LastUsedAt = time.Now()
	}

	m.mu.Lock()
	m.instances[linkedSessionID] = inst
	m.instancesBySandbox[inst.ID] = inst
	if inst.Persistent && inst.AgentID != "" {
		m.troopers[inst.AgentID] = inst
	}
	m.mu.Unlock()

	m.registryPut(parent, inst)
	logger.WithFields(
		"sandbox_id", sandboxID,
		"linked_session_id", linkedSessionID,
		"backend", inst.Backend,
		"agent_target", inst.AgentTarget,
	).Info("sandbox_manager: rehydrated linked sandbox from registry/DB")
	return inst, true
}

// loadInstanceRowBySessionID fetches the routing-subset columns of the
// most recent live sandbox_instances row for the given session. Returns
// nil with no error when no row matches. Returns a RegistryEntry-shaped
// struct so adoptFromBackend can treat registry and DB results uniformly.
func (m *SandboxManager) loadInstanceRowBySessionID(ctx context.Context, sessionID string) (*RegistryEntry, error) {
	if m.db == nil {
		return nil, nil
	}
	type row struct {
		ID          string         `db:"id"`
		TenantID    string         `db:"tenant_id"`
		AgentID     sql.NullString `db:"agent_id"`
		Backend     string         `db:"backend"`
		AgentTarget sql.NullString `db:"agent_target"`
		Image       sql.NullString `db:"image"`
		Status      string         `db:"status"`
	}
	const q = `
		SELECT id, tenant_id, agent_id, backend, agent_target, image, COALESCE(status, '') AS status
		FROM sandbox_instances
		WHERE session_id = $1
		  AND COALESCE(lifecycle_state, '') NOT IN ('terminated', 'failed')
		ORDER BY created_at DESC
		LIMIT 1`
	qCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var r row
	if err := m.db.GetContext(qCtx, &r, q, sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	entry := &RegistryEntry{
		SandboxID:   r.ID,
		TenantID:    r.TenantID,
		BackendType: r.Backend,
		SessionID:   sessionID,
		Status:      r.Status,
	}
	if r.AgentID.Valid {
		entry.AgentID = r.AgentID.String
	}
	if r.AgentTarget.Valid {
		entry.AgentTarget = r.AgentTarget.String
	}
	if r.Image.Valid {
		entry.Image = r.Image.String
	}
	return entry, nil
}

// GetOrCreate returns an existing sandbox for the session, or creates a new
// one synchronously. The returned instance is fully provisioned — image
// pulled, container started — and ready for Exec. Use this from agent
// tools, REST handlers that exec immediately, the CLI, and the eval runner.
func (m *SandboxManager) GetOrCreate(ctx context.Context, sessionID, tenantID string, config SandboxConfig) (*Instance, error) {
	return m.getOrCreateImpl(ctx, sessionID, tenantID, config, false)
}

// GetOrCreateAsync returns immediately with a "pending" placeholder and
// runs the heavy lifting (image pull, eager git clone, backend.Create) in a
// background goroutine. Callers that need to exec inside the sandbox right
// away must use GetOrCreate instead — the placeholder has no container_id
// and the backend is not yet running. Used by the admin RPC so the UI does
// not block on slow first-time image pulls.
func (m *SandboxManager) GetOrCreateAsync(ctx context.Context, sessionID, tenantID string, config SandboxConfig) (*Instance, error) {
	return m.getOrCreateImpl(ctx, sessionID, tenantID, config, true)
}

func (m *SandboxManager) getOrCreateImpl(ctx context.Context, sessionID, tenantID string, config SandboxConfig, async bool) (*Instance, error) {
	if err := m.RequireSandboxBilling(tenantID); err != nil {
		return nil, err
	}
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if ok && inst.Status == StatusRunning {
		// If config now requires a git repo but the running sandbox lacks one,
		// tear down the old sandbox so we can recreate with the repo mounted.
		needsGitUpgrade := config.GitRepoURL != "" && config.GitInstallationID > 0 &&
			inst.Config.RepoHostPath == "" && !m.lazyGitCloneEnabledForSession(sessionID)
		// If network mode or allowed hosts changed, recreate so the new
		// network policy takes effect (e.g. deny → whitelist/allow).
		needsNetworkUpgrade := networkConfigChanged(inst, config)
		if !needsGitUpgrade && !needsNetworkUpgrade {
			m.touchLastUsed(sessionID)
			return inst, nil
		}
		if needsNetworkUpgrade {
			logger.WithFields("session_id", sessionID, "sandbox_id", inst.ID,
				"old_network_mode", string(inst.Config.NetworkMode), "new_network_mode", config.NetworkMode).
				Info("sandbox_manager: recreating sandbox for network mode change")
		}
		if needsGitUpgrade {
			logger.WithFields("session_id", sessionID, "sandbox_id", inst.ID, "requested_repo", config.GitRepoURL).
				Info("sandbox_manager: recreating sandbox to mount git repo")
		}
		// Fall through to acquire write lock and recreate
	}

	requestedSandboxID := fmt.Sprintf("sbx_%s", sessionID)
	if err := m.RequireConcurrentSandboxSlot(ctx, tenantID, requestedSandboxID); err != nil {
		return nil, err
	}

	m.mu.Lock()
	// NOTE: Lock is released manually (not deferred) because we drop it before
	// the expensive backend.Create call. All early-return paths below must
	// explicitly call m.mu.Unlock().

	// Double-check after acquiring write lock
	if inst, ok := m.instances[sessionID]; ok && inst.Status == StatusRunning {
		needsGitUpgrade := config.GitRepoURL != "" && config.GitInstallationID > 0 &&
			inst.Config.RepoHostPath == "" && !m.lazyGitCloneEnabledForSession(sessionID)
		needsNetworkUpgrade := networkConfigChanged(inst, config)
		if !needsGitUpgrade && !needsNetworkUpgrade {
			inst.LastUsedAt = time.Now()
			m.mu.Unlock()
			return inst, nil
		}
	}

	// Clean up any existing entry for this session (stale or being upgraded)
	// so we don't leak map slots, and destroy the old container if it still exists.
	// Destroy synchronously to avoid racing with the subsequent Create call —
	// K8s will reject a pod create while the old pod with the same name is
	// still terminating ("object is being deleted").
	if old, ok := m.instances[sessionID]; ok {
		delete(m.instances, sessionID)
		delete(m.instancesBySandbox, old.ID)
		wasBillable := !old.BillingStartedAt.IsZero()
		if wasBillable {
			m.captureNetworkBytes(ctx, old)
		}
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		destroyErr := m.destroyBackendAndConfirmGone(cleanCtx, old.ID)
		cancel()
		if destroyErr != nil {
			m.markDestroyed(old.ID, destroyErr)
			m.mu.Unlock()
			return nil, fmt.Errorf("failed to destroy old sandbox before recreate: %w", destroyErr)
		}
		stoppedAt := time.Now().UTC()
		if wasBillable {
			if !m.recordUsageForInstance(ctx, old, EventDestroyed, "reconfigured", stoppedAt) {
				m.mu.Unlock()
				return nil, fmt.Errorf("failed to close old sandbox compute billing window before recreate")
			}
			old.BillingStartedAt = time.Time{}
			old.BillingEndedAt = time.Time{}
		}
		m.markDestroyed(old.ID, nil)
	}

	// Check global sandbox limit
	if len(m.instances) >= m.globalConfig.MaxSandboxes {
		m.mu.Unlock()
		return nil, fmt.Errorf("sandbox limit reached (%d)", m.globalConfig.MaxSandboxes)
	}
	if limit := m.ConcurrentSandboxLimit(tenantID); limit != UnlimitedSandboxLimit {
		used := 0
		for _, candidate := range m.instancesBySandbox {
			if sandboxReservesConcurrentSlot(candidate, tenantID, requestedSandboxID) {
				used++
			}
		}
		if used >= limit {
			m.mu.Unlock()
			return nil, fmt.Errorf(
				"%w: %d of %d slots are allocated; stop or sleep a sandbox before starting another",
				ErrConcurrentSandboxLimit,
				used,
				limit,
			)
		}
	}

	// Clamp config to global + per-tenant runtime caps.
	config = m.clampToGlobalLimitsForTenant(config, tenantID)
	if err := m.ValidateSandboxMachineProfile(config, tenantID); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	// Resolve image
	image := config.Image
	if image == "" {
		image = m.globalConfig.DefaultImage
	}

	// Validate image against allowlist
	if len(m.globalConfig.AllowedImages) > 0 {
		allowed := false
		for _, img := range m.globalConfig.AllowedImages {
			if img == image {
				allowed = true
				break
			}
		}
		if !allowed {
			m.mu.Unlock()
			return nil, fmt.Errorf("image %q is not in the allowed images list", image)
		}
	}

	sandboxID := requestedSandboxID

	// Auto-generate a friendly name if none was provided.
	if config.Name == "" {
		existing := make(map[string]struct{}, len(m.instancesBySandbox))
		for _, inst := range m.instancesBySandbox {
			if inst.Name != "" {
				existing[inst.Name] = struct{}{}
			}
		}
		config.Name = GenerateUniqueName(existing)
	}

	instanceConfig := InstanceConfig{
		Image:             image,
		CPULimit:          config.CPULimit,
		MemoryMB:          config.MemoryMB,
		DiskMB:            config.DiskMB,
		TimeoutSeconds:    config.TimeoutSeconds,
		NetworkMode:       NetworkMode(config.NetworkMode),
		AllowedHosts:      config.AllowedHosts,
		EnvVars:           config.EnvVars,
		WorkDir:           resolveWorkDir(config.WorkDir),
		TenantID:          tenantID,
		SessionID:         sessionID,
		Name:              config.Name,
		DNSServers:        m.globalConfig.DNSServers,
		GitRepoURL:        config.GitRepoURL,
		GitBranch:         config.GitBranch,
		GitInstallationID: config.GitInstallationID,
		SSHEnabled:        config.SSHEnabled,
		BrowserSidecar:    config.BrowserSidecar,
	}

	// Git import: clone repo to host-side directory before container creation.
	if config.GitRepoURL != "" && config.GitInstallationID <= 0 {
		m.mu.Unlock()
		return nil, fmt.Errorf("git_installation_id is required when git_repo_url is set")
	}
	if config.GitRepoURL != "" && !CapabilitiesForBackend(m.backend).Features.GitImport {
		m.mu.Unlock()
		return nil, ErrUnsupportedBackend
	}
	if config.GitRepoURL == "" {
		logger.WithFields("session_id", sessionID, "sandbox_id", sandboxID).
			Debug("sandbox_manager: no git_repo_url configured, skipping git clone")
	}
	lazyGitForSession := m.lazyGitCloneEnabledForSession(sessionID)
	if config.GitRepoURL != "" && config.GitInstallationID > 0 && lazyGitForSession {
		// Lazy clone: record the deferral now and let EnsureRepoReady run the
		// clone on-demand later. Eager-clone path runs inside finalizeCreate so
		// it does not block async callers.
		m.recordEvent(sandboxID, sessionID, tenantID, EventGitCloneDeferred, "Git clone deferred until repository access", map[string]interface{}{
			"repo":           config.GitRepoURL,
			"branch":         config.GitBranch,
			"clone_strategy": "lazy_on_demand",
		}, nil, "")
	}

	eagerClone := config.GitRepoURL != "" && config.GitInstallationID > 0 && !lazyGitForSession

	// Reserve a slot with a pending placeholder so we can release the write
	// lock before the expensive backend.Create call. The placeholder carries
	// enough data (id, name, image, backend) for the FE to render a row even
	// before the container exists.
	placeholder := &Instance{
		ID:             sandboxID,
		Status:         StatusPending,
		LifecycleState: m.directCreateLifecycleState(),
		Config:         instanceConfig,
		Backend:        m.backend.Name(),
		CreatedAt:      time.Now(),
		LastUsedAt:     time.Now(),
		Name:           config.Name,
	}
	if config.TimeoutSeconds > 0 {
		placeholder.ExpiresAt = placeholder.CreatedAt.Add(time.Duration(config.TimeoutSeconds) * time.Second)
	}
	m.instances[sessionID] = placeholder
	m.instancesBySandbox[sandboxID] = placeholder
	m.mu.Unlock()
	logger.WithFields("sandbox_id", sandboxID, "session_id", sessionID, "tenant_id", tenantID).
		Info("sandbox_manager: placeholder reserved, persisting pending row")

	// Persist the in-flight row up front for both sync and async paths. The
	// admin page polls ListSandboxInstances every 1.5s; without a DB row
	// in place at this point, a slow backend.Create leaves the user
	// staring at the previous instances list with no indication that
	// anything is happening. The 10-min stale-pending sweeper in
	// agents.go reconciles this row to "failed" if Create never completes.
	m.persistInstance(placeholder)
	logger.WithFields("sandbox_id", sandboxID).Info("sandbox_manager: pending row persisted")

	if async {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.WithFields("sandbox_id", sandboxID, "panic", fmt.Sprintf("%v", r)).
						Error("sandbox_manager: panic in async finalizeCreate")
					m.removePlaceholder(sessionID, sandboxID, placeholder)
				}
			}()
			// Detach from the RPC ctx so cancellation does not abort an
			// in-flight image pull. Cap the work at 10m to avoid leaks.
			bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
			defer cancel()
			if _, err := m.finalizeCreate(bgCtx, placeholder, instanceConfig, config, sandboxID, sessionID, tenantID, image, eagerClone); err != nil {
				logger.WithFields("session_id", sessionID, "sandbox_id", sandboxID, "error", err.Error()).
					Warn("sandbox_manager: async sandbox create failed")
			}
		}()
		return placeholder, nil
	}

	return m.finalizeCreate(ctx, placeholder, instanceConfig, config, sandboxID, sessionID, tenantID, image, eagerClone)
}

// directCreateLifecycleState keeps the synchronous manager path distinct from
// rows owned by the desired-state reconciler. A reconciler-enabled gateway
// claims pending/creating rows and calls ExecuteCreate itself. Persisting a
// direct GetOrCreate as pending therefore lets another gateway provision the
// same sandbox concurrently. "provisioning" remains visible as creating to
// API clients but is deliberately outside the reconciler's claimable states.
func (m *SandboxManager) directCreateLifecycleState() string {
	if m.delegateLifecycleToReconciler() {
		return "provisioning"
	}
	return "pending"
}

// finalizeCreate runs the slow part of provisioning: optional eager git clone,
// semaphore acquisition, backend.Create, and post-create persistence. Called
// either inline (sync GetOrCreate) or from a goroutine (GetOrCreateAsync).
func (m *SandboxManager) finalizeCreate(ctx context.Context, placeholder *Instance, instanceConfig InstanceConfig, config SandboxConfig, sandboxID, sessionID, tenantID, image string, eagerClone bool) (*Instance, error) {
	if eagerClone {
		m.recordEvent(sandboxID, sessionID, tenantID, EventGitCloneStart, "Cloning git repository", map[string]interface{}{
			"repo":           config.GitRepoURL,
			"branch":         config.GitBranch,
			"clone_strategy": "eager_provision",
		}, nil, "")

		cloneStart := time.Now()
		repoHostPath, commitSHA, repoSizeBytes, cloneErr := m.cloneRepo(ctx, sandboxID, tenantID, config)
		cloneDurationMs := time.Since(cloneStart).Milliseconds()
		if cloneErr != nil {
			m.removePlaceholder(sessionID, sandboxID, placeholder)
			m.recordEvent(sandboxID, sessionID, tenantID, EventGitCloneFail, "Git clone failed", map[string]interface{}{
				"error":             cloneErr.Error(),
				"clone_duration_ms": cloneDurationMs,
				"clone_strategy":    "eager_provision",
			}, &cloneDurationMs, cloneErr.Error())
			m.markPlaceholderFailed(placeholder, cloneErr)
			return nil, fmt.Errorf("git clone failed: %w", cloneErr)
		}
		instanceConfig.RepoHostPath = repoHostPath
		instanceConfig.GitCommitSHA = commitSHA
		m.recordEvent(sandboxID, sessionID, tenantID, EventGitClone, "Git clone completed", map[string]interface{}{
			"commit_sha":         commitSHA,
			"repo":               config.GitRepoURL,
			"branch":             config.GitBranch,
			"repo_size_bytes":    repoSizeBytes,
			"clone_duration_ms":  cloneDurationMs,
			"clone_strategy":     "eager_provision",
			"triggered_by_phase": "sandbox_create",
		}, &cloneDurationMs, "")
	}

	// Acquire creation semaphore to limit concurrent backend.Create calls.
	logger.WithFields("sandbox_id", sandboxID).Info("finalizeCreate: acquiring createSem")
	if err := m.createSem.Acquire(ctx, 1); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).Warn("finalizeCreate: createSem acquire failed")
		m.removePlaceholder(sessionID, sandboxID, placeholder)
		m.markPlaceholderFailed(placeholder, err)
		return nil, fmt.Errorf("sandbox creation cancelled: %w", err)
	}
	logger.WithFields("sandbox_id", sandboxID).Info("finalizeCreate: createSem acquired, recording event")

	m.recordEvent(sandboxID, sessionID, tenantID, EventCreated, "Creating sandbox", map[string]interface{}{"image": image}, nil, "")
	logger.WithFields("sandbox_id", sandboxID, "backend", m.backend.Name(), "image", image).
		Info("finalizeCreate: calling backend.Create")

	createStart := time.Now()
	inst, err := m.backend.Create(ctx, sandboxID, instanceConfig)
	logger.WithFields("sandbox_id", sandboxID, "duration_ms", time.Since(createStart).Milliseconds(), "err", fmt.Sprintf("%v", err)).
		Info("finalizeCreate: backend.Create returned")
	m.createSem.Release(1)

	if err != nil {
		m.removePlaceholder(sessionID, sandboxID, placeholder)
		m.recordEvent(sandboxID, sessionID, tenantID, EventError, "Failed to create sandbox", map[string]interface{}{"error": err.Error()}, nil, err.Error())
		m.markPlaceholderFailed(placeholder, err)
		return nil, fmt.Errorf("failed to create sandbox: %w", err)
	}

	now := time.Now()
	createDuration := now.Sub(createStart).Milliseconds()
	inst.LastUsedAt = now
	inst.BillingStartedAt = now.UTC()
	inst.BillingEndedAt = time.Time{}
	inst.Name = config.Name
	inst.IdleRetentionSecs = m.resolveRetention(tenantID, config.IdleRetentionSeconds)
	inst.LifecycleState = "running"
	inst.GitRepoURL = instanceConfig.GitRepoURL
	inst.GitBranch = instanceConfig.GitBranch
	inst.GitCommitSHA = instanceConfig.GitCommitSHA
	inst.KeepWarm = config.KeepWarm

	// Re-acquire lock to replace the placeholder with the real instance.
	m.mu.Lock()
	m.instances[sessionID] = inst
	m.instancesBySandbox[inst.ID] = inst
	m.mu.Unlock()

	m.persistInstance(inst)
	m.registryPut(ctx, inst)

	m.recordEvent(sandboxID, sessionID, tenantID, EventReady, "Sandbox ready", map[string]interface{}{"image": image, "backend": inst.Backend}, &createDuration, "")

	logger.WithFields("session_id", sessionID, "sandbox_id", sandboxID, "image", image).
		Info("sandbox_manager: created sandbox")

	return inst, nil
}

// removePlaceholder evicts a pending instance from the in-memory maps if it
// is still the current entry for the session. No-op if the placeholder has
// already been replaced or another caller raced to delete it.
func (m *SandboxManager) removePlaceholder(sessionID, sandboxID string, placeholder *Instance) {
	m.mu.Lock()
	if cur, ok := m.instances[sessionID]; ok && cur == placeholder {
		delete(m.instances, sessionID)
		delete(m.instancesBySandbox, sandboxID)
	}
	m.mu.Unlock()
}

// markPlaceholderFailed writes a failed lifecycle row to the DB so the FE
// can surface the error in the instances list. Best-effort: the in-memory
// row is gone (removePlaceholder ran first), so the FE sees a transient
// "failed" entry until the user dismisses it.
func (m *SandboxManager) markPlaceholderFailed(placeholder *Instance, cause error) {
	if placeholder == nil {
		return
	}
	_ = cause // captured in EventError via recordEvent; no field on Instance for it
	failed := *placeholder
	failed.Status = StatusFailed
	failed.LifecycleState = "failed"
	m.persistInstance(&failed)
}

// RepoCloneResult contains metadata about a completed git clone operation.
type RepoCloneResult struct {
	Cloned     bool
	DurationMs int64
	SizeBytes  int64
	Strategy   string
}

// EnsureRepoReady guarantees that a session sandbox with a configured git source
// has a cloned repo mounted at /repo. When lazy clone is enabled this performs
// clone + in-place container recreation on demand.
func (m *SandboxManager) EnsureRepoReady(ctx context.Context, sessionID string) (*Instance, *RepoCloneResult, error) {
	if sessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}

	lock := m.repoLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}
	if strings.TrimSpace(inst.Config.GitRepoURL) == "" || inst.Config.GitInstallationID <= 0 {
		return inst, nil, nil
	}
	if inst.Config.RepoHostPath != "" {
		return inst, nil, nil
	}
	if !CapabilitiesForBackend(m.backend).Features.GitImport {
		return nil, nil, ErrUnsupportedBackend
	}
	if err := m.RequireSandboxBilling(inst.Config.TenantID); err != nil {
		return nil, nil, err
	}
	if err := m.validateInstanceMachineProfile(inst.Config); err != nil {
		return nil, nil, err
	}

	sandboxID := inst.ID
	tenantID := inst.Config.TenantID
	cloneConfig := SandboxConfig{
		GitRepoURL:        inst.Config.GitRepoURL,
		GitBranch:         inst.Config.GitBranch,
		GitInstallationID: inst.Config.GitInstallationID,
	}

	m.recordEvent(sandboxID, sessionID, tenantID, EventGitCloneStart, "Cloning git repository", map[string]interface{}{
		"repo":               cloneConfig.GitRepoURL,
		"branch":             cloneConfig.GitBranch,
		"clone_strategy":     "lazy_on_demand",
		"triggered_by_phase": "repo_access",
	}, nil, "")

	cloneStart := time.Now()
	repoHostPath, commitSHA, repoSizeBytes, cloneErr := m.cloneRepo(ctx, sandboxID, tenantID, cloneConfig)
	cloneDurationMs := time.Since(cloneStart).Milliseconds()
	if cloneErr != nil {
		m.recordEvent(sandboxID, sessionID, tenantID, EventGitCloneFail, "Git clone failed", map[string]interface{}{
			"error":              cloneErr.Error(),
			"clone_duration_ms":  cloneDurationMs,
			"clone_strategy":     "lazy_on_demand",
			"triggered_by_phase": "repo_access",
		}, &cloneDurationMs, cloneErr.Error())
		return nil, nil, fmt.Errorf("git clone failed: %w", cloneErr)
	}

	oldInst := *inst

	// Signal callers that the sandbox is being reprovisioned with the repo mount.
	m.mu.Lock()
	inst.LifecycleState = LifecycleRepoProvisioning
	m.mu.Unlock()

	m.captureNetworkBytes(ctx, &oldInst)
	if destroyErr := m.destroyBackendAndConfirmGone(ctx, oldInst.ID); destroyErr != nil {
		m.mu.Lock()
		inst.LifecycleState = LifecycleFailed
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("failed to recreate sandbox with repo mount: %w", destroyErr)
	}
	billingStoppedAt := time.Now()
	if !oldInst.BillingStartedAt.IsZero() {
		if !m.recordUsageForInstance(ctx, &oldInst, EventDestroyed, "reprovisioned", billingStoppedAt) {
			return nil, nil, fmt.Errorf("failed to close sandbox compute billing window before repo reprovision")
		}
	}
	m.closeAllPortMappings(oldInst.ID)

	recreateConfig := oldInst.Config
	recreateConfig.RepoHostPath = repoHostPath
	recreateConfig.GitCommitSHA = commitSHA

	recreated, recreateErr := m.backend.Create(ctx, oldInst.ID, recreateConfig)
	if recreateErr != nil {
		m.mu.Lock()
		if cur, ok := m.instances[sessionID]; ok && cur.ID == oldInst.ID {
			cur.LifecycleState = LifecycleFailed
			delete(m.instances, sessionID)
			delete(m.instancesBySandbox, oldInst.ID)
		}
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("failed to recreate sandbox after git clone: %w", recreateErr)
	}

	now := time.Now()
	recreated.CreatedAt = oldInst.CreatedAt
	recreated.BillingStartedAt = now.UTC()
	recreated.BillingEndedAt = time.Time{}
	recreated.ExpiresAt = oldInst.ExpiresAt
	recreated.LastUsedAt = now
	recreated.Name = oldInst.Name
	recreated.IdleRetentionSecs = oldInst.IdleRetentionSecs
	recreated.LifecycleState = LifecycleRunning
	recreated.GitRepoURL = recreateConfig.GitRepoURL
	recreated.GitBranch = recreateConfig.GitBranch
	recreated.GitCommitSHA = commitSHA

	m.mu.Lock()
	m.instances[sessionID] = recreated
	m.instancesBySandbox[recreated.ID] = recreated
	m.mu.Unlock()
	m.persistInstance(recreated)

	m.recordEvent(sandboxID, sessionID, tenantID, EventGitClone, "Git clone completed", map[string]interface{}{
		"commit_sha":         commitSHA,
		"repo":               cloneConfig.GitRepoURL,
		"branch":             cloneConfig.GitBranch,
		"repo_size_bytes":    repoSizeBytes,
		"clone_duration_ms":  cloneDurationMs,
		"clone_strategy":     "lazy_on_demand",
		"triggered_by_phase": "repo_access",
	}, &cloneDurationMs, "")

	return recreated, &RepoCloneResult{
		Cloned:     true,
		DurationMs: cloneDurationMs,
		SizeBytes:  repoSizeBytes,
		Strategy:   "lazy_on_demand",
	}, nil
}

func (m *SandboxManager) lazyGitCloneEnabledForSession(sessionID string) bool {
	if !m.lazyGitClone {
		return false
	}
	if m.lazyGitClonePct >= 100 {
		return true
	}
	if m.lazyGitClonePct <= 0 || sessionID == "" {
		return false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(sessionID))
	bucket := int(h.Sum32() % 100)
	return bucket < m.lazyGitClonePct
}

// repoLock returns the per-session mutex for serialising repo provisioning.
// Locks are never individually deleted — they are bounded by session count and
// cleaned up in bulk via clearRepoLocks on manager shutdown. Deleting individual
// entries would race with concurrent goroutines that already hold a reference.
func (m *SandboxManager) repoLock(sessionID string) *sync.Mutex {
	m.repoLocksMu.Lock()
	defer m.repoLocksMu.Unlock()
	if lock, ok := m.repoLocks[sessionID]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	m.repoLocks[sessionID] = lock
	return lock
}

func (m *SandboxManager) clearRepoLocks() {
	m.repoLocksMu.Lock()
	m.repoLocks = make(map[string]*sync.Mutex)
	m.repoLocksMu.Unlock()
}

// Exec runs a command in the sandbox associated with the given session.
// If the underlying container/VM has died, the stale in-memory state is purged
// and ErrSandboxNotRunning is returned so callers can recreate and retry.
func (m *SandboxManager) Exec(ctx context.Context, sessionID string, req ExecRequest) (*ExecResult, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}

	cmdStr := strings.Join(req.Command, " ")
	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventExecStart, "Executing command", map[string]interface{}{"command": cmdStr}, nil, "")

	// Telemetry span for the command execution (traces-module-replan M1-T2).
	// This is distinct from the operational recordEvent above: the span is a
	// first-class sandbox observation that nests under the active agent turn.
	execCtx, span := telemetry.StartSandboxExecSpan(ctx, inst.ID, sandboxExecOp(req.Command))
	defer span.End()
	span.SetAttributes(attribute.String(attrs.SandboxCommand, truncateForAttr(cmdStr, maxSandboxCmdAttr)))

	defer m.touchLastUsed(sessionID)
	start := time.Now()
	result, err := m.backend.Exec(execCtx, inst.ID, req)

	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		span.SetAttributes(attribute.Int64(attrs.SandboxDurationMs, durationMs))
		telemetry.RecordError(span, err)
		// If the container/VM is gone, purge stale in-memory state so the
		// next ensureSandbox call will create a fresh sandbox.
		if errors.Is(err, ErrSandboxNotRunning) {
			m.purgeStaleSandbox(sessionID, inst.ID)
		}
		m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventExecDone, "Execution failed", map[string]interface{}{"command": cmdStr, "error": err.Error()}, &durationMs, err.Error())
		return nil, err
	}

	span.SetAttributes(
		attribute.Int(attrs.SandboxExitCode, result.ExitCode),
		attribute.Int64(attrs.SandboxDurationMs, durationMs),
		attribute.Bool(attrs.SandboxTimedOut, result.TimedOut),
		attribute.Int(attrs.SandboxStdoutBytes, len(result.Stdout)),
		attribute.Int(attrs.SandboxStderrBytes, len(result.Stderr)),
	)

	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventExecDone, "Execution completed", map[string]interface{}{
		"command":   cmdStr,
		"exit_code": result.ExitCode,
		"timed_out": result.TimedOut,
	}, &durationMs, "")

	return result, nil
}

// maxSandboxCmdAttr bounds the command string recorded on the exec span.
const maxSandboxCmdAttr = 512

// sandboxExecOp derives a low-cardinality operation label (the command's binary
// basename) for the sandbox.exec span name. Falls back to "exec".
func sandboxExecOp(cmd []string) string {
	if len(cmd) == 0 {
		return "exec"
	}
	base := filepath.Base(cmd[0])
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "exec"
	}
	return base
}

// truncateForAttr bounds a string for use as a span attribute value.
func truncateForAttr(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// WriteFile writes content to a file in the session's sandbox.
// If the sandbox has died, stale state is purged and ErrSandboxNotRunning is returned.
func (m *SandboxManager) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no sandbox for session %s", sessionID)
	}

	defer m.touchLastUsed(sessionID)
	ctx, span := telemetry.StartSandboxFSSpan(ctx, "write", inst.ID)
	defer span.End()
	span.SetAttributes(
		attribute.String(attrs.SandboxFSPath, path),
		attribute.Int(attrs.SandboxFSBytes, len(content)),
	)
	err := m.backend.WriteFile(ctx, inst.ID, path, content)
	if err != nil {
		telemetry.RecordError(span, err)
		if errors.Is(err, ErrSandboxNotRunning) {
			m.purgeStaleSandbox(sessionID, inst.ID)
		}
		return err
	}
	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventFileWrite, "File written", map[string]interface{}{"path": path, "size": len(content)}, nil, "")
	return nil
}

// ReadFile reads content from a file in the session's sandbox.
// If the sandbox has died, stale state is purged and ErrSandboxNotRunning is returned.
func (m *SandboxManager) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}

	defer m.touchLastUsed(sessionID)
	ctx, span := telemetry.StartSandboxFSSpan(ctx, "read", inst.ID)
	defer span.End()
	span.SetAttributes(attribute.String(attrs.SandboxFSPath, path))
	data, err := m.backend.ReadFile(ctx, inst.ID, path)
	if err != nil {
		telemetry.RecordError(span, err)
		if errors.Is(err, ErrSandboxNotRunning) {
			m.purgeStaleSandbox(sessionID, inst.ID)
		}
		return nil, err
	}
	span.SetAttributes(attribute.Int(attrs.SandboxFSBytes, len(data)))
	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventFileRead, "File read", map[string]interface{}{"path": path}, nil, "")
	return data, nil
}

// ListFiles lists directory contents in the session's sandbox.
// If the sandbox has died, stale state is purged and ErrSandboxNotRunning is returned.
func (m *SandboxManager) ListFiles(ctx context.Context, sessionID, path string) ([]FileInfo, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}

	defer m.touchLastUsed(sessionID)
	ctx, span := telemetry.StartSandboxFSSpan(ctx, "list", inst.ID)
	defer span.End()
	span.SetAttributes(attribute.String(attrs.SandboxFSPath, path))
	files, err := m.backend.ListFiles(ctx, inst.ID, path)
	if err != nil {
		telemetry.RecordError(span, err)
		if errors.Is(err, ErrSandboxNotRunning) {
			m.purgeStaleSandbox(sessionID, inst.ID)
		}
		return nil, err
	}
	span.SetAttributes(attribute.Int(attrs.SandboxFSEntries, len(files)))
	return files, nil
}

// purgeStaleSandbox removes the in-memory state for a sandbox whose
// container/VM has died. This allows the next ensureSandbox / GetOrCreate
// call to provision a fresh replacement.
func (m *SandboxManager) purgeStaleSandbox(sessionID, sandboxID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst := m.instancesBySandbox[sandboxID]
	delete(m.instances, sessionID)
	delete(m.instancesBySandbox, sandboxID)

	// Clean up trooper reference if this was a persistent sandbox.
	if inst != nil && inst.Config.AgentID != "" {
		delete(m.troopers, inst.Config.AgentID)
	}

	logger.WithFields(
		"session_id", sessionID,
		"sandbox_id", sandboxID,
	).Warn("sandbox_manager: purged stale sandbox (container/VM is not running)")
}

// SearchFiles recursively searches for files matching a query under the given root path.
// It uses `find` via Exec so it works on all backends without interface changes.
func (m *SandboxManager) SearchFiles(ctx context.Context, sessionID, rootPath, query string, limit int) ([]FileInfo, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	defer m.touchLastUsed(sessionID)

	result, err := m.backend.Exec(ctx, inst.ID, ExecRequest{
		Command: []string{
			"find", rootPath,
			"-not", "-path", "*/node_modules/*",
			"-not", "-path", "*/.git/*",
			"-not", "-path", "*/__pycache__/*",
			"-not", "-path", "*/vendor/*",
			"-not", "-path", "*/.next/*",
			"-not", "-path", "*/dist/*",
			"-iname", "*" + query + "*",
			"-printf", "%y %s %p\\n",
		},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("search files failed: %w", err)
	}

	var files []FileInfo
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			continue
		}
		fileType := parts[0]
		filePath := parts[2]
		var size int64
		fmt.Sscanf(parts[1], "%d", &size)
		files = append(files, FileInfo{
			Name:  filepath.Base(filePath),
			Path:  filePath,
			Size:  size,
			IsDir: fileType == "d",
		})
		if len(files) >= limit {
			break
		}
	}

	return files, nil
}

// Destroy tears down the sandbox for the given session.
// For persistent troopers, this stops (sleeps) the sandbox instead of destroying it.
// Use TerminateSandbox to force-destroy a persistent trooper.
func (m *SandboxManager) Destroy(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	inst, ok := m.instances[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil // No sandbox to destroy
	}

	// For persistent troopers, stop (sleep) instead of destroying.
	// The trooper stays in m.troopers for future sessions.
	if inst.Persistent {
		delete(m.instances, sessionID)
		m.mu.Unlock()
		return m.StopSandbox(ctx, inst.ID)
	}

	delete(m.instances, sessionID)
	delete(m.instancesBySandbox, inst.ID)
	m.mu.Unlock()

	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventDestroyStart, "Destroying sandbox", map[string]interface{}{"reason": "manual"}, nil, "")

	logger.WithFields("session_id", sessionID, "sandbox_id", inst.ID).
		Info("sandbox_manager: destroying sandbox")

	shouldMeter := !inst.BillingStartedAt.IsZero()

	if shouldMeter {
		m.captureNetworkBytes(ctx, inst)
	}

	err := m.destroyBackendAndConfirmGone(ctx, inst.ID)
	meteredAt := time.Now()

	if shouldMeter && err == nil {
		if !m.recordUsageForInstance(ctx, inst, EventDestroyed, "manual", meteredAt) {
			meterErr := fmt.Errorf("failed to close sandbox compute billing window")
			m.markDestroyedWithReason(inst.ID, meterErr, "manual")
			return meterErr
		}
		inst.BillingStartedAt = time.Time{}
		inst.BillingEndedAt = time.Time{}
	}

	m.markDestroyed(inst.ID, err)
	m.registryDelete(ctx, inst.ID)
	m.closeAllPortMappings(inst.ID)
	m.disableCronsWebhooks(inst.ID)

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventDestroyed, "Sandbox destroyed", map[string]interface{}{"reason": "manual"}, nil, errStr)

	return err
}

// DestroyBySessionPrefix tears down every sandbox whose session/name starts
// with prefix. It checks the manager map first, then the backend's authoritative
// list so cleanup can reap sandboxes left behind by a crashed process.
func (m *SandboxManager) DestroyBySessionPrefix(ctx context.Context, prefix string) error {
	if prefix == "" {
		return nil
	}

	tracked := map[string]string{}
	m.mu.RLock()
	for sessionID, inst := range m.instances {
		if sandboxInstanceMatchesPrefix(inst, sessionID, prefix) {
			tracked[sessionID] = inst.ID
		}
	}
	m.mu.RUnlock()

	var errs []error
	destroyed := map[string]struct{}{}
	for sessionID, sandboxID := range tracked {
		if err := m.Destroy(ctx, sessionID); err != nil {
			errs = append(errs, err)
		}
		destroyed[sandboxID] = struct{}{}
	}

	instances, err := m.backend.List(ctx)
	if err != nil {
		errs = append(errs, err)
		return errors.Join(errs...)
	}

	for _, inst := range instances {
		if inst == nil || !sandboxInstanceMatchesPrefix(inst, inst.Config.SessionID, prefix) {
			continue
		}
		if _, ok := destroyed[inst.ID]; ok {
			continue
		}

		sessionID := inst.Config.SessionID
		if sessionID == "" {
			sessionID = inst.Name
		}
		if sessionID == "" {
			sessionID = inst.Config.Name
		}

		m.mu.Lock()
		for sid, live := range m.instances {
			if live.ID == inst.ID {
				delete(m.instances, sid)
			}
		}
		delete(m.instancesBySandbox, inst.ID)
		m.mu.Unlock()

		m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventDestroyStart, "Destroying sandbox", map[string]interface{}{"reason": "prefix_cleanup"}, nil, "")

		shouldMeter := !inst.BillingStartedAt.IsZero()
		if shouldMeter {
			m.captureNetworkBytes(ctx, inst)
		}

		destroyErr := m.destroyBackendAndConfirmGone(ctx, inst.ID)
		meteredAt := time.Now()
		if shouldMeter && destroyErr == nil {
			if !m.recordUsageForInstance(ctx, inst, EventDestroyed, "prefix_cleanup", meteredAt) {
				errs = append(errs, fmt.Errorf("close sandbox %s compute billing window", inst.ID))
				continue
			}
			inst.BillingStartedAt = time.Time{}
			inst.BillingEndedAt = time.Time{}
		}

		m.markDestroyed(inst.ID, destroyErr)
		m.registryDelete(ctx, inst.ID)
		m.closeAllPortMappings(inst.ID)
		m.disableCronsWebhooks(inst.ID)

		errStr := ""
		if destroyErr != nil {
			errStr = destroyErr.Error()
			errs = append(errs, destroyErr)
		}
		m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventDestroyed, "Sandbox destroyed", map[string]interface{}{"reason": "prefix_cleanup"}, nil, errStr)
		destroyed[inst.ID] = struct{}{}
	}

	return errors.Join(errs...)
}

func sandboxInstanceMatchesPrefix(inst *Instance, sessionID, prefix string) bool {
	if inst == nil {
		return false
	}
	return strings.HasPrefix(sessionID, prefix) ||
		strings.HasPrefix(inst.Config.SessionID, prefix) ||
		strings.HasPrefix(inst.Name, prefix) ||
		strings.HasPrefix(inst.Config.Name, prefix)
}

// DestroyAll tears down all active sandboxes. Called during graceful shutdown.
func (m *SandboxManager) DestroyAll(ctx context.Context) error {
	m.mu.Lock()
	instances := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		instances = append(instances, inst)
	}
	m.instances = make(map[string]*Instance)
	m.instancesBySandbox = make(map[string]*Instance)
	m.troopers = make(map[string]*Instance)
	m.mu.Unlock()
	m.clearRepoLocks()

	// Stop event writer first so buffered events drain before shutdown.
	m.stopEventWriter()

	// Stop periodic R2 snapshot loop before draining instances. Any
	// in-flight snapshot will continue to completion via its own ctx;
	// new sweeps won't start.
	m.StopSnapshotScheduler()

	if m.reaper != nil {
		m.reaper.Stop()
	}
	if m.lastUsedFlushStop != nil {
		m.lastUsedFlushStop()
	}

	var lastErr error
	stopped := 0
	destroyed := 0
	for _, inst := range instances {
		// Persistent troopers: stop (snapshot) instead of destroying on shutdown
		// so they can be revived on next startup.
		if inst.Persistent {
			if m.db != nil {
				if err := m.StopSandbox(ctx, inst.ID); err != nil {
					logger.WithFields("sandbox_id", inst.ID, "error", err.Error()).
						Warn("sandbox_manager: failed to stop persistent trooper during shutdown, destroying")
					if err := m.destroyAndMeterForShutdown(ctx, inst, "shutdown_stop_fallback"); err != nil {
						lastErr = err
					}
					destroyed++
				} else {
					stopped++
				}
				continue
			}
		}
		if err := m.destroyAndMeterForShutdown(ctx, inst, "shutdown"); err != nil {
			logger.WithFields("sandbox_id", inst.ID, "error", err.Error()).
				Warn("sandbox_manager: failed to destroy sandbox during shutdown")
			lastErr = err
		}
		destroyed++
	}

	logger.WithFields("destroyed", destroyed, "stopped_persistent", stopped).
		Info("sandbox_manager: shutdown complete")
	return lastErr
}

// destroyAndMeterForShutdown preserves the normal lifecycle billing contract
// for the graceful-shutdown paths that intentionally bypass the reconciler.
// The billing window closes only once the backend confirms compute is gone.
func (m *SandboxManager) destroyAndMeterForShutdown(ctx context.Context, inst *Instance, reason string) error {
	if inst == nil {
		return nil
	}
	wasBillable := !inst.BillingStartedAt.IsZero()
	if wasBillable {
		m.captureNetworkBytes(ctx, inst)
	}
	destroyErr := m.backend.Destroy(ctx, inst.ID)
	if destroyErr != nil {
		if _, statusErr := m.backend.Status(ctx, inst.ID); statusErr == nil {
			m.markDestroyedWithReason(inst.ID, destroyErr, "system")
			return destroyErr
		}
		logger.WithFields("sandbox_id", inst.ID, "error", destroyErr.Error()).
			Debug("sandbox_manager: shutdown destroy errored but compute is already absent")
	}
	endedAt := time.Now().UTC()
	if wasBillable {
		if !m.recordUsageForInstance(ctx, inst, EventDestroyed, reason, endedAt) {
			return fmt.Errorf("close sandbox compute billing window during shutdown")
		}
		inst.BillingStartedAt = time.Time{}
		inst.BillingEndedAt = time.Time{}
	}
	m.markDestroyedWithReason(inst.ID, nil, "system")
	return nil
}

// destroyBackendAndConfirmGone normalizes idempotent teardown. Backends can
// return an error when a resource disappeared between lookup and destroy; if a
// follow-up status probe also cannot find it, compute is gone and the caller
// may safely close the billing window. If status still succeeds, the original
// destroy error is preserved and billing must remain open.
func (m *SandboxManager) destroyBackendAndConfirmGone(ctx context.Context, sandboxID string) error {
	if err := m.backend.Destroy(ctx, sandboxID); err != nil {
		if _, statusErr := m.backend.Status(ctx, sandboxID); statusErr == nil {
			return err
		}
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Debug("sandbox_manager: destroy errored but backend confirms compute is absent")
	}
	return nil
}

// touchLastUsed updates the in-memory LastUsedAt timestamp for a session.
// The actual DB flush happens periodically via flushLastUsedTimestamps.
func (m *SandboxManager) touchLastUsed(sessionID string) {
	m.TouchActivity(sessionID, "")
}

// TouchActivity records user or agent activity for a sandbox session. Passive
// observation paths such as stats, logs, and overview polling should not call it.
//
// Besides the in-memory timestamp (flushed in batch every 30s), a
// rate-limited write-through pushes last_used_at straight to the DB so
// idle detection stays correct when the touch lands on a pod that is
// not the IdleChecker leader, or right before a pod restart drops the
// in-memory map.
func (m *SandboxManager) TouchActivity(sessionID, reason string) {
	var sandboxID string
	m.mu.Lock()
	if inst, ok := m.instances[sessionID]; ok {
		inst.LastUsedAt = time.Now()
		sandboxID = inst.ID
	}
	m.mu.Unlock()
	m.writeThroughLastUsed(sandboxID)
}

// TouchActivityBySandboxID is the sandbox-id variant for activity
// sources that do not carry a session id (the preview proxy).
func (m *SandboxManager) TouchActivityBySandboxID(sandboxID, reason string) {
	m.mu.Lock()
	if inst, ok := m.instancesBySandbox[sandboxID]; ok {
		inst.LastUsedAt = time.Now()
	}
	m.mu.Unlock()
	m.writeThroughLastUsed(sandboxID)
}

// writeThroughLastUsed pushes last_used_at to the DB at most once per
// 15s per sandbox (in-memory limiter first, WHERE clause as the
// cross-pod backstop). Asynchronous and best-effort: the batch flusher
// still runs every 30s.
func (m *SandboxManager) writeThroughLastUsed(sandboxID string) {
	if m.db == nil || sandboxID == "" {
		return
	}
	if v, ok := m.lastTouchWrite.Load(sandboxID); ok {
		if t, ok := v.(time.Time); ok && time.Since(t) < 15*time.Second {
			return
		}
	}
	m.lastTouchWrite.Store(sandboxID, time.Now())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = m.db.ExecContext(ctx, `
			UPDATE sandbox_instances
			SET last_used_at = NOW()
			WHERE id = $1
			  AND lifecycle_state = 'running'
			  AND (last_used_at IS NULL OR last_used_at < NOW() - interval '15 seconds')`, sandboxID)
	}()
}

// networkConfigChanged returns true when the requested sandbox config has a
// different network mode or allowed-hosts list than the running instance,
// meaning the sandbox must be recreated for the change to take effect.
func networkConfigChanged(inst *Instance, config SandboxConfig) bool {
	// Compare normalized modes. An unset mode means "allow" (the
	// backends normalize it the same way at create time), so "" vs
	// "allow" is NOT a change. Comparing the raw strings here put
	// sandboxes whose stored config and caller disagree only on
	// spelling into a permanent destroy+recreate loop: every
	// GetOrCreate call saw a "changed" network config and rebuilt the
	// VM from scratch.
	if normalizedNetworkMode(string(inst.Config.NetworkMode)) != normalizedNetworkMode(config.NetworkMode) {
		return true
	}
	// Compare allowed hosts (order-insensitive).
	oldHosts := inst.Config.AllowedHosts
	newHosts := config.AllowedHosts
	if len(oldHosts) != len(newHosts) {
		return true
	}
	oldSet := make(map[string]struct{}, len(oldHosts))
	for _, h := range oldHosts {
		oldSet[h] = struct{}{}
	}
	for _, h := range newHosts {
		if _, ok := oldSet[h]; !ok {
			return true
		}
	}
	return false
}

// normalizedNetworkMode maps the empty string onto the default mode
// ("allow") so config comparisons treat unset and explicit-default as
// equal. Mirrors normalizeNetworkMode in the firecracker backend.
func normalizedNetworkMode(mode string) string {
	if mode == "" {
		return string(NetworkAllow)
	}
	return mode
}

// resolveRetention determines the idle retention in seconds for a new sandbox.
// Priority: per-agent config override > plan-tier resolver > global default.
// A return value of 0 means no expiration (sandbox lives until manually destroyed).
// configOverride: -1 = no expiration, >0 = specific seconds, 0 = use plan tier / default.
func (m *SandboxManager) resolveRetention(tenantID string, configOverride int) int {
	if configOverride == -1 {
		// Explicit no-expiration requested by the user.
		return 0
	}
	if configOverride > 0 {
		return m.clampRetention(configOverride)
	}
	if m.retentionResolver != nil && tenantID != "" {
		d := m.retentionResolver.ResolveIdleRetention(tenantID)
		if d == 0 {
			// Explicit no-expiration for this plan tier.
			return 0
		}
		if d > 0 {
			return m.clampRetention(int(d.Seconds()))
		}
	}
	return m.clampRetention(m.globalConfig.DefaultIdleRetentionSecs)
}

// clampRetention caps retention at the configured maximum.
// A value of 0 is treated as no expiration and passed through unchanged.
func (m *SandboxManager) clampRetention(secs int) int {
	if secs == 0 {
		return 0
	}
	max := m.globalConfig.MaxIdleRetentionSecs
	if max > 0 && secs > max {
		return max
	}
	if secs < 0 {
		return m.globalConfig.DefaultIdleRetentionSecs
	}
	return secs
}

// startLastUsedFlusher runs flushLastUsedTimestamps on a fixed 30s
// cadence, independent of the legacy reaper. The flush used to be a
// side effect of the reaper sweep, so disabling the reaper (reconciler
// mode) would have starved the IdleChecker of fresh last_used_at data
// and idle-stopped active sandboxes.
func (m *SandboxManager) startLastUsedFlusher() {
	ctx, cancel := context.WithCancel(context.Background())
	m.lastUsedFlushStop = cancel
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.flushLastUsedTimestamps()
			}
		}
	}()
}

// flushLastUsedTimestamps batch-updates last_used_at in the database for all
// active instances. Called periodically by the flusher (every 30s, plus the
// legacy reaper sweep when that is enabled) so individual operations never
// hit the DB for timestamp updates.
func (m *SandboxManager) flushLastUsedTimestamps() {
	if m.db == nil {
		return
	}

	m.mu.RLock()
	type entry struct {
		id         string
		lastUsedAt time.Time
	}
	entries := make([]entry, 0, len(m.instances))
	for _, inst := range m.instances {
		if !inst.LastUsedAt.IsZero() {
			entries = append(entries, entry{id: inst.ID, lastUsedAt: inst.LastUsedAt})
		}
	}
	m.mu.RUnlock()

	if len(entries) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const q = `
		UPDATE sandbox_instances
		SET last_used_at = $1
		WHERE id = $2
		  AND status = 'running'
		  AND COALESCE(NULLIF(lifecycle_state, ''), 'running') = 'running'`
	for _, e := range entries {
		if _, err := m.db.ExecContext(ctx, q, e.lastUsedAt, e.id); err != nil {
			logger.WithFields("sandbox_id", e.id, "error", err.Error()).
				Debug("sandbox_manager: failed to flush last_used_at")
		}
	}
}

// ClampToGlobalLimits enforces global resource caps on a per-agent sandbox config.
func (m *SandboxManager) ClampToGlobalLimits(config SandboxConfig) SandboxConfig {
	return m.clampToGlobalLimitsForTenant(config, "")
}

// ClampToGlobalLimitsForTenant enforces both global caps and any
// per-tenant runtime override (set via SetTenantCapResolver). Per-tenant
// values that are smaller than the global cap take effect; tenant
// values larger than global are clamped down to global (the global cap
// is always the upper bound).
func (m *SandboxManager) ClampToGlobalLimitsForTenant(config SandboxConfig, tenantID string) SandboxConfig {
	return m.clampToGlobalLimitsForTenant(config, tenantID)
}

func (m *SandboxManager) clampToGlobalLimitsForTenant(config SandboxConfig, tenantID string) SandboxConfig {
	caps := m.effectiveCaps(tenantID)
	if caps.MaxCPU > 0 && config.CPULimit > caps.MaxCPU {
		config.CPULimit = caps.MaxCPU
	}
	if caps.MaxMemoryMB > 0 && config.MemoryMB > caps.MaxMemoryMB {
		config.MemoryMB = caps.MaxMemoryMB
	}
	if caps.MaxDiskMB > 0 && config.DiskMB > caps.MaxDiskMB {
		config.DiskMB = caps.MaxDiskMB
	}
	if caps.MaxTimeoutSecs > 0 && config.TimeoutSeconds > caps.MaxTimeoutSecs {
		config.TimeoutSeconds = caps.MaxTimeoutSecs
	}
	return config
}

// SetTenantCapResolver installs a function that returns the per-tenant
// caps from runtime_config. Wired at gateway startup once the rtconfig
// service is available; nil means "global caps only".
func (m *SandboxManager) SetTenantCapResolver(fn func(tenantID string) GlobalSandboxConfig) {
	if fn == nil {
		m.tenantCapResolver.Store(nil)
		return
	}
	m.tenantCapResolver.Store(&fn)
}

// SetTenantTierResolver installs a function that returns the plan tier for
// a tenant ("free", "basic", "pro", "enterprise"). recordUsageSnapshot
// uses this to apply the TierMultipliers discount to billed cost. Nil
// resolver leaves every tenant at the "free" / 1.0 multiplier.
func (m *SandboxManager) SetTenantTierResolver(fn func(tenantID string) string) {
	if fn == nil {
		m.tenantTierResolver.Store(nil)
		return
	}
	m.tenantTierResolver.Store(&fn)
}

// SetSandboxBillingResolver installs the managed-cloud sandbox billing gate.
// Passing nil disables central billing enforcement for self-hosted deployments.
func (m *SandboxManager) SetSandboxBillingResolver(fn func(tenantID string) bool) {
	if fn == nil {
		m.sandboxBillingResolver.Store(nil)
		return
	}
	m.sandboxBillingResolver.Store(&fn)
}

// SetManagedMachineProfilesRequired enables the fixed-size catalog for
// Everstack-hosted compute. Do not enable it for self-hosted deployments.
func (m *SandboxManager) SetManagedMachineProfilesRequired(required bool) {
	m.managedMachineProfiles.Store(required)
}

// ValidateSandboxMachineProfile applies the managed fixed-size and plan-tier
// policy. It is public so the async lifecycle path can validate before writing
// a pending allocation that the reconciler would otherwise provision later.
func (m *SandboxManager) ValidateSandboxMachineProfile(config SandboxConfig, tenantID string) error {
	if m == nil || !m.managedMachineProfiles.Load() {
		return nil
	}
	return ValidateManagedMachineProfile(config, m.resolveTenantTier(tenantID))
}

// RequireSandboxBilling rejects allocation/revival when the managed tenant has
// neither starter credit nor an active usage billing arrangement.
func (m *SandboxManager) RequireSandboxBilling(tenantID string) error {
	ptr := m.sandboxBillingResolver.Load()
	if ptr == nil {
		return nil
	}
	if strings.TrimSpace(tenantID) == "" || !(*ptr)(tenantID) {
		return ErrSandboxBillingRequired
	}
	return nil
}

// ConcurrentSandboxLimit returns the customer plan entitlement for one
// instance. GlobalConfig.MaxSandboxes remains a separate infrastructure safety
// ceiling and is intentionally not exposed through this method.
func (m *SandboxManager) ConcurrentSandboxLimit(tenantID string) int {
	if m == nil {
		return FreeConcurrentSandboxLimit
	}
	return ResolveConcurrentSandboxLimit(m.resolveTenantTier(tenantID))
}

// ConcurrentSandboxCount reports sandboxes that currently reserve compute
// capacity. Pending creates count immediately; sleeping, stopped, archived,
// failed, and terminated rows do not. The DB and in-memory IDs are unioned so
// a just-created placeholder cannot slip through the short persistence gap.
func (m *SandboxManager) ConcurrentSandboxCount(
	ctx context.Context,
	tenantID string,
	excludeSandboxID string,
) (int, error) {
	ids := make(map[string]struct{})
	if m != nil && m.db != nil && strings.TrimSpace(tenantID) != "" {
		var persisted []string
		if err := m.db.SelectContext(ctx, &persisted, `
			SELECT id
			FROM sandbox_instances
			WHERE tenant_id = $1
			  AND id <> $2
			  AND (
			    lifecycle_state IN (
			      'pending', 'creating', 'provisioning', 'running',
			      'stopping', 'reviving', 'terminating'
			    )
			    OR (billing_started_at IS NOT NULL AND billing_ended_at IS NULL)
			  )`,
			tenantID, excludeSandboxID,
		); err != nil {
			return 0, fmt.Errorf("count concurrent sandboxes: %w", err)
		}
		for _, id := range persisted {
			ids[id] = struct{}{}
		}
	}

	if m != nil {
		m.mu.RLock()
		for _, inst := range m.instancesBySandbox {
			if sandboxReservesConcurrentSlot(inst, tenantID, excludeSandboxID) {
				ids[inst.ID] = struct{}{}
			}
		}
		m.mu.RUnlock()
	}
	return len(ids), nil
}

// RequireConcurrentSandboxSlot fails before any backend allocation when the
// tenant has exhausted its per-instance plan quota.
func (m *SandboxManager) RequireConcurrentSandboxSlot(
	ctx context.Context,
	tenantID string,
	excludeSandboxID string,
) error {
	limit := m.ConcurrentSandboxLimit(tenantID)
	if limit == UnlimitedSandboxLimit {
		return nil
	}
	used, err := m.ConcurrentSandboxCount(ctx, tenantID, excludeSandboxID)
	if err != nil {
		return err
	}
	if used >= limit {
		return fmt.Errorf(
			"%w: %d of %d slots are allocated; stop or sleep a sandbox before starting another",
			ErrConcurrentSandboxLimit,
			used,
			limit,
		)
	}
	return nil
}

func sandboxReservesConcurrentSlot(
	inst *Instance,
	tenantID string,
	excludeSandboxID string,
) bool {
	if inst == nil || inst.ID == excludeSandboxID {
		return false
	}
	if strings.TrimSpace(tenantID) != "" && inst.Config.TenantID != tenantID {
		return false
	}
	if !inst.BillingStartedAt.IsZero() && inst.BillingEndedAt.IsZero() {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(inst.LifecycleState)) {
	case "pending", "creating", "provisioning", "running", "stopping", "reviving", "terminating":
		return true
	}
	return inst.Status == StatusPending || inst.Status == StatusRunning
}

// resolveTenantTier returns the plan tier for the given tenant, or "free"
// when no resolver is installed or the resolver returns an empty string.
func (m *SandboxManager) resolveTenantTier(tenantID string) string {
	ptr := m.tenantTierResolver.Load()
	if ptr == nil || tenantID == "" {
		return "free"
	}
	resolver := *ptr
	tier := resolver(tenantID)
	if tier == "" {
		return "free"
	}
	return tier
}

// effectiveCaps returns the caps that should apply for a given tenant.
// Each field is the smaller (more restrictive) of (global, tenant).
// Zero values from either source mean "no cap" for that field.
//
// Reads tenantCapResolver via atomic.Pointer, NOT via m.mu, because
// this function is called from getOrCreateImpl while m.mu is held as
// a writer. RLock here would deadlock (sync.RWMutex isn't re-entrant).
func (m *SandboxManager) effectiveCaps(tenantID string) GlobalSandboxConfig {
	resolverPtr := m.tenantCapResolver.Load()
	eff := m.globalConfig
	if resolverPtr == nil || tenantID == "" {
		return eff
	}
	resolver := *resolverPtr
	t := resolver(tenantID)
	if t.MaxCPU > 0 && (eff.MaxCPU == 0 || t.MaxCPU < eff.MaxCPU) {
		eff.MaxCPU = t.MaxCPU
	}
	if t.MaxMemoryMB > 0 && (eff.MaxMemoryMB == 0 || t.MaxMemoryMB < eff.MaxMemoryMB) {
		eff.MaxMemoryMB = t.MaxMemoryMB
	}
	if t.MaxDiskMB > 0 && (eff.MaxDiskMB == 0 || t.MaxDiskMB < eff.MaxDiskMB) {
		eff.MaxDiskMB = t.MaxDiskMB
	}
	if t.MaxTimeoutSecs > 0 && (eff.MaxTimeoutSecs == 0 || t.MaxTimeoutSecs < eff.MaxTimeoutSecs) {
		eff.MaxTimeoutSecs = t.MaxTimeoutSecs
	}
	return eff
}

// GlobalConfig returns the global sandbox configuration.
func (m *SandboxManager) GlobalConfig() GlobalSandboxConfig {
	return m.globalConfig
}

// Healthy checks whether the underlying backend is operational.
func (m *SandboxManager) Healthy(ctx context.Context) error {
	return m.backend.Healthy(ctx)
}

// BackendName returns the name of the underlying backend.
func (m *SandboxManager) BackendName() string {
	return m.backend.Name()
}

// Backend returns the underlying backend handle. Exposed so the
// orchestrator/sandbox reconciler can drive Create/Destroy/Status
// directly without going through manager-level locks. Returns nil
// when the manager itself is not configured.
func (m *SandboxManager) Backend() Backend {
	if m == nil {
		return nil
	}
	return m.backend
}

// BackendStatus checks whether a sandbox actually exists at the backend level
// (Docker container, K8s pod, etc.). Returns the instance if found, or an error
// if the sandbox is gone. Used during startup reconciliation.
func (m *SandboxManager) BackendStatus(ctx context.Context, sandboxID string) (*Instance, error) {
	return m.backend.Status(ctx, sandboxID)
}

// SeedRoute restores a durable backend route for remote routed backends such
// as firecracker-agent. It is a no-op for local backends.
func (m *SandboxManager) SeedRoute(sandboxID, target string) {
	if m == nil || m.backend == nil {
		return
	}
	if seeder, ok := m.backend.(RouteSeeder); ok {
		seeder.SeedRoute(sandboxID, target)
	}
}

func (m *SandboxManager) seedRouteFromInstance(inst *Instance) {
	if inst == nil || strings.TrimSpace(inst.AgentTarget) == "" {
		return
	}
	m.SeedRoute(inst.ID, inst.AgentTarget)
}

func (m *SandboxManager) DescribePending(ctx context.Context, sandboxID string) string {
	if m == nil || m.backend == nil {
		return ""
	}
	return m.backend.DescribePending(ctx, sandboxID)
}

// TriggerRecoverySweep runs an immediate reaper sweep after startup/bootstrap.
// This is useful on server restarts so stale persisted sandbox state is cleaned
// up only after the manager has been fully wired with DB and backend health.
func (m *SandboxManager) TriggerRecoverySweep() {
	if m == nil || m.reaper == nil {
		return
	}
	m.reaper.sweep()
}

// BackendDestroy destroys a sandbox at the backend level while still honoring
// an open compute billing window. Used during startup reconciliation to clean
// up stopped/orphaned sandboxes before re-provisioning.
func (m *SandboxManager) BackendDestroy(ctx context.Context, sandboxID string) error {
	inst, _ := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID)
	if inst != nil && !inst.BillingStartedAt.IsZero() {
		m.captureNetworkBytes(ctx, inst)
	}
	destroyErr := m.backend.Destroy(ctx, sandboxID)
	if destroyErr != nil {
		if _, statusErr := m.backend.Status(ctx, sandboxID); statusErr == nil {
			m.markDestroyedWithReason(sandboxID, destroyErr, "system")
			return destroyErr
		}
		// Cleanup callers routinely probe both current and legacy IDs. A
		// backend-confirmed absence is successful idempotent cleanup.
	}
	if inst != nil && !inst.BillingStartedAt.IsZero() {
		if !m.recordUsageForInstance(ctx, inst, EventDestroyed, "reprovision_cleanup", time.Now().UTC()) {
			return fmt.Errorf("close sandbox compute billing window before reprovision")
		}
		inst.BillingStartedAt = time.Time{}
		inst.BillingEndedAt = time.Time{}
	}
	m.markDestroyedWithReason(sandboxID, nil, "system")
	return nil
}

// ListInstances returns a snapshot of all active sandbox instances.
func (m *SandboxManager) ListInstances() []*Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		out = append(out, inst)
	}
	return out
}

// ListInstancesForTenant returns only sandboxes owned by one effective
// instance tenant. Customer-facing capacity must never be computed from the
// process-wide registry because a shared runtime can host many instances.
func (m *SandboxManager) ListInstancesForTenant(tenantID string) []*Instance {
	scope := TenantInstanceScope{TenantID: tenantID}
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*Instance, 0)
	for _, inst := range m.instancesBySandbox {
		if scope.MatchesInstance(inst) {
			out = append(out, inst)
		}
	}
	return out
}

// StopTenantSandboxes stops every running sandbox for one managed tenant.
// Billing enforcement uses this after the central service reports that the
// organization's one-time starter credit is exhausted.
func (m *SandboxManager) StopTenantSandboxes(ctx context.Context, tenantID string) error {
	if m == nil || strings.TrimSpace(tenantID) == "" {
		return nil
	}
	sandboxIDs := make(map[string]struct{})
	for _, inst := range m.ListInstancesForTenant(tenantID) {
		if inst != nil &&
			(inst.Status == StatusRunning || inst.LifecycleState == LifecycleRunning) {
			sandboxIDs[inst.ID] = struct{}{}
		}
	}
	if m.db != nil {
		var persistedIDs []string
		if err := m.db.SelectContext(ctx, &persistedIDs, `
			SELECT id
			FROM sandbox_instances
			WHERE tenant_id = $1
			  AND (
				COALESCE(lifecycle_state, '') = 'running'
				OR COALESCE(status, '') = 'running'
			  )`, tenantID); err != nil {
			return fmt.Errorf("list running tenant sandboxes: %w", err)
		}
		for _, sandboxID := range persistedIDs {
			sandboxIDs[sandboxID] = struct{}{}
		}
	}

	var stopErrors []error
	for sandboxID := range sandboxIDs {
		if err := m.StopSandbox(ctx, sandboxID); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("stop sandbox %s: %w", sandboxID, err))
		}
	}
	return errors.Join(stopErrors...)
}

// GetInstance returns the sandbox instance for a session, if any.
func (m *SandboxManager) GetInstance(sessionID string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[sessionID]
	return inst, ok
}

// TrooperExists returns true if a trooper sandbox is tracked in-memory for the
// given agent ID.
func (m *SandboxManager) TrooperExists(agentID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.troopers[agentID]
	return ok
}

// SandboxExistsByID returns true if a sandbox with the given ID is tracked
// in-memory (by sandbox ID, not session ID).
func (m *SandboxManager) SandboxExistsByID(sandboxID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.instancesBySandbox[sandboxID]
	return ok
}

// Logs returns a log stream for the sandbox in the given session.
func (m *SandboxManager) Logs(ctx context.Context, sessionID string, opts LogsOptions) (io.ReadCloser, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}

	// Passive log reads (UI tailing) should not keep a sandbox alive.
	return m.backend.Logs(ctx, inst.ID, opts)
}

// Stats returns a resource usage snapshot for the sandbox in the given session.
func (m *SandboxManager) Stats(ctx context.Context, sessionID string) (*ContainerStats, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}

	// Polling stats from dashboards/health checks must not refresh idle timers.
	return m.backend.Stats(ctx, inst.ID)
}

// snapshotInstancesForReap returns a copy of the manager's instance
// map so the session reaper can iterate without holding the manager
// lock for the full sweep. Pending placeholders are skipped — they
// have no live VM to query yet. Sleeping instances are included so
// the reaper can read their lifecycle state and skip vsock calls
// itself (rather than this method silently dropping them).
func (m *SandboxManager) snapshotInstancesForReap() []*Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		if inst == nil || inst.Status == StatusPending {
			continue
		}
		out = append(out, inst)
	}
	return out
}

// Shell opens an interactive shell for the sandbox in the given session.
// shellSessionID is optional — when set the backend reattaches to that
// persistent shell session (so a client reconnect resumes the same
// shell with cwd/jobs/scrollback intact); when empty a new persistent
// session is created and its ID comes back on the returned
// ShellSession.ShellSessionID.
func (m *SandboxManager) Shell(ctx context.Context, sessionID string, cmd []string, shellSessionID string) (*ShellSession, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		// Gateway pod restart drops the in-memory cache; the actual VM
		// still lives on fcagent. Fall back to a DB lookup so the shell
		// is reachable from the UI even after a rollout.
		dbInst, err := m.lookupInstanceBySessionFromDB(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("no sandbox for session %s: %w", sessionID, err)
		}
		inst = dbInst
	}

	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventShellOpen, "Shell session opened", nil, nil, "")

	m.TouchActivity(sessionID, "shell_open")
	sess, err := m.openShell(ctx, inst.ID, shellSessionID, cmd)
	if err != nil {
		return nil, err
	}
	sess.Conn = &activityConn{
		ReadWriteCloser: sess.Conn,
		touch: func() {
			m.TouchActivity(sessionID, "shell_input")
		},
	}
	return sess, nil
}

// ShellBySandboxID opens an interactive shell using the sandbox ID (not session ID).
// shellSessionID is optional — see Shell for semantics.
func (m *SandboxManager) ShellBySandboxID(ctx context.Context, sandboxID string, cmd []string, shellSessionID string) (*ShellSession, error) {
	inst, ok := m.GetBySandboxID(sandboxID)
	if !ok {
		return nil, fmt.Errorf("no sandbox with id %s", sandboxID)
	}

	m.recordEvent(inst.ID, inst.Config.SessionID, inst.Config.TenantID, EventShellOpen, "Shell session opened (SSH)", nil, nil, "")

	m.TouchActivity(inst.Config.SessionID, "ssh_shell_open")
	sess, err := m.openShell(ctx, sandboxID, shellSessionID, cmd)
	if err != nil {
		return nil, err
	}
	sess.Conn = &activityConn{
		ReadWriteCloser: sess.Conn,
		touch: func() {
			m.TouchActivity(inst.Config.SessionID, "ssh_shell_input")
		},
	}
	return sess, nil
}

// openShell routes to the persistent-session path when the backend
// supports it; falls back to the legacy Shell call otherwise so
// Docker / Kubernetes backends keep working unchanged.
func (m *SandboxManager) openShell(ctx context.Context, sandboxID, shellSessionID string, cmd []string) (*ShellSession, error) {
	caps := CapabilitiesForBackend(m.backend)
	if !caps.Features.PersistentShell {
		if shellSessionID != "" {
			// Caller asked for reattach but backend doesn't know about
			// sessions. Bubble up as an error rather than silently
			// starting a fresh shell — otherwise the user would think
			// they resumed when they didn't.
			return nil, fmt.Errorf("backend does not support persistent shell sessions")
		}
		return m.backend.Shell(ctx, sandboxID, cmd)
	}
	if psb, ok := m.backend.(PersistentShellBackend); ok {
		return psb.ShellWithSession(ctx, sandboxID, shellSessionID, cmd)
	}
	return nil, fmt.Errorf("backend advertises persistent shell sessions but does not implement them")
}

// ListShellSessions returns the persistent shell sessions for a sandbox.
// Returns an empty slice when the backend doesn't support sessions.
func (m *SandboxManager) ListShellSessions(ctx context.Context, sandboxID string) ([]ShellSessionInfo, error) {
	if !CapabilitiesForBackend(m.backend).Features.PersistentShell {
		return []ShellSessionInfo{}, nil
	}
	if psb, ok := m.backend.(PersistentShellBackend); ok {
		return psb.ListShellSessions(ctx, sandboxID)
	}
	return nil, fmt.Errorf("backend advertises persistent shell sessions but does not implement them")
}

// KillShellSession terminates a persistent shell session.
func (m *SandboxManager) KillShellSession(ctx context.Context, sandboxID, shellSessionID string) error {
	if !CapabilitiesForBackend(m.backend).Features.PersistentShell {
		return nil
	}
	if psb, ok := m.backend.(PersistentShellBackend); ok {
		return psb.KillShellSession(ctx, sandboxID, shellSessionID)
	}
	return fmt.Errorf("backend advertises persistent shell sessions but does not implement them")
}

type activityConn struct {
	io.ReadWriteCloser
	touch func()
}

func (c *activityConn) Write(p []byte) (int, error) {
	n, err := c.ReadWriteCloser.Write(p)
	if n > 0 && c.touch != nil {
		c.touch()
	}
	return n, err
}

// persistInstance upserts the sandbox instance record into the database.
// Errors are logged but not returned — DB persistence is best-effort so
// that the in-memory + Docker-label path always works as a fallback.
func (m *SandboxManager) persistInstance(inst *Instance) {
	if m.db == nil {
		return
	}

	configJSON, err := json.Marshal(inst.Config)
	if err != nil {
		configJSON = []byte("{}")
	}

	var expiresAt *time.Time
	if !inst.ExpiresAt.IsZero() {
		expiresAt = &inst.ExpiresAt
	}

	var lastUsedAt *time.Time
	if !inst.LastUsedAt.IsZero() {
		lastUsedAt = &inst.LastUsedAt
	}

	var billingStartedAt *time.Time
	if !inst.BillingStartedAt.IsZero() {
		billingStartedAt = &inst.BillingStartedAt
	}

	lifecycleState := inst.LifecycleState
	if lifecycleState == "" {
		lifecycleState = LifecycleRunning
	}

	instanceID := strings.TrimSpace(inst.InstanceID)
	if instanceID == "" {
		instanceID = strings.TrimSpace(inst.Config.TenantID)
	}

	const q = `
		INSERT INTO sandbox_instances
			(id, session_id, tenant_id, instance_id, backend, container_id, image, status, config, created_at, expires_at, last_used_at, idle_retention_secs, name,
			 git_repo_url, git_branch, git_commit_sha, git_installation_id, keep_warm,
				 agent_id, persistent, lifecycle_state, agent_target, billing_started_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
		        $15, $16, $17, $18, $19,
			        $20, $21, $22, $23, $24)
		ON CONFLICT (id) DO UPDATE SET
			status              = EXCLUDED.status,
			instance_id         = COALESCE(EXCLUDED.instance_id, sandbox_instances.instance_id),
			container_id        = EXCLUDED.container_id,
			config              = EXCLUDED.config,
			expires_at          = EXCLUDED.expires_at,
			last_used_at        = EXCLUDED.last_used_at,
			idle_retention_secs = EXCLUDED.idle_retention_secs,
			lifecycle_state     = CASE
				-- Keep active transition ownership to avoid racing lifecycle ops.
				WHEN sandbox_instances.lifecycle_state IN ('stopping', 'reviving', 'terminating')
				THEN sandbox_instances.lifecycle_state
				-- Recreated/running sandbox with same ID must reset terminal/stopped rows
				-- back to running; otherwise reaper stop claims are permanently rejected.
				WHEN EXCLUDED.status = 'running'
				THEN EXCLUDED.lifecycle_state
				WHEN sandbox_instances.lifecycle_state IN ('stopped', 'terminated', 'failed')
				THEN sandbox_instances.lifecycle_state
				ELSE EXCLUDED.lifecycle_state
			END,
			desired_state       = CASE
				-- A direct recreate reuses the deterministic sandbox ID. Once
				-- the replacement is running it must clear the terminal desired
				-- state written by the preceding destroy, otherwise lifecycle
				-- policy can never manage the replacement correctly.
				WHEN EXCLUDED.status = 'running'
				 AND sandbox_instances.lifecycle_state IN ('stopped', 'terminated', 'failed')
				THEN 'running'
				ELSE sandbox_instances.desired_state
			END,
			reconcile_after     = CASE
				WHEN EXCLUDED.status = 'running'
				 AND sandbox_instances.lifecycle_state IN ('stopped', 'terminated', 'failed')
				THEN NOW() + INTERVAL '24 hours'
				ELSE sandbox_instances.reconcile_after
			END,
			destroyed_at        = CASE
				WHEN EXCLUDED.status = 'running' THEN NULL
				ELSE sandbox_instances.destroyed_at
			END,
			destroy_reason      = CASE
				WHEN EXCLUDED.status = 'running' THEN NULL
				ELSE sandbox_instances.destroy_reason
			END,
			name                = CASE
				WHEN EXCLUDED.name <> '' THEN EXCLUDED.name
				ELSE sandbox_instances.name
			END,
			git_repo_url        = EXCLUDED.git_repo_url,
			git_branch          = EXCLUDED.git_branch,
			git_commit_sha      = EXCLUDED.git_commit_sha,
			git_installation_id = EXCLUDED.git_installation_id,
			keep_warm           = EXCLUDED.keep_warm,
				agent_id            = EXCLUDED.agent_id,
				persistent          = EXCLUDED.persistent,
				agent_target        = COALESCE(EXCLUDED.agent_target, sandbox_instances.agent_target),
				billing_started_at  = CASE
					WHEN EXCLUDED.billing_started_at IS NOT NULL THEN EXCLUDED.billing_started_at
					WHEN EXCLUDED.status IN ('stopped', 'sleeping', 'terminated', 'failed') THEN NULL
					ELSE sandbox_instances.billing_started_at
				END,
				billing_ended_at = CASE
					WHEN EXCLUDED.billing_started_at IS NOT NULL THEN NULL
					WHEN EXCLUDED.status IN ('stopped', 'sleeping', 'terminated', 'failed') THEN NULL
					ELSE sandbox_instances.billing_ended_at
				END`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := m.db.ExecContext(ctx, q,
		inst.ID,
		inst.Config.SessionID,
		inst.Config.TenantID,
		nullableString(instanceID),
		inst.Backend,
		inst.ContainerID,
		inst.Config.Image,
		string(inst.Status),
		configJSON,
		inst.CreatedAt,
		expiresAt,
		lastUsedAt,
		inst.IdleRetentionSecs,
		preferredSandboxName(inst),
		nullableString(strings.TrimSpace(inst.Config.GitRepoURL)),
		nullableString(strings.TrimSpace(inst.Config.GitBranch)),
		nullableString(strings.TrimSpace(inst.Config.GitCommitSHA)),
		nullableInt64(inst.Config.GitInstallationID),
		inst.KeepWarm,
		nullableString(strings.TrimSpace(inst.AgentID)),
		inst.Persistent,
		lifecycleState,
		nullableString(strings.TrimSpace(inst.AgentTarget)),
		billingStartedAt,
	); err != nil {
		logger.WithFields("sandbox_id", inst.ID, "error", err.Error()).
			Warn("sandbox_manager: failed to persist instance to DB")
	}
}

func preferredSandboxName(inst *Instance) string {
	if inst == nil {
		return ""
	}
	if name := strings.TrimSpace(inst.Name); name != "" {
		return name
	}
	return strings.TrimSpace(inst.Config.Name)
}

// markDestroyed updates the DB record for a destroyed sandbox.
func (m *SandboxManager) markDestroyed(sandboxID string, destroyErr error) {
	m.markDestroyedWithReason(sandboxID, destroyErr, "manual")
}

// markDestroyedWithReason updates the DB record for a destroyed sandbox with a specific reason.
// Valid reasons: "manual", "expired", "error", "system".
func (m *SandboxManager) markDestroyedWithReason(sandboxID string, destroyErr error, reason string) {
	if m.db == nil {
		return
	}

	reconcilerOwned := m.delegateLifecycleToReconciler()
	status := "stopped"
	var errStr *string
	if destroyErr != nil {
		status = "failed"
		if reconcilerOwned {
			// The direct destroy already expressed terminal intent. Leave a
			// claimable transition behind so the reconciler retries cleanup
			// instead of the health loop reviving the resource.
			status = "terminating"
		}
		s := destroyErr.Error()
		errStr = &s
	}

	const q = `
		UPDATE sandbox_instances
		SET status = $1,
		    destroyed_at = CASE WHEN $5 THEN NOW() ELSE destroyed_at END,
		    error = $2, destroy_reason = $3,
		    billing_started_at = CASE WHEN $5 THEN NULL ELSE billing_started_at END,
		    billing_ended_at = CASE WHEN $5 THEN NULL ELSE billing_ended_at END,
		    desired_state = CASE WHEN $6 THEN 'terminated' ELSE desired_state END,
		    lifecycle_state = CASE
		        WHEN NOT $6 THEN lifecycle_state
		        WHEN $5 THEN 'terminated'
		        ELSE 'terminating'
		    END,
		    reconcile_after = CASE
		        WHEN $6 AND NOT $5 THEN NOW()
		        WHEN $6 THEN NOW() + INTERVAL '24 hours'
		        ELSE reconcile_after
		    END,
		    reconcile_locked_by = CASE WHEN $6 THEN NULL ELSE reconcile_locked_by END,
		    reconcile_locked_at = CASE WHEN $6 THEN NULL ELSE reconcile_locked_at END
		WHERE id = $4`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := m.db.ExecContext(ctx, q, status, errStr, reason, sandboxID, destroyErr == nil, reconcilerOwned); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to mark instance destroyed in DB")
	}
}

// DestroyWithReason tears down the sandbox and records the destroy reason.
// For persistent troopers, this stops (sleeps) instead of destroying.
func (m *SandboxManager) DestroyWithReason(ctx context.Context, sessionID, reason string) error {
	m.mu.Lock()
	inst, ok := m.instances[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil
	}

	// For persistent troopers, stop (sleep) instead of destroying.
	if inst.Persistent {
		delete(m.instances, sessionID)
		m.mu.Unlock()
		return m.StopSandbox(ctx, inst.ID)
	}

	delete(m.instances, sessionID)
	delete(m.instancesBySandbox, inst.ID)
	m.mu.Unlock()

	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventDestroyStart, "Destroying sandbox", map[string]interface{}{"reason": reason}, nil, "")

	logger.WithFields("session_id", sessionID, "sandbox_id", inst.ID, "reason", reason).
		Info("sandbox_manager: destroying sandbox")

	shouldMeter := !inst.BillingStartedAt.IsZero()

	// Sample the network counters while the VM is still alive — they're
	// gone once Destroy returns.
	if shouldMeter {
		m.captureNetworkBytes(ctx, inst)
	}

	err := m.destroyBackendAndConfirmGone(ctx, inst.ID)
	meteredAt := time.Now()

	if shouldMeter && err == nil {
		if !m.recordUsageForInstance(ctx, inst, EventDestroyed, reason, meteredAt) {
			meterErr := fmt.Errorf("failed to close sandbox compute billing window")
			m.markDestroyedWithReason(inst.ID, meterErr, reason)
			return meterErr
		}
		inst.BillingStartedAt = time.Time{}
		inst.BillingEndedAt = time.Time{}
	}

	m.markDestroyedWithReason(inst.ID, err, reason)
	m.closeAllPortMappings(inst.ID)
	m.disableCronsWebhooks(inst.ID)

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventDestroyed, "Sandbox destroyed", map[string]interface{}{"reason": reason}, nil, errStr)

	return err
}

// GetInstanceConfig retrieves the stored config for a sandbox instance by sandbox ID.
// Used for recreating expired sandboxes with the same configuration.
// IsSandboxRecoverable reports whether a sandbox the backend can no
// longer find is expected to come back on its own. True when the
// lifecycle row still wants it running (desired_state='running') and it
// hasn't been (or isn't being) terminated — i.e. the HealthSweeper +
// RecoveryChecker will revive it. Callers that observe a "VM gone" error
// (e.g. the shell WebSocket) use this to decide whether to tell the
// client to keep reconnecting through the restart ("recovering") or to
// give up ("gone"). Best-effort: a nil DB, missing row, or query error
// returns false (treat as terminal — safer than telling the client to
// retry a sandbox that will never return).
func (m *SandboxManager) IsSandboxRecoverable(ctx context.Context, sandboxID string) bool {
	if m.db == nil || sandboxID == "" {
		return false
	}
	var row struct {
		Desired   string `db:"desired_state"`
		Lifecycle string `db:"lifecycle_state"`
	}
	const q = `SELECT COALESCE(desired_state, '') AS desired_state,
	                  COALESCE(lifecycle_state, '') AS lifecycle_state
	             FROM sandbox_instances WHERE id = $1`
	if err := m.db.GetContext(ctx, &row, q, sandboxID); err != nil {
		return false
	}
	if row.Desired != "running" {
		return false
	}
	switch row.Lifecycle {
	case "terminating", "terminated":
		return false
	}
	return true
}

func (m *SandboxManager) GetInstanceConfig(ctx context.Context, sandboxID string) (*InstanceConfig, string, error) {
	if m.db == nil {
		return nil, "", fmt.Errorf("database not available")
	}

	var row struct {
		Config []byte `db:"config"`
		Image  string `db:"image"`
	}
	const q = `SELECT config, image FROM sandbox_instances WHERE id = $1`
	if err := m.db.GetContext(ctx, &row, q, sandboxID); err != nil {
		return nil, "", fmt.Errorf("sandbox instance not found: %w", err)
	}

	var cfg InstanceConfig
	if err := json.Unmarshal(row.Config, &cfg); err != nil {
		return nil, "", fmt.Errorf("failed to parse instance config: %w", err)
	}

	// Ensure image is populated (may be stored separately)
	if cfg.Image == "" {
		cfg.Image = row.Image
	}

	return &cfg, cfg.SessionID, nil
}

// LookupInstanceBySession is the exported wrapper around the DB-fallback
// path so HTTP handlers (sandbox_shell, etc.) can reach a sandbox that
// was created before the current gateway pod existed.
func (m *SandboxManager) LookupInstanceBySession(ctx context.Context, sessionID string) (*Instance, error) {
	return m.lookupInstanceBySessionFromDB(ctx, sessionID)
}

// lookupTrooperByAgentIDFromDB returns the canonical persistent trooper
// row for an agent (sandbox id `wks_<agentID>`) when one exists in DB.
// Returns nil for missing rows or terminal-state rows; the caller then
// proceeds with fresh creation. Used by GetOrCreateTrooper's pod-
// restart fallback to avoid duplicating troopers (issue #6).
func (m *SandboxManager) lookupTrooperByAgentIDFromDB(ctx context.Context, agentID string) *Instance {
	if m.db == nil || agentID == "" {
		return nil
	}
	canonicalID := "wks_" + agentID
	var row struct {
		ID             string         `db:"id"`
		InstanceID     sql.NullString `db:"instance_id"`
		ContainerID    sql.NullString `db:"container_id"`
		Backend        string         `db:"backend"`
		Status         string         `db:"status"`
		LifecycleState sql.NullString `db:"lifecycle_state"`
		CreatedAt      time.Time      `db:"created_at"`
		BillingStarted sql.NullTime   `db:"billing_started_at"`
		BillingEnded   sql.NullTime   `db:"billing_ended_at"`
		Name           string         `db:"name"`
		AgentID        sql.NullString `db:"agent_id"`
		Persistent     sql.NullBool   `db:"persistent"`
		Config         []byte         `db:"config"`
		Image          string         `db:"image"`
		AgentTarget    sql.NullString `db:"agent_target"`
	}
	const q = `
		SELECT id, instance_id, container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name,
		       agent_id, persistent, config, image, agent_target
		FROM sandbox_instances
		WHERE id = $1
		  AND COALESCE(lifecycle_state, '') NOT IN ('terminated', 'failed')
		LIMIT 1`
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := m.db.GetContext(queryCtx, &row, q, canonicalID); err != nil {
		return nil
	}
	var cfg InstanceConfig
	if len(row.Config) > 0 {
		_ = json.Unmarshal(row.Config, &cfg)
	}
	if cfg.Image == "" {
		cfg.Image = row.Image
	}
	if cfg.AgentID == "" {
		cfg.AgentID = agentID
	}
	inst := &Instance{
		ID:               row.ID,
		InstanceID:       row.InstanceID.String,
		ContainerID:      row.ContainerID.String,
		Backend:          row.Backend,
		Status:           Status(row.Status),
		LifecycleState:   row.LifecycleState.String,
		CreatedAt:        row.CreatedAt,
		BillingStartedAt: nullTimeValue(row.BillingStarted),
		BillingEndedAt:   nullTimeValue(row.BillingEnded),
		Config:           cfg,
		Name:             row.Name,
		AgentID:          row.AgentID.String,
		Persistent:       row.Persistent.Bool,
		AgentTarget:      row.AgentTarget.String,
	}
	m.seedRouteFromInstance(inst)
	return inst
}

// LookupInstanceByIDFromDB is the exported wrapper that reconstructs an
// Instance from the DB keyed on sandbox id (for ID-or-name shell access
// when m.instances is empty post-restart).
func (m *SandboxManager) LookupInstanceByIDFromDB(ctx context.Context, sandboxID string) (*Instance, error) {
	return m.LookupInstanceByIDFromDBScoped(ctx, sandboxID, "")
}

// LookupInstanceByIDFromDBScoped is the tenant-scoped form of
// LookupInstanceByIDFromDB. When tenantID is non-empty the query adds an
// `AND tenant_id = $2` predicate so a caller cannot resolve another
// tenant/instance's sandbox by id, name, or short_code (the name/short_code
// match is otherwise cross-tenant). When tenantID is empty the lookup is
// unscoped — reserved for internal/system callers (reconciler, sweepers) that
// have no tenant context. Authenticated request paths MUST pass a non-empty
// tenantID.
//
// In cloud multi-instance mode the context tenant_id is the instance_id, so
// this enforces instance isolation; in self-hosted it is the org id.
func (m *SandboxManager) LookupInstanceByIDFromDBScoped(ctx context.Context, sandboxID, tenantID string) (*Instance, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	var row struct {
		ID             string         `db:"id"`
		InstanceID     sql.NullString `db:"instance_id"`
		ContainerID    sql.NullString `db:"container_id"`
		Backend        string         `db:"backend"`
		Status         string         `db:"status"`
		LifecycleState sql.NullString `db:"lifecycle_state"`
		CreatedAt      time.Time      `db:"created_at"`
		BillingStarted sql.NullTime   `db:"billing_started_at"`
		BillingEnded   sql.NullTime   `db:"billing_ended_at"`
		Name           string         `db:"name"`
		AgentID        sql.NullString `db:"agent_id"`
		Persistent     sql.NullBool   `db:"persistent"`
		Config         []byte         `db:"config"`
		Image          string         `db:"image"`
		ShortCode      sql.NullString `db:"short_code"`
		AgentTarget    sql.NullString `db:"agent_target"`
	}
	var err error
	if tenantID != "" {
		const qScoped = `
			SELECT id, instance_id, container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent, config, image, short_code, agent_target
			FROM sandbox_instances
			WHERE (id = $1 OR name = $1 OR short_code = $1) AND tenant_id = $2
			LIMIT 1`
		err = m.db.GetContext(ctx, &row, qScoped, sandboxID, tenantID)
	} else {
		const q = `
			SELECT id, instance_id, container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent, config, image, short_code, agent_target
			FROM sandbox_instances
			WHERE id = $1 OR name = $1 OR short_code = $1
			LIMIT 1`
		err = m.db.GetContext(ctx, &row, q, sandboxID)
	}
	if err != nil {
		return nil, fmt.Errorf("sandbox lookup failed: %w", err)
	}
	var cfg InstanceConfig
	if err := json.Unmarshal(row.Config, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	if cfg.Image == "" {
		cfg.Image = row.Image
	}
	inst := &Instance{
		ID:               row.ID,
		InstanceID:       row.InstanceID.String,
		ContainerID:      row.ContainerID.String,
		Backend:          row.Backend,
		Status:           Status(row.Status),
		LifecycleState:   row.LifecycleState.String,
		CreatedAt:        row.CreatedAt,
		BillingStartedAt: nullTimeValue(row.BillingStarted),
		BillingEndedAt:   nullTimeValue(row.BillingEnded),
		Config:           cfg,
		Name:             row.Name,
		AgentID:          row.AgentID.String,
		Persistent:       row.Persistent.Bool,
		ShortCode:        row.ShortCode.String,
		AgentTarget:      row.AgentTarget.String,
	}
	m.seedRouteFromInstance(inst)
	return inst, nil
}

// LookupInstanceByIDFromDBInScope is the canonical scope-shaped wrapper for
// public callers. It fails closed when the supplied scope has no effective
// sandbox owner and otherwise delegates to the legacy tenant-scoped query.
func (m *SandboxManager) LookupInstanceByIDFromDBInScope(ctx context.Context, sandboxID string, scope TenantInstanceScope) (*Instance, error) {
	owner := scope.SandboxTenantID()
	if owner == "" {
		return nil, fmt.Errorf("sandbox lookup requires tenant or instance scope")
	}
	return m.LookupInstanceByIDFromDBScoped(ctx, sandboxID, owner)
}

// lookupInstanceBySessionFromDB reconstructs a minimal Instance from the DB
// row keyed on session_id. Used when the in-memory cache is empty (gateway
// pod restart) but the underlying VM is still alive on fcagent.
func (m *SandboxManager) lookupInstanceBySessionFromDB(ctx context.Context, sessionID string) (*Instance, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	var row struct {
		ID             string         `db:"id"`
		InstanceID     sql.NullString `db:"instance_id"`
		ContainerID    sql.NullString `db:"container_id"`
		Backend        string         `db:"backend"`
		Status         string         `db:"status"`
		LifecycleState sql.NullString `db:"lifecycle_state"`
		CreatedAt      time.Time      `db:"created_at"`
		BillingStarted sql.NullTime   `db:"billing_started_at"`
		BillingEnded   sql.NullTime   `db:"billing_ended_at"`
		Name           string         `db:"name"`
		AgentID        sql.NullString `db:"agent_id"`
		Persistent     sql.NullBool   `db:"persistent"`
		Config         []byte         `db:"config"`
		Image          string         `db:"image"`
		AgentTarget    sql.NullString `db:"agent_target"`
	}
	const q = `
		SELECT id, instance_id, container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent, config, image, agent_target
		FROM sandbox_instances
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT 1`
	if err := m.db.GetContext(ctx, &row, q, sessionID); err != nil {
		return nil, fmt.Errorf("session lookup failed: %w", err)
	}
	var cfg InstanceConfig
	if err := json.Unmarshal(row.Config, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	if cfg.Image == "" {
		cfg.Image = row.Image
	}
	inst := &Instance{
		ID:               row.ID,
		InstanceID:       row.InstanceID.String,
		ContainerID:      row.ContainerID.String,
		Backend:          row.Backend,
		Status:           Status(row.Status),
		LifecycleState:   row.LifecycleState.String,
		CreatedAt:        row.CreatedAt,
		BillingStartedAt: nullTimeValue(row.BillingStarted),
		BillingEndedAt:   nullTimeValue(row.BillingEnded),
		Config:           cfg,
		Name:             row.Name,
		AgentID:          row.AgentID.String,
		Persistent:       row.Persistent.Bool,
		AgentTarget:      row.AgentTarget.String,
	}
	m.seedRouteFromInstance(inst)
	return inst, nil
}

// PatchInstanceConfig performs a partial update of the stored config for a
// sandbox instance. The patchFn receives the current config and should mutate
// it in-place. The updated config is written back to the database.
// Used to propagate new config fields (e.g. BrowserSidecar) to previously
// created troopers before reviving them.
func (m *SandboxManager) PatchInstanceConfig(ctx context.Context, sandboxID string, patchFn func(*InstanceConfig)) error {
	if m.db == nil {
		return fmt.Errorf("database not available")
	}
	cfg, _, err := m.GetInstanceConfig(ctx, sandboxID)
	if err != nil {
		return err
	}
	patchFn(cfg)
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal patched config: %w", err)
	}
	_, err = m.db.ExecContext(ctx,
		`UPDATE sandbox_instances SET config = $1 WHERE id = $2`,
		configJSON, sandboxID)
	return err
}

// instanceWorkDirUpdater is the optional capability a backend can
// implement to propagate a workdir change to its live VM/container
// state. Only backends that hold their own copy of InstanceConfig
// (firecracker captures it on the VM struct, for instance) need this;
// the rest happily re-read from sandbox_instances.config on demand
// and have nothing to update in memory.
type instanceWorkDirUpdater interface {
	UpdateInstanceWorkDir(id, newWorkDir string)
}

// UpdateInstanceWorkDir propagates a WorkDir change to a live sandbox
// in three places that all have to agree for the change to be visible:
//
//   - the manager's in-memory Instance (used by subsequent
//     Shell/Exec resolution paths),
//   - the backend's own copy of InstanceConfig if it keeps one
//     (firecracker's VM struct does — without this, the next Shell()
//     would still land in the stale directory),
//   - the sandbox_instances.config row in Postgres so the change
//     survives gateway restart / fcagent recovery.
//
// What this does NOT do: force-cd into already-open shells. Bash
// inside an open PTY tracks its own cwd; barging in with `cd /new`
// would interrupt whatever the user is typing. Open shells stay
// where they are; the user can `cd $NEW` themselves or close/reopen
// to land in the new dir.
//
// Returns nil on success, or when sandboxID isn't tracked locally
// (the call is a no-op in that case — typical for cross-fcagent
// scenarios where the gateway updates DB and the owning fcagent
// will pick up the change on the next config reload).
func (m *SandboxManager) UpdateInstanceWorkDir(ctx context.Context, sandboxID, newWorkDir string) error {
	newWorkDir = strings.TrimSpace(newWorkDir)
	if newWorkDir == "" {
		// Empty / whitespace is treated as "leave it alone" rather than
		// silently resetting to /workspace — operators editing the field
		// in the UI can clear it accidentally and we shouldn't punish
		// them for it.
		return nil
	}

	m.mu.Lock()
	inst, ok := m.instancesBySandbox[sandboxID]
	if ok {
		inst.Config.WorkDir = newWorkDir
	}
	m.mu.Unlock()

	if updater, ok := m.backend.(instanceWorkDirUpdater); ok {
		updater.UpdateInstanceWorkDir(sandboxID, newWorkDir)
	}

	// Persist to sandbox_instances.config so the change survives a
	// restart. Done outside the manager lock so the DB write doesn't
	// stall concurrent reads. If the in-memory update succeeded but
	// the DB write fails, the running sandbox is correct but a future
	// reconcile will overwrite — caller logs and moves on.
	if m.db != nil {
		if err := m.PatchInstanceConfig(ctx, sandboxID, func(cfg *InstanceConfig) {
			cfg.WorkDir = newWorkDir
		}); err != nil {
			return fmt.Errorf("persist workdir update: %w", err)
		}
	}
	return nil
}

// UpdateAgentTarget writes a recovered fcagent host:port back to
// sandbox_instances.agent_target. Called by the fcagent backend's
// route-recovery path (setRoute → fire-and-forget goroutine) so the
// DB row stays in sync with the in-memory route table.
//
// Without this, a gateway pod restart re-seeds the in-memory routes
// from a stale DB row and immediately pins to the dead IP — the
// exact bug observed live on dev when a persistent-agent reconcile
// reported `dial tcp 10.42.0.153:9090: connect: no route to host`
// against a fcagent that had been replaced.
//
// Best-effort: failures here don't break the calling RPC (route is
// still live in memory). The next route refresh re-attempts the
// persist, so eventual consistency. We pass through to the existing
// sandbox_instances UPDATE path that other agent_target writes use.
func (m *SandboxManager) UpdateAgentTarget(ctx context.Context, sandboxID, target string) error {
	target = strings.TrimSpace(target)
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" || target == "" {
		return nil
	}
	if m.db == nil {
		// No DB plumbed — manager is running in a config that doesn't
		// persist instances. In-memory routing still works for the
		// life of this process; there's nothing to write.
		return nil
	}
	// Single-statement UPDATE. We deliberately don't fall through to
	// the broader reconciler write path (UpdateInstanceWorkDir-style)
	// because agent_target doesn't carry any of the lifecycle-state
	// implications that the reconciler cares about — it's a pure
	// addressing field.
	_, err := m.db.ExecContext(ctx,
		`UPDATE sandbox_instances SET agent_target = $1, updated_at = NOW() WHERE id = $2`,
		target, sandboxID,
	)
	return err
}

// AggregateStats collects resource usage stats from all running instances and
// returns the sum. Instances that fail to report stats are silently skipped.
func (m *SandboxManager) AggregateStats(ctx context.Context) *ContainerStats {
	return m.aggregateStats(ctx, "")
}

// AggregateStatsForTenant collects only one instance tenant's running
// sandboxes for customer-facing dashboards.
func (m *SandboxManager) AggregateStatsForTenant(ctx context.Context, tenantID string) *ContainerStats {
	return m.aggregateStats(ctx, tenantID)
}

func (m *SandboxManager) aggregateStats(ctx context.Context, tenantID string) *ContainerStats {
	scope := TenantInstanceScope{TenantID: tenantID}
	m.mu.RLock()
	var running []*Instance
	for _, inst := range m.instances {
		if inst.Status == StatusRunning && (tenantID == "" || scope.MatchesInstance(inst)) {
			running = append(running, inst)
		}
	}
	m.mu.RUnlock()

	agg := &ContainerStats{}
	for _, inst := range running {
		stats, err := m.backend.Stats(ctx, inst.ID)
		if err != nil {
			continue
		}
		agg.CPUPercent += stats.CPUPercent
		agg.MemoryUsage += stats.MemoryUsage
		agg.MemoryLimit += stats.MemoryLimit
		agg.NetworkRxBytes += stats.NetworkRxBytes
		agg.NetworkTxBytes += stats.NetworkTxBytes
		agg.BlockRead += stats.BlockRead
		agg.BlockWrite += stats.BlockWrite
		agg.PIDs += stats.PIDs
	}
	if agg.MemoryLimit > 0 {
		agg.MemoryPercent = float64(agg.MemoryUsage) / float64(agg.MemoryLimit) * 100
	}
	return agg
}

// GetExpiredInstances returns instances whose idle time has exceeded their
// retention period. For instances with IdleRetentionSecs set, expiry is
// calculated as LastUsedAt + IdleRetentionSecs. Instances with
// IdleRetentionSecs == 0 never expire (pro/enterprise plans). Legacy
// instances without idle retention fall back to the fixed ExpiresAt timestamp.
//
// Keep-warm sandboxes (KeepWarm=true) use DefaultKeepWarmIdleSecs as their
// idle timeout and are skipped entirely when they still have active triggers.
// IdleSleepCandidatesSpecial returns sandbox IDs for the rows the
// reconciler IdleChecker's SQL pass intentionally excludes: persistent
// troopers (tier-resolved idle window, never while a turn is active)
// and keep-warm sandboxes (longer window, skipped while triggers
// exist). The IdleChecker calls this hook each tick and writes
// desired_state='sleeping' for the returned IDs; SetDesiredState only
// advances running rows, so a stale candidate is a harmless no-op.
func (m *SandboxManager) IdleSleepCandidatesSpecial() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	var out []string
	for _, inst := range m.instances {
		if inst.LifecycleState != "" && inst.LifecycleState != LifecycleRunning {
			continue
		}
		if inst.Status != StatusRunning {
			continue
		}
		switch {
		case inst.Persistent:
			if m.hasActiveTrooperSession(inst.AgentID) {
				continue
			}
			tier := "free"
			if m.trooperLimitResolver != nil {
				tier = m.trooperLimitResolver.ResolvePlanTier(inst.Config.TenantID)
			}
			idleTimeout := ResolveTrooperIdleTimeout(tier)
			if idleTimeout == 0 {
				continue
			}
			if !inst.LastUsedAt.IsZero() && now.After(inst.LastUsedAt.Add(idleTimeout)) {
				out = append(out, inst.ID)
			}
		case inst.KeepWarm:
			keepWarmSecs := m.globalConfig.DefaultKeepWarmIdleSecs
			if keepWarmSecs <= 0 {
				keepWarmSecs = 300
			}
			if !inst.LastUsedAt.IsZero() && now.Before(inst.LastUsedAt.Add(time.Duration(keepWarmSecs)*time.Second)) {
				continue
			}
			if m.hasActiveTriggers(inst.ID) {
				continue
			}
			out = append(out, inst.ID)
		}
	}
	return out
}

func (m *SandboxManager) GetExpiredInstances() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	var expired []string
	for sessionID, inst := range m.instances {
		// Lifecycle-aware expiry: only running sandboxes should be considered for
		// idle-stop pass. Stopped/reviving/terminating instances are handled by
		// dedicated lifecycle sweeps.
		if inst.LifecycleState != "" && inst.LifecycleState != LifecycleRunning {
			continue
		}
		if inst.Status != StatusRunning {
			continue
		}

		// Persistent troopers use their own idle-to-sleep timeout from the plan tier.
		// Idle-stop (sleep) is fine — it preserves state via snapshots.
		// The key invariant is: persistent troopers are NEVER destroyed/terminated
		// automatically. They can only be stopped (sleeping) and revived.
		if inst.Persistent {
			// Never idle-stop while a trooper turn is in progress or blocked on
			// HITL/user-input. This prevents mid-request sleep races.
			if m.hasActiveTrooperSession(inst.AgentID) {
				continue
			}

			tier := "free" // dev mode default: 30s idle-to-sleep
			if m.trooperLimitResolver != nil {
				tier = m.trooperLimitResolver.ResolvePlanTier(inst.Config.TenantID)
			}
			idleTimeout := ResolveTrooperIdleTimeout(tier)
			if idleTimeout == 0 {
				continue // no auto-sleep for this tier
			}
			if !inst.LastUsedAt.IsZero() {
				deadline := inst.LastUsedAt.Add(idleTimeout)
				if now.After(deadline) {
					expired = append(expired, sessionID)
				}
			}
			continue
		}

		// Keep-warm sandboxes get a longer idle window and are skipped when
		// active triggers still exist.
		if inst.KeepWarm {
			keepWarmSecs := m.globalConfig.DefaultKeepWarmIdleSecs
			if keepWarmSecs <= 0 {
				keepWarmSecs = 300
			}
			if !inst.LastUsedAt.IsZero() {
				deadline := inst.LastUsedAt.Add(time.Duration(keepWarmSecs) * time.Second)
				if now.Before(deadline) {
					continue // still within keep-warm window
				}
			}
			// Past keep-warm window — check if triggers are still active
			if m.hasActiveTriggers(inst.ID) {
				continue // triggers exist, keep alive
			}
			expired = append(expired, sessionID)
			continue
		}

		if inst.IdleRetentionSecs == 0 {
			// "Never expire" — the sandbox is never auto-terminated. But it
			// should still sleep when idle so we don't bill compute on a
			// dormant container. Fall back to DefaultIdleSleepSecs; the
			// stopped snapshot is preserved per the tenant's stop-retention
			// policy, so "Never" still means data lives indefinitely.
			sleepSecs := m.globalConfig.DefaultIdleSleepSecs
			if sleepSecs > 0 && !inst.LastUsedAt.IsZero() {
				deadline := inst.LastUsedAt.Add(time.Duration(sleepSecs) * time.Second)
				if now.After(deadline) {
					expired = append(expired, sessionID)
				}
			}
			continue
		}
		if inst.IdleRetentionSecs > 0 && !inst.LastUsedAt.IsZero() {
			deadline := inst.LastUsedAt.Add(time.Duration(inst.IdleRetentionSecs) * time.Second)
			if now.After(deadline) {
				expired = append(expired, sessionID)
			}
		} else if !inst.ExpiresAt.IsZero() && now.After(inst.ExpiresAt) {
			// Legacy fallback for instances created before idle retention
			expired = append(expired, sessionID)
		}
	}
	return expired
}

// logReaperDebug logs the state of all instances for debugging idle detection.
func (m *SandboxManager) logReaperDebug() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	for sessionID, inst := range m.instances {
		idleSecs := int(now.Sub(inst.LastUsedAt).Seconds())
		tier := ""
		trooperIdleTimeoutSecs := 0
		if inst.Persistent {
			tier = "free"
			if m.trooperLimitResolver != nil {
				tier = m.trooperLimitResolver.ResolvePlanTier(inst.Config.TenantID)
			}
			trooperIdleTimeoutSecs = int(ResolveTrooperIdleTimeout(tier).Seconds())
		}
		logger.WithFields(
			"session_id", sessionID,
			"sandbox_id", inst.ID,
			"status", string(inst.Status),
			"lifecycle", inst.LifecycleState,
			"persistent", inst.Persistent,
			"plan_tier", tier,
			"trooper_idle_timeout_secs", trooperIdleTimeoutSecs,
			"idle_secs", idleSecs,
			"idle_retention_secs", inst.IdleRetentionSecs,
			"resolver_nil", m.trooperLimitResolver == nil,
		).Info("sandbox_reaper: instance state")
	}
}

// hasActiveTriggers returns true when the sandbox has at least one enabled
// webhook or cron with auto_recreate. Uses a single batched DB query.
// Called under m.mu.RLock, so must not acquire the write lock.
func (m *SandboxManager) hasActiveTriggers(sandboxID string) bool {
	if m.db == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var count int
	const q = `
		SELECT COUNT(*) FROM (
			SELECT 1 FROM sandbox_webhooks
			WHERE sandbox_id = $1 AND enabled = true AND auto_recreate = true
			UNION ALL
			SELECT 1 FROM sandbox_crons
			WHERE sandbox_id = $1 AND enabled = true AND auto_recreate = true
		) t`
	if err := m.db.GetContext(ctx, &count, q, sandboxID); err != nil {
		return false
	}
	return count > 0
}

// hasActiveTrooperSession returns true when a trooper has an in-flight session
// that should block idle sleep.
func (m *SandboxManager) hasActiveTrooperSession(agentID string) bool {
	if m.db == nil || strings.TrimSpace(agentID) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var count int
	const q = `
		SELECT COUNT(*) FROM agent_sessions
		WHERE trooper_id = $1
		  AND status IN ('running', 'waiting_for_approval', 'waiting_for_user_input')`
	if err := m.db.GetContext(ctx, &count, q, agentID); err != nil {
		return false
	}
	return count > 0
}

// ============================================================================
// Port Exposure
// ============================================================================

// ExposePort exposes a container port to external traffic via subdomain routing.
// Only works when the sandbox's network mode is "allow" or "whitelist".
func (m *SandboxManager) ExposePort(ctx context.Context, sessionID string, port int, protocol string) (*PortMapping, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}

	if inst.Config.NetworkMode == NetworkDeny {
		return nil, fmt.Errorf("port exposure requires network_mode 'allow' or 'whitelist', got 'deny'")
	}

	if m.maxPortsPerSandbox > 0 {
		active := m.countActivePorts(inst.ID)
		if active >= m.maxPortsPerSandbox {
			return nil, fmt.Errorf("maximum of %d exposed ports per sandbox reached", m.maxPortsPerSandbox)
		}
	}

	if !CapabilitiesForBackend(m.backend).Features.PortExposure {
		return nil, fmt.Errorf("backend %s does not support port exposure", m.backend.Name())
	}
	exposer, ok := m.backend.(PortExposer)
	if !ok {
		return nil, fmt.Errorf("backend %s advertises port exposure but does not implement it", m.backend.Name())
	}

	if protocol == "" {
		protocol = "tcp"
	}

	hostPort, err := exposer.ExposePort(ctx, inst.ID, port, protocol)
	if err != nil {
		if errors.Is(err, ErrSandboxNotRunning) {
			m.purgeStaleSandbox(sessionID, inst.ID)
			return nil, fmt.Errorf("failed to expose port: %w", err)
		}
		return nil, fmt.Errorf("failed to expose port: %w", err)
	}

	// Subdomain format: <short_code>-<port>. Matches the user-visible
	// SSH username, so a single bitly-style code maps to both
	//   ssh <code>@ssh.evs.run
	//   https://<code>-<port>.evs.run
	// Legacy fallback (sbx-<last9>-<port>) covers rows that predate the
	// short_code column until the backfill runs.
	var subdomain string
	if inst.ShortCode != "" {
		subdomain = fmt.Sprintf("%s-%d", inst.ShortCode, port)
	} else {
		subID := strings.ReplaceAll(inst.ID, "-", "")
		if len(subID) > 9 {
			subID = subID[len(subID)-9:]
		}
		subdomain = fmt.Sprintf("sbx-%s-%d", subID, port)
	}

	backendTarget := fmt.Sprintf("localhost:%d", hostPort)
	if targeter, ok := m.backend.(BackendTargeter); ok {
		if target, err := targeter.BackendTarget(ctx, inst.ID, port); err == nil {
			backendTarget = target
		} else {
			logger.WithFields("sandbox_id", inst.ID, "port", port, "error", err.Error()).
				Warn("sandbox_manager: BackendTargeter failed, falling back to localhost")
		}
	}

	mapping := &PortMapping{
		SandboxID:     inst.ID,
		SessionID:     sessionID,
		TenantID:      inst.Config.TenantID,
		Port:          port,
		Protocol:      protocol,
		Subdomain:     subdomain,
		HostPort:      hostPort,
		BackendTarget: backendTarget,
		Status:        "active",
	}

	// Persist to DB
	m.persistPortMapping(mapping)

	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventPortExpose, fmt.Sprintf("Port %d exposed", port), map[string]interface{}{
		"port":      port,
		"protocol":  protocol,
		"subdomain": subdomain,
		"host_port": hostPort,
	}, nil, "")

	defer m.touchLastUsed(sessionID)
	return mapping, nil
}

// UnexposePort closes an exposed port mapping.
func (m *SandboxManager) UnexposePort(ctx context.Context, sessionID string, port int) error {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no sandbox for session %s", sessionID)
	}

	if !CapabilitiesForBackend(m.backend).Features.PortExposure {
		return fmt.Errorf("backend %s does not support port exposure", m.backend.Name())
	}
	exposer, ok := m.backend.(PortExposer)
	if !ok {
		return fmt.Errorf("backend %s advertises port exposure but does not implement it", m.backend.Name())
	}

	if err := exposer.UnexposePort(ctx, inst.ID, port); err != nil {
		return fmt.Errorf("failed to unexpose port: %w", err)
	}

	m.closePortMapping(inst.ID, port)

	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventPortUnexpose, fmt.Sprintf("Port %d unexposed", port), map[string]interface{}{"port": port}, nil, "")

	defer m.touchLastUsed(sessionID)
	return nil
}

// ListExposedPorts returns active port mappings for a session's sandbox.
func (m *SandboxManager) ListExposedPorts(ctx context.Context, sessionID string) ([]PortMapping, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}

	if m.db == nil {
		return nil, nil
	}

	var mappings []PortMapping
	const q = `SELECT * FROM sandbox_ports WHERE sandbox_id = $1 AND status = 'active' ORDER BY port`
	if err := m.db.SelectContext(ctx, &mappings, q, inst.ID); err != nil {
		return nil, fmt.Errorf("failed to list port mappings: %w", err)
	}

	return mappings, nil
}

// persistPortMapping upserts a port mapping to the database.
func (m *SandboxManager) persistPortMapping(mapping *PortMapping) {
	if m.db == nil {
		return
	}

	const q = `
		INSERT INTO sandbox_ports
			(sandbox_id, session_id, tenant_id, port, protocol, subdomain, host_port, backend_target, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (sandbox_id, port) DO UPDATE SET
			status = 'active', host_port = EXCLUDED.host_port, backend_target = EXCLUDED.backend_target, closed_at = NULL`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := m.db.ExecContext(ctx, q,
		mapping.SandboxID, mapping.SessionID, mapping.TenantID,
		mapping.Port, mapping.Protocol, mapping.Subdomain,
		mapping.HostPort, mapping.BackendTarget, mapping.Status,
	); err != nil {
		logger.WithFields("sandbox_id", mapping.SandboxID, "port", mapping.Port, "error", err.Error()).
			Warn("sandbox_manager: failed to persist port mapping")
	}
}

// closePortMapping marks a port mapping as closed in the database.
func (m *SandboxManager) closePortMapping(sandboxID string, port int) {
	if m.db == nil {
		return
	}

	const q = `UPDATE sandbox_ports SET status = 'closed', closed_at = NOW() WHERE sandbox_id = $1 AND port = $2`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := m.db.ExecContext(ctx, q, sandboxID, port); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "port", port, "error", err.Error()).
			Debug("sandbox_manager: failed to close port mapping")
	}
}

// closeAllPortMappings marks all active port mappings for a sandbox as closed.
// Called during sandbox destruction.
func (m *SandboxManager) closeAllPortMappings(sandboxID string) {
	if m.db == nil {
		return
	}

	const q = `UPDATE sandbox_ports SET status = 'closed', closed_at = NOW() WHERE sandbox_id = $1 AND status = 'active'`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := m.db.ExecContext(ctx, q, sandboxID); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Debug("sandbox_manager: failed to close all port mappings")
	}
}

// restoreOrCloseStalePortMappings restores in-memory TCP proxies for port
// mappings whose sandboxes survived the server restart (persistent agents).
// Mappings for sandboxes that are no longer running are closed as stale.
func (m *SandboxManager) restoreOrCloseStalePortMappings() {
	if m.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Query all active mappings from the DB.
	var mappings []PortMapping
	const q = `SELECT * FROM sandbox_ports WHERE status = 'active'`
	if err := m.db.SelectContext(ctx, &mappings, q); err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_manager: failed to query active port mappings, closing all as stale")
		m.closeAllStalePortMappings()
		return
	}
	if len(mappings) == 0 {
		return
	}

	// 2. Snapshot which sandboxes are alive under the read lock.
	m.mu.RLock()
	liveSandboxes := make(map[string]*Instance, len(m.instancesBySandbox))
	for id, inst := range m.instancesBySandbox {
		liveSandboxes[id] = inst
	}
	m.mu.RUnlock()

	caps := CapabilitiesForBackend(m.backend)
	exposer, hasExposer := m.backend.(PortExposer)
	hasExposer = hasExposer && caps.Features.PortExposure
	var restored, closed int

	for _, pm := range mappings {
		inst, alive := liveSandboxes[pm.SandboxID]
		if !alive || inst.Status != StatusRunning || !hasExposer {
			m.closePortMapping(pm.SandboxID, pm.Port)
			closed++
			continue
		}

		// 3. Re-create the in-memory TCP proxy for this mapping.
		hostPort, err := exposer.ExposePort(ctx, pm.SandboxID, pm.Port, pm.Protocol)
		if err != nil {
			logger.WithFields("sandbox_id", pm.SandboxID, "port", pm.Port, "error", err.Error()).
				Warn("sandbox_manager: failed to restore port proxy, closing mapping")
			m.closePortMapping(pm.SandboxID, pm.Port)
			closed++
			continue
		}

		// 4. Compute new backend target.
		backendTarget := fmt.Sprintf("localhost:%d", hostPort)
		if targeter, ok := m.backend.(BackendTargeter); ok {
			if target, err := targeter.BackendTarget(ctx, pm.SandboxID, pm.Port); err == nil {
				backendTarget = target
			}
		}

		// 5. Update DB record with new host_port and backend_target.
		pm.HostPort = hostPort
		pm.BackendTarget = backendTarget
		pm.Status = "active"
		m.persistPortMapping(&pm)
		restored++

		logger.WithFields(
			"sandbox_id", pm.SandboxID,
			"port", pm.Port,
			"host_port", hostPort,
			"subdomain", pm.Subdomain,
		).Info("sandbox_manager: restored port mapping from previous process")
	}

	if restored > 0 || closed > 0 {
		logger.WithFields("restored", restored, "closed", closed).
			Info("sandbox_manager: port mapping restore complete")
	}
}

// closeAllStalePortMappings is the fallback that closes all active port
// mappings when we can't query them individually (e.g., DB query failure).
func (m *SandboxManager) closeAllStalePortMappings() {
	const q = `UPDATE sandbox_ports SET status = 'closed', closed_at = NOW() WHERE status = 'active'`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := m.db.ExecContext(ctx, q)
	if err != nil {
		logger.WithFields("error", err.Error()).
			Debug("sandbox_manager: failed to close stale port mappings")
		return
	}
	if n, _ := result.RowsAffected(); n > 0 {
		logger.WithFields("count", n).
			Info("sandbox_manager: closed stale port mappings from previous process")
	}
}

// disableCronsWebhooks disables crons and webhooks for a destroyed sandbox,
// except those with auto_recreate enabled (they remain active to recreate the sandbox).
func (m *SandboxManager) disableCronsWebhooks(sandboxID string) {
	if m.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const cronQ = `UPDATE sandbox_crons SET enabled = false, updated_at = NOW() WHERE sandbox_id = $1 AND enabled = true AND auto_recreate = false`
	if _, err := m.db.ExecContext(ctx, cronQ, sandboxID); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Debug("sandbox_manager: failed to disable crons on destroy")
	}

	const webhookQ = `UPDATE sandbox_webhooks SET enabled = false, updated_at = NOW() WHERE sandbox_id = $1 AND enabled = true AND auto_recreate = false`
	if _, err := m.db.ExecContext(ctx, webhookQ, sandboxID); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Debug("sandbox_manager: failed to disable webhooks on destroy")
	}
}

// countActivePorts returns the number of active port mappings for a sandbox.
func (m *SandboxManager) countActivePorts(sandboxID string) int {
	if m.db == nil {
		return 0
	}

	var count int
	const q = `SELECT COUNT(*) FROM sandbox_ports WHERE sandbox_id = $1 AND status = 'active'`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := m.db.GetContext(ctx, &count, q, sandboxID); err != nil {
		return 0
	}
	return count
}

// DetectListeningPorts returns ports currently listening inside the sandbox.
// Only works with backends that implement the PortDetector interface.
func (m *SandboxManager) DetectListeningPorts(ctx context.Context, sessionID string) ([]ListeningPort, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}

	if !CapabilitiesForBackend(m.backend).Features.PortDetection {
		return nil, fmt.Errorf("backend %s does not support port detection", m.backend.Name())
	}
	detector, ok := m.backend.(PortDetector)
	if !ok {
		return nil, fmt.Errorf("backend %s advertises port detection but does not implement it", m.backend.Name())
	}

	ports, err := detector.DetectListeningPorts(ctx, inst.ID)
	if err != nil && errors.Is(err, ErrSandboxNotRunning) {
		m.purgeStaleSandbox(sessionID, inst.ID)
		return nil, fmt.Errorf("detect listening ports: %w", err)
	}
	return ports, err
}

// LookupPortMapping looks up an active port mapping by subdomain.
// Used by the reverse proxy to route incoming requests.
func (m *SandboxManager) LookupPortMapping(ctx context.Context, subdomain string) (*PortMapping, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var mapping PortMapping
	const q = `SELECT * FROM sandbox_ports WHERE subdomain = $1 AND status = 'active'`
	if err := m.db.GetContext(ctx, &mapping, q, subdomain); err != nil {
		return nil, fmt.Errorf("port mapping not found for subdomain %s: %w", subdomain, err)
	}
	return &mapping, nil
}

// LookupPortMappingByPort resolves a sandbox ID + port number to an active port mapping.
// Used by the gateway for header-based and path-based routing.
// If no active mapping exists in the DB but the sandbox is alive and running,
// the port is auto-exposed on-demand so that users don't need to manually call
// sandbox_expose_port for every service.
func (m *SandboxManager) LookupPortMappingByPort(ctx context.Context, sandboxID string, port int) (*PortMapping, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var mapping PortMapping
	const q = `SELECT * FROM sandbox_ports WHERE sandbox_id = $1 AND port = $2 AND status = 'active'`
	if err := m.db.GetContext(ctx, &mapping, q, sandboxID, port); err == nil {
		return &mapping, nil
	}

	// No active mapping — try auto-exposing if the sandbox is alive.
	m.mu.RLock()
	inst, alive := m.instancesBySandbox[sandboxID]
	m.mu.RUnlock()
	if !alive || inst.Status != StatusRunning {
		return nil, fmt.Errorf("port mapping not found for sandbox %s port %d", sandboxID, port)
	}

	sessionID := inst.Config.SessionID
	if sessionID == "" {
		return nil, fmt.Errorf("port mapping not found for sandbox %s port %d (no session)", sandboxID, port)
	}

	logger.WithFields("sandbox_id", sandboxID, "port", port).
		Info("sandbox_manager: auto-exposing port on gateway lookup miss")

	autoMapping, err := m.ExposePort(ctx, sessionID, port, "tcp")
	if err != nil {
		return nil, fmt.Errorf("port mapping not found for sandbox %s port %d (auto-expose failed: %w)", sandboxID, port, err)
	}
	return autoMapping, nil
}

// ============================================================================
// Auto-Recreate (for crons/webhooks)
// ============================================================================

// GetOrRecreate returns a running sandbox or recreates it from saved config.
// Used by cron scheduler and webhook router for auto_recreate mode.
// Sandboxes created through this path are automatically marked as keep-warm
// since they are bound to active triggers.
func (m *SandboxManager) GetOrRecreate(ctx context.Context, sessionID, tenantID string, savedConfig json.RawMessage) (*Instance, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()

	if ok && inst.Status == StatusRunning {
		// Ensure keep-warm flag is set on already-running sandboxes that are
		// being accessed through trigger auto-recreate path.
		if !inst.KeepWarm {
			m.mu.Lock()
			inst.KeepWarm = true
			m.mu.Unlock()
		}
		m.touchLastUsed(sessionID)
		return inst, nil
	}

	// Parse saved config and recreate
	var cfg InstanceConfig
	if err := json.Unmarshal(savedConfig, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse saved sandbox config: %w", err)
	}

	sandboxCfg := SandboxConfig{
		Enabled:           true,
		Image:             cfg.Image,
		CPULimit:          cfg.CPULimit,
		MemoryMB:          cfg.MemoryMB,
		DiskMB:            cfg.DiskMB,
		TimeoutSeconds:    cfg.TimeoutSeconds,
		NetworkMode:       string(cfg.NetworkMode),
		AllowedHosts:      cfg.AllowedHosts,
		EnvVars:           cfg.EnvVars,
		GitRepoURL:        cfg.GitRepoURL,
		GitBranch:         cfg.GitBranch,
		GitInstallationID: cfg.GitInstallationID,
		KeepWarm:          true, // auto-recreate sandboxes are always keep-warm
		Persistent:        cfg.AgentID != "",
		AgentID:           cfg.AgentID,
	}

	return m.GetOrCreate(ctx, sessionID, tenantID, sandboxCfg)
}

// DB returns the database connection, allowing subsystems (scheduler, webhook)
// to query trigger-related tables.
func (m *SandboxManager) DB() *sqlx.DB {
	return m.db
}

// SetMaxPortsPerSandbox sets the maximum number of exposed ports per sandbox.
func (m *SandboxManager) SetMaxPortsPerSandbox(max int) {
	m.maxPortsPerSandbox = max
}

func nullableString(v string) interface{} {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullTimeValue(v sql.NullTime) time.Time {
	if v.Valid {
		return v.Time
	}
	return time.Time{}
}

func nullableInt64(v int64) interface{} {
	if v <= 0 {
		return nil
	}
	return v
}

// DeleteFile removes one or more files inside the session's sandbox.
func (m *SandboxManager) DeleteFile(ctx context.Context, sessionID string, paths []string) error {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no sandbox for session %s", sessionID)
	}
	defer m.touchLastUsed(sessionID)

	ctx, span := telemetry.StartSandboxFSSpan(ctx, "delete", inst.ID)
	defer span.End()
	span.SetAttributes(attribute.Int(attrs.SandboxFSEntries, len(paths)))

	args := append([]string{"rm", "-f"}, paths...)
	result, err := m.backend.Exec(ctx, inst.ID, ExecRequest{
		Command: args,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		telemetry.RecordError(span, err)
		return fmt.Errorf("delete file failed: %w", err)
	}
	if result.ExitCode != 0 {
		delErr := fmt.Errorf("delete file failed: %s", result.Stderr)
		telemetry.RecordError(span, delErr)
		return delErr
	}
	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventFileDelete, "Files deleted", map[string]interface{}{"paths": paths}, nil, "")
	return nil
}

// MoveFile moves or renames a file inside the session's sandbox.
func (m *SandboxManager) MoveFile(ctx context.Context, sessionID, src, dest string) error {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no sandbox for session %s", sessionID)
	}
	defer m.touchLastUsed(sessionID)

	result, err := m.backend.Exec(ctx, inst.ID, ExecRequest{
		Command: []string{"mv", src, dest},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("move file failed: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("move file failed: %s", result.Stderr)
	}
	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventFileMove, "File moved", map[string]interface{}{"src": src, "dest": dest}, nil, "")
	return nil
}

// GetFileInfo retrieves detailed metadata for one or more paths inside the session's sandbox.
func (m *SandboxManager) GetFileInfo(ctx context.Context, sessionID string, paths []string) ([]FileMetadata, error) {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}
	defer m.touchLastUsed(sessionID)

	// stat --printf outputs: size\ntype\nmtime\nctime\nowner\ngroup\nmode\npath\n---\n
	// We use --- as a record separator for multiple paths.
	args := []string{"stat", "--printf", "%s\\n%F\\n%Y\\n%W\\n%U\\n%G\\n%a\\n%n\\n---\\n"}
	args = append(args, paths...)
	result, err := m.backend.Exec(ctx, inst.ID, ExecRequest{
		Command: args,
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("get file info failed: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("get file info failed: %s", result.Stderr)
	}

	var files []FileMetadata
	records := strings.Split(strings.TrimSpace(result.Stdout), "---")
	for _, rec := range records {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		lines := strings.Split(rec, "\n")
		if len(lines) < 8 {
			continue
		}
		var size int64
		fmt.Sscanf(lines[0], "%d", &size)
		isDir := strings.Contains(lines[1], "directory")
		var mtime, ctime int64
		fmt.Sscanf(lines[2], "%d", &mtime)
		fmt.Sscanf(lines[3], "%d", &ctime)
		var mode uint32
		fmt.Sscanf(lines[6], "%o", &mode)

		files = append(files, FileMetadata{
			Path:       lines[7],
			Size:       size,
			IsDir:      isDir,
			ModifiedAt: time.Unix(mtime, 0),
			CreatedAt:  time.Unix(ctime, 0),
			Owner:      lines[4],
			Group:      lines[5],
			Mode:       mode,
		})
	}
	return files, nil
}

// MkdirAll creates one or more directories (with parents) inside the session's sandbox.
func (m *SandboxManager) MkdirAll(ctx context.Context, sessionID string, paths []string) error {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no sandbox for session %s", sessionID)
	}
	defer m.touchLastUsed(sessionID)

	args := append([]string{"mkdir", "-p"}, paths...)
	result, err := m.backend.Exec(ctx, inst.ID, ExecRequest{
		Command: args,
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("mkdir failed: %s", result.Stderr)
	}
	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventDirCreate, "Directories created", map[string]interface{}{"paths": paths}, nil, "")
	return nil
}

// DeleteDirectories removes one or more directories inside the session's sandbox.
func (m *SandboxManager) DeleteDirectories(ctx context.Context, sessionID string, paths []string) error {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no sandbox for session %s", sessionID)
	}
	defer m.touchLastUsed(sessionID)

	args := append([]string{"rm", "-rf"}, paths...)
	result, err := m.backend.Exec(ctx, inst.ID, ExecRequest{
		Command: args,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("delete directories failed: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("delete directories failed: %s", result.Stderr)
	}
	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventDirDelete, "Directories deleted", map[string]interface{}{"paths": paths}, nil, "")
	return nil
}

// ChmodFile changes the file mode of a path inside the session's sandbox.
func (m *SandboxManager) ChmodFile(ctx context.Context, sessionID, path, mode string) error {
	m.mu.RLock()
	inst, ok := m.instances[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no sandbox for session %s", sessionID)
	}
	defer m.touchLastUsed(sessionID)

	result, err := m.backend.Exec(ctx, inst.ID, ExecRequest{
		Command: []string{"chmod", mode, path},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("chmod failed: %s", result.Stderr)
	}
	m.recordEvent(inst.ID, sessionID, inst.Config.TenantID, EventFileChmod, "File permissions changed", map[string]interface{}{"path": path, "mode": mode}, nil, "")
	return nil
}

// RenewExpiration extends the idle retention timeout for the session's sandbox.
func (m *SandboxManager) RenewExpiration(sessionID string, extraSeconds int) error {
	m.mu.Lock()
	inst, ok := m.instances[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no sandbox for session %s", sessionID)
	}
	inst.IdleRetentionSecs += extraSeconds
	inst.LastUsedAt = time.Now()
	m.mu.Unlock()

	m.persistInstance(inst)
	return nil
}

// ReplaceInFile performs a string replacement in a file inside the session's sandbox.
func (m *SandboxManager) ReplaceInFile(ctx context.Context, sessionID, path, oldStr, newStr string) error {
	data, err := m.ReadFile(ctx, sessionID, path)
	if err != nil {
		return fmt.Errorf("read for replace failed: %w", err)
	}
	replaced := strings.Replace(string(data), oldStr, newStr, -1)
	return m.WriteFile(ctx, sessionID, path, []byte(replaced))
}
