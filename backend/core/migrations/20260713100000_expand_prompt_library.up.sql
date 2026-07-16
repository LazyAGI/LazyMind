ALTER TABLE prompts
    ADD COLUMN IF NOT EXISTS category VARCHAR(64) NOT NULL DEFAULT 'custom';

CREATE TABLE IF NOT EXISTS prompt_user_states (
    id VARCHAR(64) PRIMARY KEY,
    prompt_id VARCHAR(64) NOT NULL,
    is_favorite BOOLEAN NOT NULL DEFAULT FALSE,
    usage_count BIGINT NOT NULL DEFAULT 0,
    last_used_at TIMESTAMP WITH TIME ZONE,
    create_user_id VARCHAR(255) NOT NULL,
    create_user_name VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_prompt_user_states_user_prompt
    ON prompt_user_states (create_user_id, prompt_id);

DROP TABLE IF EXISTS default_prompts CASCADE;
