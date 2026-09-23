-- Keep project records and membership when downgrading; disambiguate their names.
UPDATE conversation_groups SET normalized_name = id || '#project#' WHERE kind = 'project';
DROP INDEX uk_conversation_projects_user_path;
DROP INDEX uk_conversation_groups_user_name;
ALTER TABLE conversation_groups DROP COLUMN project_path;
ALTER TABLE conversation_groups DROP COLUMN workspace_id;
ALTER TABLE conversation_groups DROP COLUMN kind;
CREATE UNIQUE INDEX uk_conversation_groups_user_name ON conversation_groups(user_id, normalized_name);
