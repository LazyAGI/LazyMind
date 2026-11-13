CREATE TABLE workflow_action_jobs (
 id VARCHAR(36) PRIMARY KEY,
 session_id VARCHAR(36) NOT NULL,
 slot_id VARCHAR(128) NOT NULL,
 list_index INTEGER NOT NULL,
 base_revision INTEGER NOT NULL,
 base_draft_version BIGINT,
 status VARCHAR(24) NOT NULL,
 version BIGINT NOT NULL,
 request_json TEXT NOT NULL,
 result_json TEXT NOT NULL,
 created_at TIMESTAMP NOT NULL,
 updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_workflow_action_jobs_session ON workflow_action_jobs(session_id);
