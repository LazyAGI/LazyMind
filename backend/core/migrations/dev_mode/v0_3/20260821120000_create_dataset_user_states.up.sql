-- 20260821120000_create_dataset_user_states
-- +migrate Up
-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.dataset_user_states (
    id VARCHAR(64) PRIMARY KEY,
    dataset_id VARCHAR(255) NOT NULL,
    usage_count BIGINT NOT NULL DEFAULT 0,
    last_used_at TIMESTAMP WITH TIME ZONE,
    create_user_id VARCHAR(255) NOT NULL,
    create_user_name VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_dataset_user_states_user_dataset
    ON public.dataset_user_states (create_user_id, dataset_id);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS dataset_user_states (
    id varchar(64) PRIMARY KEY,
    dataset_id varchar(255) NOT NULL,
    usage_count bigint NOT NULL DEFAULT 0,
    last_used_at datetime,
    create_user_id varchar(255) NOT NULL,
    create_user_name varchar(255) NOT NULL DEFAULT '',
    created_at datetime NOT NULL,
    updated_at datetime NOT NULL,
    deleted_at datetime
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_dataset_user_states_user_dataset
    ON dataset_user_states (create_user_id, dataset_id);
