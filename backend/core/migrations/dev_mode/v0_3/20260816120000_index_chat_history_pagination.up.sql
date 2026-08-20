CREATE INDEX IF NOT EXISTS idx_chat_histories_conversation_seq
    ON chat_histories (conversation_id, seq DESC);
