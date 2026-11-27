-- Bind persisted /workspace tarballs to the sandbox incarnation and run that
-- produced them, and make terminate's object deletion durable.
--
-- Sandbox ids are reused (persistent agents always use wks_<agent_id>), so
-- the sandbox id alone cannot tell this sandbox's tarballs from those of a
-- previous, terminated sandbox with the same id. Object keys now carry:
--   workspace/<workspace_incarnation>/<workspace_run_id>/<generation>.tar.gz
-- workspace_incarnation changes when a terminated/failed row is reused for a
-- new sandbox; workspace_run_id changes every time the sandbox starts
-- running (create or wake). A row without a ref only ever adopts tarballs
-- from its current incarnation and run.
ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS workspace_incarnation TEXT NOT NULL DEFAULT gen_random_uuid()::text;
ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS workspace_run_id TEXT NOT NULL DEFAULT gen_random_uuid()::text;

-- Outbox for deleting a terminated incarnation's workspace objects. The
-- terminate transition enqueues a row in the same transaction; a worker
-- deletes the incarnation's prefix and removes the row, retrying with
-- backoff until it succeeds, even if the sandbox id is reused meanwhile.
CREATE TABLE IF NOT EXISTS sandbox_workspace_purges (
    sandbox_id      TEXT        NOT NULL,
    incarnation     TEXT        NOT NULL,
    tenant_id       TEXT        NOT NULL,
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    PRIMARY KEY (sandbox_id, incarnation)
);
CREATE INDEX IF NOT EXISTS idx_sandbox_workspace_purges_due
    ON sandbox_workspace_purges (next_attempt_at);
