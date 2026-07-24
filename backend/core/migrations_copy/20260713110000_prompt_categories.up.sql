CREATE TABLE IF NOT EXISTS prompt_categories (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(64) NOT NULL,
    create_user_id VARCHAR(255) NOT NULL,
    create_user_name VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_prompt_categories_user_name
    ON prompt_categories (create_user_id, name);
