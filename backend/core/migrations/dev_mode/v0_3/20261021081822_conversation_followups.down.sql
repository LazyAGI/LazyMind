-- +migrate Dialect postgres,sqlite
DROP INDEX IF EXISTS uk_conversation_followups_idempotency;
DROP INDEX IF EXISTS idx_conversation_followups_queue;
DROP TABLE IF EXISTS conversation_followups;
