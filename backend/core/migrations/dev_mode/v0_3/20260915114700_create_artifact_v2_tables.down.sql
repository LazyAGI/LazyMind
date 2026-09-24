-- +migrate Dialect postgres
DROP TRIGGER IF EXISTS artifact_revisions_no_update ON artifact_revisions;
DROP FUNCTION IF EXISTS artifact_revisions_immutable();
DROP TABLE IF EXISTS artifact_event_outbox;
DROP TABLE IF EXISTS artifact_idempotency;
DROP TABLE IF EXISTS artifact_dependencies;
DROP TABLE IF EXISTS artifact_bindings;
DROP TABLE IF EXISTS artifact_heads;
DROP TABLE IF EXISTS artifact_revisions;
DROP TABLE IF EXISTS artifact_blobs;
DROP TABLE IF EXISTS artifacts;

-- +migrate Dialect sqlite
DROP TRIGGER IF EXISTS artifact_revisions_no_update;
DROP TABLE IF EXISTS artifact_event_outbox;
DROP TABLE IF EXISTS artifact_idempotency;
DROP TABLE IF EXISTS artifact_dependencies;
DROP TABLE IF EXISTS artifact_bindings;
DROP TABLE IF EXISTS artifact_heads;
DROP TABLE IF EXISTS artifact_revisions;
DROP TABLE IF EXISTS artifact_blobs;
DROP TABLE IF EXISTS artifacts;

