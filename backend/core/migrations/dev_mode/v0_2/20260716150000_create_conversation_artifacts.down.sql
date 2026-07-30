-- +migrate Dialect postgres
DROP TABLE IF EXISTS conversation_artifacts;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
