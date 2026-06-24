-- plugin_human_artifacts stores content written by human edits to plugin slots.
-- Structure mirrors sub_agent_artifacts but uses session_id instead of task_id
-- (human edits have no associated SubAgent task).
--
-- Value format is identical to sub_agent_artifacts.value:
--   text (small):   {"text": "..."}
--   text (large):   {"type": "text", "path": "rel/path", "size": N}
--   json (small):   {"data": {...}}
--   json (large):   {"type": "json", "path": "rel/path", "size": N}
--   image:          {"path": "/abs/or/rel/path"}
--   file:           {"filename": "foo.pdf", "path": "...", "size": N}
--
-- plugin_slot_revisions.human_artifact_id references this table for human revisions.

CREATE TABLE IF NOT EXISTS plugin_human_artifacts (
    id           VARCHAR(36)  NOT NULL,
    session_id   VARCHAR(36)  NOT NULL REFERENCES plugin_sessions(id),
    artifact_key VARCHAR(64)  NOT NULL,
    content_type VARCHAR(32)  NOT NULL,
    value        JSONB        NOT NULL,
    caption      TEXT,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL,
    CONSTRAINT plugin_human_artifacts_pkey PRIMARY KEY (id)
);

CREATE INDEX IF NOT EXISTS idx_plugin_human_artifacts_session_key
    ON plugin_human_artifacts (session_id, artifact_key);

-- Add human_artifact_id pointer to plugin_slot_revisions.
-- NULL for AI revisions (those use artifact_seq instead).
-- Non-NULL for human revisions (points to plugin_human_artifacts).
ALTER TABLE plugin_slot_revisions
    ADD COLUMN IF NOT EXISTS human_artifact_id VARCHAR(36)
        REFERENCES plugin_human_artifacts(id);
