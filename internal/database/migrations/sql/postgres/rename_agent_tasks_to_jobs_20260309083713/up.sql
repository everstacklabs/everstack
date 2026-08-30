-- Rename agent_tasks table to agent_jobs
ALTER TABLE agent_tasks RENAME TO agent_jobs;

-- Rename all indexes
ALTER INDEX IF EXISTS idx_agent_tasks_dequeue RENAME TO idx_agent_jobs_dequeue;
ALTER INDEX IF EXISTS idx_agent_tasks_tenant RENAME TO idx_agent_jobs_tenant;
ALTER INDEX IF EXISTS idx_agent_tasks_status RENAME TO idx_agent_jobs_status;
ALTER INDEX IF EXISTS idx_agent_tasks_worker RENAME TO idx_agent_jobs_worker;
ALTER INDEX IF EXISTS idx_agent_tasks_session RENAME TO idx_agent_jobs_session;
ALTER INDEX IF EXISTS idx_agent_tasks_spawn_tree RENAME TO idx_agent_jobs_spawn_tree;
