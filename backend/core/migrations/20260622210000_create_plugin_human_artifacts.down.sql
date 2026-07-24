ALTER TABLE plugin_slot_revisions
    DROP COLUMN IF EXISTS human_artifact_id;

DROP TABLE IF EXISTS plugin_human_artifacts;
