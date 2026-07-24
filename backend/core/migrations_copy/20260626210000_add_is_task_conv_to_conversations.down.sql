ALTER TABLE conversations DROP COLUMN IF EXISTS is_task_conv;
DROP INDEX IF EXISTS idx_conversations_is_task_conv;
