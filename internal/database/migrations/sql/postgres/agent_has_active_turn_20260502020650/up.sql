-- Issue 9 from project_sandbox_launch_open_issues.md: agent lifecycle
-- should be derived from sandbox lifecycle, not a parallel state machine.
-- The reconciler writes agent_definitions.lifecycle_status whenever a
-- sandbox transitions; the per-turn 'running' vs 'idle' distinction is
-- the only orthogonal bit.
--
-- has_active_turn is set/cleared by the agent runtime's turn-start and
-- turn-end handlers. The reconciler reads it to decide whether a
-- StateRunning sandbox should map agent to 'running' (turn in flight)
-- or 'idle' (alive but quiet).
--
-- Idempotent — safe to apply on databases that already have the column.

ALTER TABLE agent_definitions
    ADD COLUMN IF NOT EXISTS has_active_turn BOOLEAN NOT NULL DEFAULT false;
