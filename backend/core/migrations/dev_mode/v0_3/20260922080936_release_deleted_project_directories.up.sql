DROP INDEX uk_conversation_projects_user_path;
CREATE UNIQUE INDEX uk_conversation_projects_user_path ON conversation_groups(user_id, project_path) WHERE kind = 'project' AND deleted_at IS NULL;
