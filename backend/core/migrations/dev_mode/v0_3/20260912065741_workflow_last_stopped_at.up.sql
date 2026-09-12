-- +migrate Dialect postgres
ALTER TABLE plugin_sessions ADD COLUMN last_stopped_at TIMESTAMP WITH TIME ZONE NULL;

-- +migrate Dialect sqlite
ALTER TABLE plugin_sessions ADD COLUMN last_stopped_at DATETIME NULL;
