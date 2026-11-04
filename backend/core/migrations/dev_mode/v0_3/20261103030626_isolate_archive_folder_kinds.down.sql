-- +migrate Dialect postgres
-- Returning to shared folders merges same-name destinations and preserves every conversation.
CREATE TABLE archive_folder_kind_map (id VARCHAR(36) PRIMARY KEY, target_id VARCHAR(36) NOT NULL);
INSERT INTO archive_folder_kind_map
 SELECT task.id,dialog.id FROM conversation_archive_folders task JOIN conversation_archive_folders dialog
 ON task.user_id=dialog.user_id AND task.normalized_name=dialog.normalized_name
 WHERE task.is_task_conv=TRUE AND dialog.is_task_conv=FALSE;
UPDATE conversations SET archive_folder_id=(SELECT target_id FROM archive_folder_kind_map WHERE id=conversations.archive_folder_id)
 WHERE archive_folder_id IN (SELECT id FROM archive_folder_kind_map);
CREATE TABLE conversation_archive_folders_shared (
 id VARCHAR(36) PRIMARY KEY, user_id VARCHAR(255) NOT NULL,
 name VARCHAR(255) NOT NULL, normalized_name VARCHAR(255) NOT NULL,
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
 CONSTRAINT uk_conversation_archive_folders_user_name UNIQUE(user_id,normalized_name)
);
INSERT INTO conversation_archive_folders_shared SELECT id,user_id,name,normalized_name,created_at,updated_at FROM conversation_archive_folders
 WHERE id NOT IN (SELECT id FROM archive_folder_kind_map);
DROP TABLE conversation_archive_folders;
ALTER TABLE conversation_archive_folders_shared RENAME TO conversation_archive_folders;
ALTER TABLE conversation_archive_folders RENAME CONSTRAINT conversation_archive_folders_shared_pkey TO conversation_archive_folders_pkey;
DROP TABLE archive_folder_kind_map;

-- +migrate Dialect sqlite
-- Returning to shared folders merges same-name destinations and preserves every conversation.
CREATE TABLE archive_folder_kind_map (id VARCHAR(36) PRIMARY KEY, target_id VARCHAR(36) NOT NULL);
INSERT INTO archive_folder_kind_map
 SELECT task.id,dialog.id FROM conversation_archive_folders task JOIN conversation_archive_folders dialog
 ON task.user_id=dialog.user_id AND task.normalized_name=dialog.normalized_name
 WHERE task.is_task_conv=TRUE AND dialog.is_task_conv=FALSE;
UPDATE conversations SET archive_folder_id=(SELECT target_id FROM archive_folder_kind_map WHERE id=conversations.archive_folder_id)
 WHERE archive_folder_id IN (SELECT id FROM archive_folder_kind_map);
CREATE TABLE conversation_archive_folders_shared (
 id VARCHAR(36) PRIMARY KEY, user_id VARCHAR(255) NOT NULL,
 name VARCHAR(255) NOT NULL, normalized_name VARCHAR(255) NOT NULL,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT uk_conversation_archive_folders_user_name UNIQUE(user_id,normalized_name)
);
INSERT INTO conversation_archive_folders_shared SELECT id,user_id,name,normalized_name,created_at,updated_at FROM conversation_archive_folders
 WHERE id NOT IN (SELECT id FROM archive_folder_kind_map);
DROP TABLE conversation_archive_folders;
ALTER TABLE conversation_archive_folders_shared RENAME TO conversation_archive_folders;
DROP TABLE archive_folder_kind_map;
