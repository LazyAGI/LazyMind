-- Refuse a downgrade that would merge independent project histories.
-- Build the old constraint before dropping the typed one, so conflicts are safe.
CREATE UNIQUE INDEX uk_conversation_projects_user_path_rollback ON conversation_groups(user_id,project_path) WHERE kind='project' AND deleted_at IS NULL;
DROP INDEX uk_conversation_projects_user_path;
CREATE UNIQUE INDEX uk_conversation_projects_user_path ON conversation_groups(user_id,project_path) WHERE kind='project' AND deleted_at IS NULL;
DROP INDEX uk_conversation_projects_user_path_rollback;
