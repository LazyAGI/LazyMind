-- Keep both groups and their members when returning to the shared namespace.
UPDATE conversation_groups SET name=name || ' [' || id || ']', normalized_name=normalized_name || '#task#' || id
WHERE kind='group' AND is_task_conv=TRUE AND EXISTS (
 SELECT 1 FROM conversation_groups other WHERE other.user_id=conversation_groups.user_id
 AND other.kind='group' AND other.is_task_conv=FALSE AND other.normalized_name=conversation_groups.normalized_name
);
DROP INDEX uk_conversation_groups_user_name;
ALTER TABLE conversation_groups DROP COLUMN is_task_conv;
CREATE UNIQUE INDEX uk_conversation_groups_user_name ON conversation_groups(user_id, normalized_name) WHERE kind = 'group';
