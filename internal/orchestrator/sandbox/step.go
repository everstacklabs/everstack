package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

// Executor is the side-effect layer step() drives. Implemented by
// *sandbox.SandboxManager (manager_executor.go), which wraps the raw
// backend with workspace snapshot/restore, R2 archive, usage metering,
// event recording, and in-memory cache upkeep. Defined locally so
// step() tests can mock without importing the manager.
//
// History: step() used to call the raw backend directly, which meant a
// reconciler-driven stop destroyed the VM WITHOUT snapshotting /workspace
// and a revive recreated a blank workspace. Data loss, fixed by moving
// all side effects behind this interface.
type Executor interface {
	ExecuteCreate(ctx context.Context, id string, config sandbox.InstanceConfig) (*sandbox.Instance, error)
	// ExecuteStop snapshots the workspace, destroys the VM, and
	// returns the local tarball path ("" when none could be taken).
	ExecuteStop(ctx context.Context, id string) (snapshotRef string, err error)
	// ExecuteRevive recreates the VM and restores the workspace from
	// the local tarball or, when archived, from object storage.
	ExecuteRevive(ctx context.Context, id, snapshotRef, archiveRef string) (*sandbox.Instance, error)
	// ExecuteArchive uploads the workspace tarball to object storage
	// and deletes the local copy. Returns the archive ref ("" when
	// archiving degraded to label-only).
	ExecuteArchive(ctx context.Context, id, snapshotRef string) (archiveRef string, err error)
	ExecuteTerminate(ctx context.Context, id string) error
	BackendStatus(ctx context.Context, id string) (*sandbox.Instance, error)
}

// Lifecycle states. Mirror the existing constants in
// internal/sandbox/manager_lifecycle.go so the FE keeps working
// during the migration.
const (
	StatePending     = "pending"
	StateCreating    = "creating"
	StateRunning     = "running"
	StateStopping    = "stopping"
	StateSleeping    = "sleeping"
	StateReviving    = "reviving"
	StateArchiving   = "archiving" // sleeping → archiving: state-only transition (VM already gone)
	StateArchived    = "archived"  // parked: no VM, workspace snapshot preserved
	StateTerminating = "terminating"
	StateTerminated  = "terminated"
	StateFailed      = "failed" // legacy terminal; superseded by StateError
	// StateError is the recoverable problem state (Daytona-style):
	// convergence exhausted its retries or the health sweeper found the
	// VM dead. Exits ONLY via Recover() (re-enter convergence toward
	// desired_state) or terminate. Unlike the legacy 'failed', error
	// rows are never auto-terminated by a sweep; they persist until
	// the user acts or auto_delete_minutes elapses.
	StateError = "error"
)

// Desired states. Only four: what the user wants the system to
// converge to. Reconciler diffs current vs desired.
const (
	DesireRunning    = "running"
	DesireSleeping   = "sleeping"
	DesireArchived   = "archived" // ArchiveChecker sets this on eligible sleeping rows
	DesireTerminated = "terminated"
)

var claimableStates = []string{
	StatePending,
	StateCreating,
	StateStopping,
	StateReviving,
	StateArchiving,
	StateTerminating,
}

// convergenceStates are the lifecycle states where step() actively
// calls the backend. Failures in these states increment
// reconcile_attempts and may transition to terminal `failed` after
// the backoff cap. See plan: "Critical corollary — running rows never
// enter the failure path."
//
// running, sleeping, failed, terminated are NOT in this set. A step()
// error on a running row parks the row but does not increment attempts
// or flip to failed. This is the structural protection for the user's
// "can't lose uptime ever" invariant.
var convergenceStates = map[string]bool{
	StateCreating:    true,
	StateStopping:    true,
	StateReviving:    true,
	StateArchiving:   true, // real work since the R2 upload: retry with backoff
	StateTerminating: true,
}

// IsConvergenceState reports whether failures during step() on this
// state should increment reconcile_attempts and progress toward
// terminal failed. Used by Reconciler to choose between RecordFailure
// and ParkRow.
func IsConvergenceState(s string) bool {
	return convergenceStates[s]
}

// IsTerminal reports whether a row should never be claimed by the
// reconciler again. Terminal rows are dropped from the partial index
// at the schema layer; this is just a Go-side mirror. StateError is
// included: error rows are parked until Recover() or terminate flips
// their lifecycle_state back into a claimable convergence state.
func IsTerminal(s string) bool {
	return s == StateFailed || s == StateTerminated || s == StateError
}

