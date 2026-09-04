-- +migrate Dialect postgres
ALTER TABLE external_agent_bindings
    ADD COLUMN IF NOT EXISTS managed_by_lazymind BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE external_agent_bindings
SET managed_by_lazymind = TRUE
WHERE EXISTS (
    SELECT 1
    FROM external_agent_runs
    WHERE external_agent_runs.conversation_id = external_agent_bindings.conversation_id
      AND external_agent_runs.provider = external_agent_bindings.provider
      AND external_agent_runs.host_id = external_agent_bindings.host_id
      AND external_agent_runs.provider_thread_id = external_agent_bindings.provider_thread_id
      AND external_agent_runs.action = 'start'
      AND external_agent_runs.created_at <= external_agent_bindings.created_at
);

-- +migrate Dialect sqlite
ALTER TABLE external_agent_bindings
    ADD COLUMN managed_by_lazymind BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE external_agent_bindings
SET managed_by_lazymind = TRUE
WHERE EXISTS (
    SELECT 1
    FROM external_agent_runs
    WHERE external_agent_runs.conversation_id = external_agent_bindings.conversation_id
      AND external_agent_runs.provider = external_agent_bindings.provider
      AND external_agent_runs.host_id = external_agent_bindings.host_id
      AND external_agent_runs.provider_thread_id = external_agent_bindings.provider_thread_id
      AND external_agent_runs.action = 'start'
      AND external_agent_runs.created_at <= external_agent_bindings.created_at
);
