-- Build the old constraint first: duplicates fail without removing the active index.
CREATE UNIQUE INDEX uk_conversation_projects_user_path_rollback ON conversation_groups(user_id, project_path);
DROP INDEX uk_conversation_projects_user_path;
CREATE UNIQUE INDEX uk_conversation_projects_user_path ON conversation_groups(user_id, project_path);
DROP INDEX uk_conversation_projects_user_path_rollback;
