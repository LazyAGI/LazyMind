-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS user_github_star_prompts (
    user_id VARCHAR(255) PRIMARY KEY,
    prompted_task_ids TEXT NOT NULL DEFAULT '[]',
    last_prompt_at TIMESTAMP WITH TIME ZONE NULL,
    supported BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS user_github_star_prompts (
    user_id VARCHAR(255) PRIMARY KEY,
    prompted_task_ids TEXT NOT NULL DEFAULT '[]',
    last_prompt_at DATETIME NULL,
    supported BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

