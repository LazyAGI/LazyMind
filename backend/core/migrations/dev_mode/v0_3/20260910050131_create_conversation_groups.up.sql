-- Conversation groups and incremental organizer final schema.
CREATE TABLE conversation_groups (
 pinned BOOLEAN NOT NULL DEFAULT FALSE, sort_order BIGINT NOT NULL DEFAULT 0,
 id VARCHAR(36) PRIMARY KEY, user_id VARCHAR(255) NOT NULL, name VARCHAR(255) NOT NULL,
 normalized_name VARCHAR(255) NOT NULL, scope TEXT NOT NULL DEFAULT '', version BIGINT NOT NULL DEFAULT 1,
 created_by VARCHAR(16) NOT NULL DEFAULT 'user', created_run_id VARCHAR(64) NOT NULL DEFAULT '',
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL, deleted_at TIMESTAMP
);
CREATE UNIQUE INDEX uk_conversation_groups_user_name ON conversation_groups(user_id, normalized_name);
CREATE INDEX idx_conversation_groups_created_run ON conversation_groups(created_run_id);
CREATE TABLE conversation_group_members (
 conversation_id VARCHAR(36) PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
 group_id VARCHAR(36) NOT NULL REFERENCES conversation_groups(id) ON DELETE CASCADE,
 user_id VARCHAR(255) NOT NULL, revision BIGINT NOT NULL DEFAULT 1, source VARCHAR(16) NOT NULL DEFAULT 'user',
 source_run_id VARCHAR(64) NOT NULL DEFAULT '', created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_conversation_group_members_group ON conversation_group_members(group_id);
CREATE INDEX idx_conversation_group_members_user ON conversation_group_members(user_id);
CREATE INDEX idx_conversation_group_members_run ON conversation_group_members(source_run_id);
CREATE TABLE conversation_group_states (
 conversation_id VARCHAR(36) PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
 user_id VARCHAR(255) NOT NULL, group_id VARCHAR(36), revision BIGINT NOT NULL DEFAULT 1,
 source_run_id VARCHAR(64) NOT NULL DEFAULT '', updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_conversation_group_states_user ON conversation_group_states(user_id);
CREATE INDEX idx_conversation_group_states_run ON conversation_group_states(source_run_id);
CREATE TABLE conversation_organizer_runs (
 id VARCHAR(64) PRIMARY KEY, user_id VARCHAR(255) NOT NULL, status VARCHAR(16) NOT NULL,
 stage VARCHAR(32) NOT NULL DEFAULT 'snapshot', snapshot_json JSON NOT NULL, snapshot_hash VARCHAR(64) NOT NULL,
 model_config_json JSON NOT NULL, preparation_json JSON, stream_json JSON, checkpoint_json JSON, proposal_json JSON, result_json JSON,
 progress_current BIGINT NOT NULL DEFAULT 0, progress_total BIGINT NOT NULL DEFAULT 0,
 version BIGINT NOT NULL DEFAULT 1, job_id VARCHAR(64) NOT NULL DEFAULT '', error_code VARCHAR(64) NOT NULL DEFAULT '',
 error_message TEXT NOT NULL DEFAULT '', created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
 finished_at TIMESTAMP, undone_at TIMESTAMP
);
CREATE INDEX idx_conversation_organizer_runs_user ON conversation_organizer_runs(user_id);
CREATE INDEX idx_conversation_organizer_runs_status ON conversation_organizer_runs(status);
CREATE INDEX idx_conversation_organizer_runs_job ON conversation_organizer_runs(job_id);
CREATE UNIQUE INDEX uk_conversation_organizer_active_user ON conversation_organizer_runs(user_id) WHERE status IN ('pending','running','applying');
CREATE TABLE conversation_organizer_snapshot_items (
 ordinal INTEGER NOT NULL DEFAULT 0,
 frozen_input JSON,
 preparation_status VARCHAR(16) NOT NULL DEFAULT '',
 preparation_reason VARCHAR(64) NOT NULL DEFAULT '',
 preparation_error VARCHAR(64) NOT NULL DEFAULT '',
 assignment VARCHAR(255) NOT NULL DEFAULT '',

 run_id VARCHAR(64) NOT NULL REFERENCES conversation_organizer_runs(id) ON DELETE CASCADE,
 conversation_id VARCHAR(36) NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 user_id VARCHAR(255) NOT NULL, title TEXT NOT NULL, summary TEXT NOT NULL,
 title_revision BIGINT NOT NULL DEFAULT 0, metadata_revision BIGINT NOT NULL DEFAULT 0,
 created_at TIMESTAMP NOT NULL, PRIMARY KEY(run_id, conversation_id)
);
CREATE INDEX idx_conversation_organizer_snapshot_conversation ON conversation_organizer_snapshot_items(conversation_id);
CREATE INDEX idx_conversation_organizer_snapshot_user ON conversation_organizer_snapshot_items(user_id);
CREATE TABLE conversation_organizer_candidates (
 run_id VARCHAR(64) NOT NULL REFERENCES conversation_organizer_runs(id) ON DELETE CASCADE,
 id VARCHAR(255) NOT NULL, data JSON NOT NULL, PRIMARY KEY (run_id,id)
);
CREATE INDEX idx_organizer_items_cursor ON conversation_organizer_snapshot_items(run_id,ordinal);
CREATE INDEX idx_organizer_items_assignment ON conversation_organizer_snapshot_items(run_id,assignment,ordinal);
CREATE TABLE conversation_organizer_changes (
 id VARCHAR(64) PRIMARY KEY, run_id VARCHAR(64) NOT NULL REFERENCES conversation_organizer_runs(id) ON DELETE CASCADE,
 conversation_id VARCHAR(36) NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 before_group_id VARCHAR(36), after_group_id VARCHAR(36), after_member_revision BIGINT NOT NULL DEFAULT 0,
 kind VARCHAR(16) NOT NULL, created_at TIMESTAMP NOT NULL, undone_at TIMESTAMP
);
CREATE INDEX idx_conversation_organizer_changes_run ON conversation_organizer_changes(run_id);
CREATE INDEX idx_conversation_organizer_changes_conversation ON conversation_organizer_changes(conversation_id);
