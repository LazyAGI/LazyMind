-- +migrate Dialect postgres
CREATE TABLE archive_folder_kind_map (id VARCHAR(36) PRIMARY KEY, task_id VARCHAR(36) NOT NULL);
INSERT INTO archive_folder_kind_map SELECT f.id, md5(random()::text || f.id)::uuid::text FROM conversation_archive_folders f
WHERE EXISTS (SELECT 1 FROM conversations c WHERE c.archive_folder_id=f.id AND c.create_user_id=f.user_id AND c.is_task_conv=TRUE);
CREATE TABLE conversation_archive_folders_typed (
 id VARCHAR(36) PRIMARY KEY, user_id VARCHAR(255) NOT NULL,
 name VARCHAR(255) NOT NULL, normalized_name VARCHAR(255) NOT NULL,
 is_task_conv BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
 CONSTRAINT uk_conversation_archive_folders_user_kind_name UNIQUE(user_id,is_task_conv,normalized_name)
);
INSERT INTO conversation_archive_folders_typed SELECT id,user_id,name,normalized_name,FALSE,created_at,updated_at FROM conversation_archive_folders;
INSERT INTO conversation_archive_folders_typed
 SELECT m.task_id,f.user_id,f.name,f.normalized_name,TRUE,f.created_at,f.updated_at
 FROM conversation_archive_folders f JOIN archive_folder_kind_map m ON m.id=f.id;
UPDATE conversations SET archive_folder_id=(SELECT task_id FROM archive_folder_kind_map WHERE id=conversations.archive_folder_id)
 WHERE is_task_conv=TRUE AND archive_folder_id IN (SELECT id FROM archive_folder_kind_map);
DROP TABLE conversation_archive_folders;
ALTER TABLE conversation_archive_folders_typed RENAME TO conversation_archive_folders;
ALTER TABLE conversation_archive_folders RENAME CONSTRAINT conversation_archive_folders_typed_pkey TO conversation_archive_folders_pkey;
DROP TABLE archive_folder_kind_map;

-- +migrate Dialect sqlite
CREATE TABLE archive_folder_kind_map (id VARCHAR(36) PRIMARY KEY, task_id VARCHAR(36) NOT NULL);
INSERT INTO archive_folder_kind_map SELECT f.id, lower(hex(randomblob(16))) FROM conversation_archive_folders f
WHERE EXISTS (SELECT 1 FROM conversations c WHERE c.archive_folder_id=f.id AND c.create_user_id=f.user_id AND c.is_task_conv=TRUE);
CREATE TABLE conversation_archive_folders_typed (
 id VARCHAR(36) PRIMARY KEY, user_id VARCHAR(255) NOT NULL,
 name VARCHAR(255) NOT NULL, normalized_name VARCHAR(255) NOT NULL,
 is_task_conv BOOLEAN NOT NULL DEFAULT FALSE,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT uk_conversation_archive_folders_user_kind_name UNIQUE(user_id,is_task_conv,normalized_name)
);
INSERT INTO conversation_archive_folders_typed SELECT id,user_id,name,normalized_name,FALSE,created_at,updated_at FROM conversation_archive_folders;
INSERT INTO conversation_archive_folders_typed
 SELECT m.task_id,f.user_id,f.name,f.normalized_name,TRUE,f.created_at,f.updated_at
 FROM conversation_archive_folders f JOIN archive_folder_kind_map m ON m.id=f.id;
UPDATE conversations SET archive_folder_id=(SELECT task_id FROM archive_folder_kind_map WHERE id=conversations.archive_folder_id)
 WHERE is_task_conv=TRUE AND archive_folder_id IN (SELECT id FROM archive_folder_kind_map);
DROP TABLE conversation_archive_folders;
ALTER TABLE conversation_archive_folders_typed RENAME TO conversation_archive_folders;
DROP TABLE archive_folder_kind_map;
