ALTER TABLE conversation_groups ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT 'group';
ALTER TABLE conversation_groups ADD COLUMN workspace_id VARCHAR(64);
ALTER TABLE conversation_groups ADD COLUMN project_path TEXT;
DROP INDEX uk_conversation_groups_user_name;
CREATE UNIQUE INDEX uk_conversation_groups_user_name ON conversation_groups(user_id, normalized_name) WHERE kind = 'group';
CREATE UNIQUE INDEX uk_conversation_projects_user_path ON conversation_groups(user_id, project_path);
