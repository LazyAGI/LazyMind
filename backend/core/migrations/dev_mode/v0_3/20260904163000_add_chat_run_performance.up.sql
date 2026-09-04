-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS chat_run_performance (
    run_id VARCHAR(64) PRIMARY KEY,
    conversation_id VARCHAR(36) NOT NULL,
    history_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    turn_seq INTEGER,
    schema_version INTEGER NOT NULL,
    status VARCHAR(32) NOT NULL,
    model VARCHAR(255) NOT NULL DEFAULT '',
    steps INTEGER NOT NULL DEFAULT 0 CHECK (steps >= 0),
    model_steps INTEGER NOT NULL DEFAULT 0 CHECK (model_steps >= 0),
    tool_steps INTEGER NOT NULL DEFAULT 0 CHECK (tool_steps >= 0),
    wall_ms BIGINT CHECK (wall_ms IS NULL OR wall_ms >= 0),
    model_ms BIGINT CHECK (model_ms IS NULL OR model_ms >= 0),
    tool_ms BIGINT CHECK (tool_ms IS NULL OR tool_ms >= 0),
    ttft_ms BIGINT CHECK (ttft_ms IS NULL OR ttft_ms >= 0),
    input_tokens BIGINT CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens BIGINT CHECK (output_tokens IS NULL OR output_tokens >= 0),
    total_tokens BIGINT CHECK (total_tokens IS NULL OR total_tokens >= 0),
    cached_tokens BIGINT CHECK (cached_tokens IS NULL OR cached_tokens >= 0),
    cache_input_tokens BIGINT CHECK (cache_input_tokens IS NULL OR cache_input_tokens >= 0),
    reasoning_tokens BIGINT CHECK (reasoning_tokens IS NULL OR reasoning_tokens >= 0),
    max_input_tokens BIGINT CHECK (max_input_tokens IS NULL OR max_input_tokens >= 0),
    context_input_tokens BIGINT CHECK (context_input_tokens IS NULL OR context_input_tokens >= 0),
    observed_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_run_performance_conversation_id ON chat_run_performance(conversation_id);
CREATE INDEX IF NOT EXISTS idx_chat_run_performance_history_id ON chat_run_performance(history_id);
CREATE INDEX IF NOT EXISTS idx_chat_run_performance_user_id ON chat_run_performance(user_id);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS chat_run_performance (
    run_id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    history_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    turn_seq INTEGER,
    schema_version INTEGER NOT NULL,
    status TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    steps INTEGER NOT NULL DEFAULT 0 CHECK (steps >= 0),
    model_steps INTEGER NOT NULL DEFAULT 0 CHECK (model_steps >= 0),
    tool_steps INTEGER NOT NULL DEFAULT 0 CHECK (tool_steps >= 0),
    wall_ms INTEGER CHECK (wall_ms IS NULL OR wall_ms >= 0),
    model_ms INTEGER CHECK (model_ms IS NULL OR model_ms >= 0),
    tool_ms INTEGER CHECK (tool_ms IS NULL OR tool_ms >= 0),
    ttft_ms INTEGER CHECK (ttft_ms IS NULL OR ttft_ms >= 0),
    input_tokens INTEGER CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens INTEGER CHECK (output_tokens IS NULL OR output_tokens >= 0),
    total_tokens INTEGER CHECK (total_tokens IS NULL OR total_tokens >= 0),
    cached_tokens INTEGER CHECK (cached_tokens IS NULL OR cached_tokens >= 0),
    cache_input_tokens INTEGER CHECK (cache_input_tokens IS NULL OR cache_input_tokens >= 0),
    reasoning_tokens INTEGER CHECK (reasoning_tokens IS NULL OR reasoning_tokens >= 0),
    max_input_tokens INTEGER CHECK (max_input_tokens IS NULL OR max_input_tokens >= 0),
    context_input_tokens INTEGER CHECK (context_input_tokens IS NULL OR context_input_tokens >= 0),
    observed_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_run_performance_conversation_id ON chat_run_performance(conversation_id);
CREATE INDEX IF NOT EXISTS idx_chat_run_performance_history_id ON chat_run_performance(history_id);
CREATE INDEX IF NOT EXISTS idx_chat_run_performance_user_id ON chat_run_performance(user_id);
