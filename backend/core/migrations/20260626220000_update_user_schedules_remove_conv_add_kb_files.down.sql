ALTER TABLE user_schedules DROP COLUMN IF EXISTS kb_ids;
ALTER TABLE user_schedules DROP COLUMN IF EXISTS file_ids;
ALTER TABLE user_schedules ADD COLUMN IF NOT EXISTS conversation_id VARCHAR(36);
