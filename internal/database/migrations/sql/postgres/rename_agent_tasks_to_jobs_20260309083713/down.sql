-- Revert: rename agent_jobs back to agent_tasks
ALTER TABLE agent_jobs RENAME TO agent_tasks;

ALTER INDEX IF EXISTS idx_agent_jobs_dequeue RENAME TO idx_agent_tasks_dequeue;
ALTER INDEX IF EXISTS idx_agent_jobs_tenant RENAME TO idx_agent_tasks_tenant;
ALTER INDEX IF EXISTS idx_agent_jobs_status RENAME TO idx_agent_tasks_status;
ALTER INDEX IF EXISTS idx_agent_jobs_worker RENAME TO idx_agent_tasks_worker;
ALTER INDEX IF EXISTS idx_agent_jobs_session RENAME TO idx_agent_tasks_session;
ALTER INDEX IF EXISTS idx_agent_jobs_spawn_tree RENAME TO idx_agent_tasks_spawn_tree;
