-- Add is_task_conv flag to conversations to identify task-triggered conversations
ALTER TABLE conversations
    ADD COLUMN IF NOT EXISTS is_task_conv BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_conversations_is_task_conv ON conversations(is_task_conv);
