CREATE TABLE IF NOT EXISTS external_agent_workflow_tasks (
    id VARCHAR(36) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL DEFAULT '',
    agent_type VARCHAR(32) NOT NULL,
    external_conversation_id VARCHAR(255) NOT NULL DEFAULT '',
    external_thread_id VARCHAR(255) NOT NULL DEFAULT '',
    skill_id VARCHAR(255) NOT NULL,
    skill_revision_id VARCHAR(255) NOT NULL DEFAULT '',
    task_description TEXT NOT NULL DEFAULT '',
    draft_id VARCHAR(36) NOT NULL DEFAULT '',
    workflow_ref VARCHAR(512) NOT NULL DEFAULT '',
    workflow_id VARCHAR(255) NOT NULL DEFAULT '',
    workflow_revision_id VARCHAR(36) NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    conversation_id VARCHAR(36) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    stage VARCHAR(32) NOT NULL DEFAULT 'preflight',
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    suggestion TEXT NOT NULL DEFAULT '',
    request_json JSONB NOT NULL DEFAULT '{}',
    result_summary_json JSONB NOT NULL DEFAULT '{}',
    result_artifacts_json JSONB NOT NULL DEFAULT '[]',
    lazymind_url VARCHAR(1024) NOT NULL DEFAULT '',
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_workflow_task_owner_key UNIQUE (owner_user_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_external_agent_workflow_tasks_owner_status
    ON external_agent_workflow_tasks(owner_user_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_external_agent_workflow_tasks_agent
    ON external_agent_workflow_tasks(agent_type);
CREATE INDEX IF NOT EXISTS idx_external_agent_workflow_tasks_skill
    ON external_agent_workflow_tasks(skill_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_workflow_tasks_external_conversation
    ON external_agent_workflow_tasks(external_conversation_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_workflow_tasks_draft
    ON external_agent_workflow_tasks(draft_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_workflow_tasks_session
    ON external_agent_workflow_tasks(session_id);

CREATE TABLE IF NOT EXISTS external_agent_skill_sources (
    id VARCHAR(36) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    source_type VARCHAR(32) NOT NULL,
    source_key VARCHAR(128) NOT NULL,
    source_name VARCHAR(255) NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    resolved_skill_id VARCHAR(36) NOT NULL DEFAULT '',
    install_status VARCHAR(32) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_skill_source UNIQUE (owner_user_id, source_type, source_key)
);

CREATE INDEX IF NOT EXISTS idx_external_agent_skill_sources_owner
    ON external_agent_skill_sources(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_skill_sources_skill
    ON external_agent_skill_sources(resolved_skill_id);