// Agent lifecycle states (mirrors agent_definitions.lifecycle_status
// values used by the FE / proto enum). Kept here as bare strings so
// the reconciler can write to agent_definitions without importing
// the agent package and creating a cycle.
const (
	AgentStateCreated      = "created"
	AgentStateProvisioning = "provisioning"
	AgentStateRunning      = "running"
	AgentStateIdle         = "idle"
	AgentStateSleeping     = "sleeping"
	AgentStateWaking       = "waking"
	AgentStateFailed       = "failed"
	AgentStateTerminated   = "terminated"
)

// AgentLifecycleFor maps a sandbox lifecycle_state to the agent
// lifecycle_status that should be derived from it. This implements
// the issue-9 invariant from project_sandbox_launch_open_issues.md:
// the agent's lifecycle is a derived view of the sandbox's
// lifecycle, never a parallel state machine.
//
// The per-turn 'running' vs 'idle' distinction is owned entirely by
// the agent runtime's turn-start / turn-end handlers (they write
// directly to agent_definitions.lifecycle_status). The reconciler
// derives 'idle' for any running sandbox; the SQL guard in
// applyAgentLifecycleInTx prevents that write from clobbering an
// active turn (lifecycle_status='running' AND $2='idle' is blocked).
// So 'running' is never written or read by the reconciler — it's
// strictly the runtime's lane.
//
// Returned states intentionally don't include "created"; that's the
// pre-provisioning state set when the agent row is first inserted,
// before any sandbox exists. The reconciler only writes to agents
// that already have a primary_session_id pointing at a sandbox.
func AgentLifecycleFor(sandboxState string) string {
	switch sandboxState {
	case StatePending, StateCreating:
		return AgentStateProvisioning
	case StateRunning:
		// Idle is the default for any running sandbox; the runtime
		// flips to 'running' when a turn starts. The SQL guard
		// prevents the reconciler from demoting an active turn.
		return AgentStateIdle
	case StateStopping, StateSleeping, StateArchiving, StateArchived:
		return AgentStateSleeping
	case StateReviving:
		return AgentStateWaking
	case StateTerminating, StateTerminated:
		return AgentStateTerminated
	case StateFailed, StateError:
		return AgentStateFailed
	}
	// Unknown sandbox state — leave the agent row alone rather than
	// guessing.
	return ""
}

