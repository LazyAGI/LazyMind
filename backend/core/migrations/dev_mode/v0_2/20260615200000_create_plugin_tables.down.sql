-- +migrate Dialect postgres
DROP TABLE IF EXISTS plugin_slot_revisions;
DROP TABLE IF EXISTS plugin_session_steps;
DROP TABLE IF EXISTS plugin_sessions;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
