-- 20260805121000_create_episode_memories
-- +migrate Up
-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.episode_memories (
    row_id BIGSERIAL PRIMARY KEY,
    id VARCHAR(36) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(255) NOT NULL,
    source_kind VARCHAR(32) NOT NULL,
    episode_type VARCHAR(16) NOT NULL,
    summary TEXT NOT NULL,
    normalized_summary TEXT NOT NULL,
    search_text TEXT NOT NULL,
    tokenizer_version VARCHAR(64) NOT NULL,
    occurred_at_ms BIGINT NOT NULL,
    recorded_at_ms BIGINT NOT NULL,
    hit_count BIGINT NOT NULL DEFAULT 0,
    search_vector TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', COALESCE(search_text, '')), 'A')
        || setweight(to_tsvector('simple', COALESCE(summary, '')), 'B')
    ) STORED,
    CONSTRAINT uk_episode_memories_user_id UNIQUE (user_id, id),
    CONSTRAINT uk_episode_memories_identity UNIQUE (user_id, conversation_id, normalized_summary),
    CONSTRAINT chk_episode_memories_source_kind CHECK (source_kind IN ('chat_explicit', 'memory_review')),
    CONSTRAINT chk_episode_memories_episode_type CHECK (episode_type IN ('decision', 'progress', 'result', 'blocker', 'event')),
    CONSTRAINT chk_episode_memories_summary CHECK (length(btrim(summary)) BETWEEN 1 AND 200),
    CONSTRAINT chk_episode_memories_search_text CHECK (length(btrim(search_text)) > 0),
    CONSTRAINT chk_episode_memories_tokenizer_version CHECK (length(btrim(tokenizer_version)) > 0),
    CONSTRAINT chk_episode_memories_timestamps CHECK (occurred_at_ms > 0 AND recorded_at_ms > 0),
    CONSTRAINT chk_episode_memories_hit_count CHECK (hit_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_episode_memories_user_recorded
    ON public.episode_memories (user_id, recorded_at_ms DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_episode_memories_user_conversation_recorded
    ON public.episode_memories (user_id, conversation_id, recorded_at_ms ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_episode_memories_search_vector
    ON public.episode_memories USING GIN (search_vector);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS episode_memories (
    row_id INTEGER PRIMARY KEY AUTOINCREMENT,
    id varchar(36) NOT NULL,
    user_id varchar(255) NOT NULL,
    conversation_id varchar(255) NOT NULL,
    source_kind varchar(32) NOT NULL,
    episode_type varchar(16) NOT NULL,
    summary text NOT NULL,
    normalized_summary text NOT NULL,
    search_text text NOT NULL,
    tokenizer_version varchar(64) NOT NULL,
    occurred_at_ms integer NOT NULL,
    recorded_at_ms integer NOT NULL,
    hit_count integer NOT NULL DEFAULT 0,
    CONSTRAINT uk_episode_memories_user_id UNIQUE (user_id, id),
    CONSTRAINT uk_episode_memories_identity UNIQUE (user_id, conversation_id, normalized_summary),
    CONSTRAINT chk_episode_memories_source_kind CHECK (source_kind IN ('chat_explicit', 'memory_review')),
    CONSTRAINT chk_episode_memories_episode_type CHECK (episode_type IN ('decision', 'progress', 'result', 'blocker', 'event')),
    CONSTRAINT chk_episode_memories_summary CHECK (length(trim(summary)) BETWEEN 1 AND 200),
    CONSTRAINT chk_episode_memories_search_text CHECK (length(trim(search_text)) > 0),
    CONSTRAINT chk_episode_memories_tokenizer_version CHECK (length(trim(tokenizer_version)) > 0),
    CONSTRAINT chk_episode_memories_timestamps CHECK (occurred_at_ms > 0 AND recorded_at_ms > 0),
    CONSTRAINT chk_episode_memories_hit_count CHECK (hit_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_episode_memories_user_recorded
    ON episode_memories (user_id, recorded_at_ms DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_episode_memories_user_conversation_recorded
    ON episode_memories (user_id, conversation_id, recorded_at_ms ASC, id ASC);