// Step computes the next row state given the current row and the
// backend it's running on. Returns the new row + an error if the
// transition failed (caller decides whether to retry).
//
// Invariants:
//   - If err != nil, the returned Row is the input row unchanged.
//   - Step does not mutate the input row.
//   - Step is idempotent: calling it twice on the same row is safe.
//   - Step never holds a lock or touches the DB. Pure function.
//   - For non-convergence states, Step returns a Row with
//     ReconcileAfter set far in the future (see parkUntil).
func Step(ctx context.Context, ex Executor, row Row) (Row, error) {
	switch row.LifecycleState {
	case StatePending:
		// Optimistic transition: just flip to creating. The next tick
		// will call Create. We do this so the FE sees creating-state
		// progress within one tick instead of waiting for Create's
		// network latency on the first tick.
		next := row
		next.LifecycleState = StateCreating
		next.Status = StateCreating
		next.ReconcileAfter = time.Now() // claim immediately next tick
		return next, nil

	case StateCreating:
		cfg, err := unmarshalConfig(row.Config)
		if err != nil {
			return row, fmt.Errorf("creating: parse config: %w", err)
		}
		// Ensure tenant + session ID flow through to the backend even
		// if the persisted config didn't carry them (defensive — the
		// FE handler should've set them).
		if cfg.TenantID == "" {
			cfg.TenantID = row.TenantID
		}
		if cfg.SessionID == "" {
			cfg.SessionID = row.SessionID
		}
		inst, err := ex.ExecuteCreate(ctx, row.ID, cfg)
		if err != nil {
			return row, fmt.Errorf("creating: ExecuteCreate: %w", err)
		}
		next := row
		next.LifecycleState = StateRunning
		next.Status = StateRunning
		if inst != nil {
			next.ContainerID = nullable(inst.ContainerID)
			if !inst.BillingStartedAt.IsZero() {
				next.BillingStartedAt = sql.NullTime{Time: inst.BillingStartedAt, Valid: true}
			}
			// fcagent's Create populates AgentTarget with the picked
			// host:port; other backends leave it empty (no per-instance
			// agent address) so we just skip the column write.
			if inst.AgentTarget != "" {
				next.AgentTarget = nullable(inst.AgentTarget)
			}
		}
		// Park running rows; they're only re-evaluated on a
		// desired_state change (which writes reconcile_after = NOW()).
		next.ReconcileAfter = parkUntil(24 * time.Hour)
		return next, nil

	case StateRunning:
		// Running is a quiescent state. The reconciler shouldn't be
		// claiming it unless desired_state diverged. If we got here:
		switch row.DesiredState {
		case DesireSleeping:
			next := row
			next.LifecycleState = StateStopping
			next.Status = StateStopping
			next.ReconcileAfter = time.Now()
			return next, nil
		case DesireTerminated:
			next := row
			next.LifecycleState = StateTerminating
			next.Status = StateTerminating
			next.ReconcileAfter = time.Now()
			return next, nil
		default:
			// Already converged. Park far in the future. Idle policy
			// (a separate component) decides if/when to bump
			// desired_state to sleeping.
			return parkRow(row, 24*time.Hour), nil
		}

	case StateStopping:
		// ExecuteStop snapshots /workspace BEFORE destroying the VM and
		// returns the tarball ref to persist; a reconciler-driven stop
		// is data-preserving exactly like the user-facing one.
		snapshotRef, err := ex.ExecuteStop(ctx, row.ID)
		if err != nil {
			return row, fmt.Errorf("stopping: ExecuteStop: %w", err)
		}
		next := row
		next.LifecycleState = StateSleeping
		next.Status = "stopped"
		next.ContainerID = sqlNullStringEmpty()
		next.WorkspaceSnapshotRef = nullable(snapshotRef)
		next.ReconcileAfter = parkUntil(24 * time.Hour)
		return next, nil

	case StateSleeping:
		switch row.DesiredState {
		case DesireRunning:
			next := row
			next.LifecycleState = StateReviving
			next.Status = StateReviving
			next.ReconcileAfter = time.Now()
			return next, nil
		case DesireArchived:
			// Archive transition: the VM is already gone (stopped in
			// StateStopping). Archiving is purely a state label change
			// that records when the row was archived and blocks it from
			// waking on idle-revive. No backend call needed.
			next := row
			next.LifecycleState = StateArchiving
			next.Status = StateArchiving
			next.ReconcileAfter = time.Now() // claim immediately on next tick
			return next, nil
		case DesireTerminated:
			next := row
			next.LifecycleState = StateTerminating
			next.Status = StateTerminating
			next.ReconcileAfter = time.Now()
			return next, nil
		default:
			return parkRow(row, 24*time.Hour), nil
		}

	case StateArchiving:
		// Real archive: move the workspace tarball to object storage
		// and free the host-disk copy (Daytona semantics: archived =
		// filesystem in object storage). Degrades to a label-only
		// transition when R2 is unconfigured or there is no snapshot,
		// in which case the local tar (if any) is kept.
		archiveRef, err := ex.ExecuteArchive(ctx, row.ID, row.WorkspaceSnapshotRef.String)
		if err != nil {
			return row, fmt.Errorf("archiving: ExecuteArchive: %w", err)
		}
		next := row
		next.LifecycleState = StateArchived
		next.Status = StateArchived
		if archiveRef != "" {
			next.WorkspaceArchiveRef = nullable(archiveRef)
			next.WorkspaceSnapshotRef = sqlNullStringEmpty() // local copy deleted
		}
		next.ReconcileAfter = parkUntil(30 * 24 * time.Hour)
		return next, nil

	case StateArchived:
		switch row.DesiredState {
		case DesireRunning:
			// Restore: transition to reviving. The backend's Create path
			// will restore from workspace_snapshot_ref if present.
			next := row
			next.LifecycleState = StateReviving
			next.Status = StateReviving
			next.ReconcileAfter = time.Now()
			return next, nil
		case DesireTerminated:
			next := row
			next.LifecycleState = StateTerminating
			next.Status = StateTerminating
			next.ReconcileAfter = time.Now()
			return next, nil
		default:
			return parkRow(row, 30*24*time.Hour), nil
		}

	case StateReviving:
		// ExecuteRevive recreates the VM AND restores /workspace from the
		// local tarball, or from object storage when the row was
		// archived. Config parsing lives in the executor (it loads the
		// saved config from the DB row itself).
		inst, err := ex.ExecuteRevive(ctx, row.ID, row.WorkspaceSnapshotRef.String, row.WorkspaceArchiveRef.String)
		if err != nil {
			return row, fmt.Errorf("reviving: ExecuteRevive: %w", err)
		}
		next := row
		next.LifecycleState = StateRunning
		next.Status = StateRunning
		if inst != nil {
			next.ContainerID = nullable(inst.ContainerID)
			if !inst.BillingStartedAt.IsZero() {
				next.BillingStartedAt = sql.NullTime{Time: inst.BillingStartedAt, Valid: true}
			}
			if inst.AgentTarget != "" {
				next.AgentTarget = nullable(inst.AgentTarget)
			}
		}
		// The workspace is live inside the VM again; the consumed refs
		// are cleared so the next stop records a fresh snapshot.
		next.WorkspaceSnapshotRef = sqlNullStringEmpty()
		next.WorkspaceArchiveRef = sqlNullStringEmpty()
		next.ReconcileAfter = parkUntil(24 * time.Hour)
		return next, nil

	case StateTerminating:
		if err := ex.ExecuteTerminate(ctx, row.ID); err != nil {
			// fcagent returns NotFound when the VM was already gone —
			// treat that as success so re-runs of Terminate don't loop.
			// All other errors retry via the convergence-state path.
			if !isNotFoundErr(err) {
				return row, fmt.Errorf("terminating: ExecuteTerminate: %w", err)
			}
		}
		next := row
		next.LifecycleState = StateTerminated
		next.Status = StateTerminated
		next.ContainerID = sqlNullStringEmpty()
		next.ReconcileAfter = parkUntil(365 * 24 * time.Hour)
		return next, nil

	case StateError:
		// Recoverable problem state. Only Recover() (re-enters a
		// convergence state) or SetDesiredState(terminated) moves it;
		// the reconciler itself never auto-exits error. Park.
		return parkRow(row, 365*24*time.Hour), nil

	case StateFailed, StateTerminated:
		// Terminal. Reconciler should not have picked this up; the
		// partial index excludes these states. Park indefinitely as a
		// safety net so a misbehaving claim doesn't loop.
		return parkRow(row, 365*24*time.Hour), nil
	}

	return row, fmt.Errorf("step: unknown lifecycle state %q", row.LifecycleState)
}

