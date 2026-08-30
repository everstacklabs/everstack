-- Schema additions for the sandbox lifecycle reconciler.
-- See docs/design/sandbox-reconciler.md and sandbox-reconciler-plan.md.
--
-- The DB row becomes the single source of truth for sandbox state.
-- The reconciler claims rows with reconcile_after <= NOW() that are
-- in a non-terminal lifecycle state, drives them through step()
-- transitions, and writes back. NOTIFY pushes per-row events to a
-- gateway-side LISTEN goroutine that fans out to per-tenant SSE
-- subscribers.
--
-- Idempotent — safe to apply on databases that already have these
-- columns; the reconciler is gated by EVS_SANDBOX_RECONCILER_ENABLED
-- so the new code path does nothing until the flag flips.

ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS desired_state TEXT NOT NULL DEFAULT 'running',
    -- "running" | "sleeping" | "terminated" — what the user wants the
    -- system to converge to. The reconciler diffs current vs desired
    -- and chooses the next transition.

    ADD COLUMN IF NOT EXISTS reconcile_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- A row is eligible for reconciliation when reconcile_after <= NOW().
    -- Used for backoff after a failed attempt and for parking converged
    -- (running, sleeping) rows far in the future so they don't get
    -- claimed every tick.

    ADD COLUMN IF NOT EXISTS reconcile_attempts INT NOT NULL DEFAULT 0,
    -- Bounded retry counter. After N consecutive failures on a
    -- convergence-driving state (creating, stopping, reviving,
    -- terminating) the row transitions to terminal `failed` with the
    -- last error string. State-only archiving is also claimable, but it
    -- does not call a backend. Running rows never increment this — see
    -- step()'s critical corollary.

    ADD COLUMN IF NOT EXISTS reconcile_locked_by TEXT,
    -- Leader id (gateway pod name + uuid) that currently owns this row.
    -- Belt-and-suspenders defense alongside FOR UPDATE SKIP LOCKED so a
    -- stuck transaction's row can be force-released by ops if needed.

    ADD COLUMN IF NOT EXISTS reconcile_locked_at TIMESTAMPTZ,
    -- When the lock was taken. Operators can find rows locked > N min
    -- and intervene if the leader pod died mid-transition.

    ADD COLUMN IF NOT EXISTS agent_target TEXT;
    -- Which fcagent (host:port) owns this sandbox. Set by step() at
    -- creating → running. Survives gateway restart so Exec / Shell /
    -- Logs route correctly without consulting fcagent's discovery
    -- map (which is also lost across restarts).

-- Partial index for the reconciler's "find me work" query. Excludes
-- terminal states (failed, terminated) and converged states whose
-- reconcile_after sits far in the future.
CREATE INDEX IF NOT EXISTS idx_sandbox_instances_reconcile_due
    ON sandbox_instances (reconcile_after)
    WHERE lifecycle_state IN ('pending', 'creating', 'stopping', 'reviving', 'archiving', 'terminating');

-- NOTIFY trigger: every state-changing UPDATE / INSERT pushes a
-- compact JSON payload onto the 'sandbox_events' channel. The gateway's
-- LISTEN goroutine fans these out to per-tenant SSE subscribers so the
-- admin UI sees state changes within ~50ms instead of polling every
-- 1.5s. Mirrors the pattern already in use by sandbox_crons NOTIFY.
CREATE OR REPLACE FUNCTION notify_sandbox_lifecycle_event() RETURNS trigger AS $$
BEGIN
    -- Only fire on actual state changes to keep the channel quiet.
    IF TG_OP = 'INSERT'
       OR (NEW.lifecycle_state IS DISTINCT FROM OLD.lifecycle_state)
       OR (NEW.status IS DISTINCT FROM OLD.status) THEN
        PERFORM pg_notify(
            'sandbox_events',
            json_build_object(
                'id',              NEW.id,
                'tenant_id',       NEW.tenant_id,
                'session_id',      NEW.session_id,
                'lifecycle_state', NEW.lifecycle_state,
                'status',          NEW.status,
                'updated_at',      EXTRACT(EPOCH FROM NEW.updated_at)
            )::text
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS sandbox_instances_lifecycle_notify ON sandbox_instances;
CREATE TRIGGER sandbox_instances_lifecycle_notify
    AFTER INSERT OR UPDATE OF lifecycle_state, status
    ON sandbox_instances
    FOR EACH ROW
    EXECUTE FUNCTION notify_sandbox_lifecycle_event();
