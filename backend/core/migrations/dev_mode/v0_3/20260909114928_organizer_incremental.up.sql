ALTER TABLE conversation_organizer_runs ADD COLUMN protocol_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE conversation_organizer_snapshot_items ADD COLUMN ordinal INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversation_organizer_snapshot_items ADD COLUMN frozen_input JSON;
ALTER TABLE conversation_organizer_snapshot_items ADD COLUMN preparation_status VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE conversation_organizer_snapshot_items ADD COLUMN preparation_reason VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE conversation_organizer_snapshot_items ADD COLUMN preparation_error VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE conversation_organizer_snapshot_items ADD COLUMN assignment VARCHAR(255) NOT NULL DEFAULT '';
CREATE TABLE conversation_organizer_candidates (
 run_id VARCHAR(64) NOT NULL REFERENCES conversation_organizer_runs(id) ON DELETE CASCADE,
 id VARCHAR(255) NOT NULL, data JSON NOT NULL, PRIMARY KEY (run_id,id)
);
CREATE INDEX idx_organizer_items_cursor ON conversation_organizer_snapshot_items(run_id,ordinal);
CREATE INDEX idx_organizer_items_assignment ON conversation_organizer_snapshot_items(run_id,assignment,ordinal);