// PublicState maps the internal lifecycle vocabulary onto the
// Daytona-style labels exposed in the public API and UI. The DB keeps
// its names (a rename would touch every query, trigger, and migration
// for zero functional value); clients branch on these labels.
func PublicState(internal string) string {
	switch internal {
	case StatePending, StateCreating, "provisioning":
		return "creating"
	case StateRunning:
		return "started"
	case StateStopping:
		return "stopping"
	case StateSleeping, "stopped":
		return "stopped"
	case StateReviving:
		return "starting"
	case StateArchiving:
		return "archiving"
	case StateArchived:
		return "archived"
	case StateTerminating:
		return "destroying"
	case StateTerminated:
		return "destroyed"
	case StateError, StateFailed:
		return "error"
	}
	return internal
}

// parkUntil returns a far-future timestamp for non-convergence rows.
// 24h is large enough that the reconciler won't waste cycles on
// converged rows, but small enough that a row whose reconcile_after
// somehow gets stuck won't be silently abandoned for years.
func parkUntil(d time.Duration) time.Time {
	return time.Now().Add(d)
}

// parkRow returns the input row with reconcile_after pushed out. Used
// for non-convergence states where Step is a no-op.
func parkRow(row Row, d time.Duration) Row {
	next := row
	next.ReconcileAfter = parkUntil(d)
	return next
}

// unmarshalConfig parses the JSON config blob stored on the row.
// Returns a zero-value config (not an error) when the blob is empty
// — older rows from before the migration may have empty config.
func unmarshalConfig(raw []byte) (sandbox.InstanceConfig, error) {
	var cfg sandbox.InstanceConfig
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// nullable wraps a string in sql.NullString, treating empty as NULL.
func nullable(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func sqlNullStringEmpty() sql.NullString {
	return sql.NullString{}
}

// isNotFoundErr is the local heuristic for "the backend doesn't have
// this sandbox anymore." fcagent returns gRPC NotFound; the firecracker
// backend returns a fmt.Errorf with "VM not found" in the message.
// We deliberately don't import the gRPC code constants here so step()
// stays free of network-layer deps.
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return containsAny(msg, []string{
		"not found",
		"NotFound",
		"code = NotFound",
	})
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		for i := 0; i+len(n) <= len(s); i++ {
			if s[i:i+len(n)] == n {
				return true
			}
		}
	}
	return false
}
