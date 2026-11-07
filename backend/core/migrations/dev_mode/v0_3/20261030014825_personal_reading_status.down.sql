-- +migrate Dialect *
ALTER TABLE academic_user_paper_states DROP COLUMN reading_status;
-- Discarded shared values cannot be recovered on rollback.
ALTER TABLE paper_library_entries ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'unread';
