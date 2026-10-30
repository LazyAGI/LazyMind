-- +migrate Dialect *
ALTER TABLE academic_user_paper_states ADD COLUMN reading_status VARCHAR(16) NOT NULL DEFAULT 'unread';
-- Shared reading values have no reliable personal ownership; do not copy them.
ALTER TABLE paper_library_entries DROP COLUMN status;
