-- Split project identity by member type, including archived and deleted history.
-- Run with writers stopped. Workspace grants and conversation bindings are preserved.
DROP INDEX uk_conversation_projects_user_path;
CREATE UNIQUE INDEX uk_conversation_projects_user_path ON conversation_groups(user_id,is_task_conv,project_path) WHERE kind='project' AND deleted_at IS NULL;
-- Side conversations inherit their parent type and must stay in the same project.
UPDATE conversations SET is_task_conv=(SELECT p.is_task_conv FROM conversations p WHERE p.id=conversations.parent_conversation_id)
WHERE parent_conversation_id IS NOT NULL AND EXISTS (
 SELECT 1 FROM conversations p WHERE p.id=conversations.parent_conversation_id AND p.is_task_conv<>conversations.is_task_conv
) AND id IN (SELECT m.conversation_id FROM conversation_group_members m JOIN conversation_groups g ON g.id=m.group_id WHERE g.kind='project');
CREATE TEMP TABLE conversation_project_type_migration (old_id VARCHAR(36) PRIMARY KEY, task_id VARCHAR(36) NOT NULL);
-- +migrate Dialect postgres
INSERT INTO conversation_project_type_migration(old_id, task_id)
SELECT g.id, CASE WHEN EXISTS (
 SELECT 1 FROM conversation_group_members m JOIN conversations c ON c.id=m.conversation_id
 WHERE m.group_id=g.id AND COALESCE(c.is_task_conv,FALSE)=FALSE
) THEN md5(random()::text || clock_timestamp()::text) ELSE g.id END
FROM conversation_groups g WHERE g.kind='project' AND EXISTS (
 SELECT 1 FROM conversation_group_members m JOIN conversations c ON c.id=m.conversation_id
 WHERE m.group_id=g.id AND c.is_task_conv=TRUE
);
-- +migrate Dialect sqlite
INSERT INTO conversation_project_type_migration(old_id, task_id)
SELECT g.id, CASE WHEN EXISTS (
 SELECT 1 FROM conversation_group_members m JOIN conversations c ON c.id=m.conversation_id
 WHERE m.group_id=g.id AND COALESCE(c.is_task_conv,FALSE)=FALSE
) THEN lower(hex(randomblob(16))) ELSE g.id END
FROM conversation_groups g WHERE g.kind='project' AND EXISTS (
 SELECT 1 FROM conversation_group_members m JOIN conversations c ON c.id=m.conversation_id
 WHERE m.group_id=g.id AND c.is_task_conv=TRUE
);
-- +migrate Dialect *
INSERT INTO conversation_groups (id,user_id,name,normalized_name,scope,version,created_by,created_run_id,created_at,updated_at,deleted_at,pinned,sort_order,kind,workspace_id,project_path,is_task_conv)
SELECT t.task_id,g.user_id,g.name,g.normalized_name,g.scope,1,'user','',g.created_at,CURRENT_TIMESTAMP,g.deleted_at,g.pinned,g.sort_order,'project',g.workspace_id,g.project_path,TRUE
FROM conversation_groups g JOIN conversation_project_type_migration t ON t.old_id=g.id WHERE t.task_id<>t.old_id;
UPDATE conversation_groups SET
 is_task_conv=CASE WHEN id IN (SELECT old_id FROM conversation_project_type_migration WHERE old_id=task_id) THEN TRUE ELSE is_task_conv END,
 version=version+1, updated_at=CURRENT_TIMESTAMP
WHERE id IN (SELECT old_id FROM conversation_project_type_migration);
UPDATE conversation_group_members SET
 group_id=(SELECT t.task_id FROM conversation_project_type_migration t WHERE t.old_id=conversation_group_members.group_id),
 revision=revision+1, source='user', source_run_id='', updated_at=CURRENT_TIMESTAMP
WHERE group_id IN (SELECT old_id FROM conversation_project_type_migration)
 AND conversation_id IN (SELECT id FROM conversations WHERE is_task_conv=TRUE);
INSERT INTO conversation_group_states (conversation_id,user_id,group_id,revision,source_run_id,updated_at)
SELECT m.conversation_id,m.user_id,m.group_id,m.revision,'',CURRENT_TIMESTAMP
FROM conversation_group_members m JOIN conversations c ON c.id=m.conversation_id
WHERE c.is_task_conv=TRUE AND m.group_id IN (SELECT task_id FROM conversation_project_type_migration)
ON CONFLICT(conversation_id) DO UPDATE SET group_id=excluded.group_id,
 revision=conversation_group_states.revision+1,source_run_id='',updated_at=CURRENT_TIMESTAMP;
DROP TABLE conversation_project_type_migration;
