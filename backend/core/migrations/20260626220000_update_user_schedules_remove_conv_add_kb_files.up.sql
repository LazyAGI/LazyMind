-- Add kb_ids and file_ids to user_schedules; drop conversation_id (each trigger now creates a fresh conversation)
ALTER TABLE user_schedules
    ADD COLUMN IF NOT EXISTS kb_ids   TEXT NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS file_ids TEXT NOT NULL DEFAULT '[]';

ALTER TABLE user_schedules DROP COLUMN IF EXISTS conversation_id;
