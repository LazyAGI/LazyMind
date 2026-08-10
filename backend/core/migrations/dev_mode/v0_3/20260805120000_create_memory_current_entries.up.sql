-- 20260805120000_create_memory_current_entries
-- +migrate Up
-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.memory_current_entries (
    user_id VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    entry_type VARCHAR(16) NOT NULL,
    content BYTEA,
    size BIGINT NOT NULL DEFAULT 0,
    mime VARCHAR(128) NOT NULL DEFAULT '',
    file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    "binary" BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, path),
    CONSTRAINT chk_memory_current_entry_type CHECK (entry_type IN ('file', 'dir')),
    CONSTRAINT chk_memory_current_entry_content CHECK (
        (entry_type = 'file' AND content IS NOT NULL)
        OR (entry_type = 'dir' AND content IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_memory_current_entries_user_path
    ON public.memory_current_entries (user_id, path);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS memory_current_entries (
    user_id varchar(255) NOT NULL,
    path varchar(1024) NOT NULL,
    entry_type varchar(16) NOT NULL,
    content BLOB,
    size integer NOT NULL DEFAULT 0,
    mime varchar(128) NOT NULL DEFAULT '',
    file_type varchar(32) NOT NULL DEFAULT 'unknown',
    "binary" boolean NOT NULL DEFAULT false,
    created_at datetime NOT NULL,
    updated_at datetime NOT NULL,
    PRIMARY KEY (user_id, path),
    CONSTRAINT chk_memory_current_entry_type CHECK (entry_type IN ('file', 'dir')),
    CONSTRAINT chk_memory_current_entry_content CHECK (
        (entry_type = 'file' AND content IS NOT NULL)
        OR (entry_type = 'dir' AND content IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_memory_current_entries_user_path
    ON memory_current_entries (user_id, path);
