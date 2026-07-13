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

INSERT INTO prompt_user_states (
    id,
    prompt_id,
    is_favorite,
    usage_count,
    create_user_id,
    create_user_name,
    created_at,
    updated_at
)
SELECT
    'pus_' || md5(create_user_id || ':' || prompt_id),
    prompt_id,
    TRUE,
    0,
    create_user_id,
    MIN(create_user_name),
    MIN(created_at),
    MAX(updated_at)
FROM default_prompts
WHERE deleted_at IS NULL
GROUP BY create_user_id, prompt_id
ON CONFLICT (create_user_id, prompt_id) DO UPDATE
SET is_favorite = TRUE,
    updated_at = EXCLUDED.updated_at;
