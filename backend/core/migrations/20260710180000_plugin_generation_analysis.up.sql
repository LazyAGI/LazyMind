ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS source_skill_revision_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS source_skill_revision_no BIGINT NOT NULL DEFAULT 0;
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS source_skill_tree_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS source_analysis_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE plugin_drafts ALTER COLUMN generate_status TYPE VARCHAR(32);

CREATE TABLE IF NOT EXISTS plugin_generation_analyses (
    id VARCHAR(36) PRIMARY KEY,
    draft_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    source_type VARCHAR(16) NOT NULL,
    source_skill_id VARCHAR(36) NOT NULL DEFAULT '',
    source_skill_revision_id VARCHAR(36) NOT NULL DEFAULT '',
    source_skill_revision_no BIGINT NOT NULL DEFAULT 0,
    source_skill_tree_hash VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL,
    verdict_code VARCHAR(64) NOT NULL DEFAULT '',
    verdict_message TEXT NOT NULL DEFAULT '',
    candidates_json TEXT NOT NULL DEFAULT '[]',
    selected_candidate_id VARCHAR(128) NOT NULL DEFAULT '',
    coverage_report_json TEXT NOT NULL DEFAULT '{}',
    tool_mapping_report_json TEXT NOT NULL DEFAULT '{}',
    script_report_json TEXT NOT NULL DEFAULT '{}',
    source_package_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_plugin_generation_analyses_draft ON plugin_generation_analyses(draft_id, created_at);

CREATE TABLE IF NOT EXISTS plugin_repair_runs (
    id VARCHAR(36) PRIMARY KEY,
    draft_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    base_plugin_revision_id VARCHAR(36) NOT NULL DEFAULT '',
    draft_version_before INT NOT NULL,
    target VARCHAR(32) NOT NULL,
    mode VARCHAR(32) NOT NULL,
    source_analysis_id VARCHAR(36) NOT NULL DEFAULT '',
    source_skill_revision_id VARCHAR(36) NOT NULL DEFAULT '',
    repair_hint TEXT NOT NULL DEFAULT '',
    diagnostics_before_json TEXT NOT NULL DEFAULT '{}',
    changes_json TEXT NOT NULL DEFAULT '{}',
    diagnostics_after_json TEXT NOT NULL DEFAULT '{}',
    status VARCHAR(32) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_plugin_repair_runs_draft ON plugin_repair_runs(draft_id, created_at);
