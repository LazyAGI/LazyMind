-- +migrate Dialect postgres
ALTER TABLE user_schedules DROP COLUMN IF EXISTS kb_ids;
ALTER TABLE user_schedules DROP COLUMN IF EXISTS file_ids;
ALTER TABLE user_schedules ADD COLUMN IF NOT EXISTS conversation_id VARCHAR(36);

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
