-- +migrate Dialect postgres,sqlite
CREATE TABLE paper_library_folders (
 dataset_id VARCHAR(255) NOT NULL, id VARCHAR(255) NOT NULL, parent_id VARCHAR(255) NOT NULL DEFAULT '',
 name VARCHAR(255) NOT NULL, name_key VARCHAR(255) NOT NULL, revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
 PRIMARY KEY(dataset_id,id), UNIQUE(dataset_id,parent_id,name_key)
);
CREATE INDEX idx_library_folder_parent ON paper_library_folders(dataset_id,parent_id);
CREATE TABLE paper_library_entries (
 dataset_id VARCHAR(255) NOT NULL, id VARCHAR(255) NOT NULL, academic_work_id VARCHAR(36) NOT NULL,
 folder_id VARCHAR(255) NOT NULL DEFAULT '', status VARCHAR(16) NOT NULL DEFAULT 'unread',
 tags_json JSON NOT NULL, metadata_json JSON NOT NULL, source VARCHAR(32) NOT NULL DEFAULT 'unknown',
 import_method VARCHAR(32) NOT NULL DEFAULT '', source_url TEXT NOT NULL DEFAULT '', arxiv_version VARCHAR(64) NOT NULL DEFAULT '',
 revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0), removed_at TIMESTAMP,
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
 PRIMARY KEY(dataset_id,id), UNIQUE(dataset_id,academic_work_id),
 FOREIGN KEY(academic_work_id) REFERENCES academic_works(id)
);
CREATE INDEX idx_library_entry_folder ON paper_library_entries(dataset_id,folder_id);
CREATE INDEX idx_library_entry_active ON paper_library_entries(dataset_id,removed_at);
CREATE TABLE paper_library_notes (
 dataset_id VARCHAR(255) NOT NULL, id VARCHAR(255) NOT NULL, paper_id VARCHAR(255) NOT NULL,
 title TEXT NOT NULL, content TEXT NOT NULL, kind VARCHAR(32) NOT NULL DEFAULT 'note',
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
 PRIMARY KEY(dataset_id,id), FOREIGN KEY(dataset_id,paper_id) REFERENCES paper_library_entries(dataset_id,id)
);
CREATE INDEX idx_library_note_paper ON paper_library_notes(dataset_id,paper_id);
CREATE TABLE paper_library_attachments (
 dataset_id VARCHAR(255) NOT NULL, id VARCHAR(255) NOT NULL, paper_id VARCHAR(255) NOT NULL,
 document_id VARCHAR(128) NOT NULL DEFAULT '', import_item_id VARCHAR(36) NOT NULL DEFAULT '',
 name TEXT NOT NULL, kind VARCHAR(32) NOT NULL DEFAULT 'pdf', source_url TEXT NOT NULL DEFAULT '',
 is_primary BOOLEAN NOT NULL DEFAULT FALSE, detached_at TIMESTAMP,
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
 PRIMARY KEY(dataset_id,id), FOREIGN KEY(dataset_id,paper_id) REFERENCES paper_library_entries(dataset_id,id)
);
CREATE INDEX idx_library_attachment_paper ON paper_library_attachments(dataset_id,paper_id);
CREATE INDEX idx_library_attachment_document ON paper_library_attachments(dataset_id,document_id);
CREATE UNIQUE INDEX uk_library_active_document ON paper_library_attachments(dataset_id,document_id) WHERE document_id<>'' AND detached_at IS NULL;

-- Retain the original graph as an immutable archive. Core converts it
-- transactionally on first access and commits a conversion marker with its rows.

-- +migrate Dialect postgres
ALTER TABLE academic_expansions ALTER COLUMN discovery_mode SET DEFAULT 'external';

-- +migrate Dialect sqlite
ALTER TABLE academic_expansions RENAME TO academic_expansions_previous;
CREATE TABLE academic_expansions (
 discovery_mode VARCHAR(32) NOT NULL DEFAULT 'external',
 seed_snapshot_json JSON NOT NULL DEFAULT '[]',
 discovery_json JSON NOT NULL DEFAULT '[]',
 progress_json JSON NOT NULL DEFAULT '{}',
  id varchar(36) NOT NULL,
  user_id varchar(255) NOT NULL,
  dataset_id varchar(255) NOT NULL,
  conversation_id varchar(36) NOT NULL,
  request_key varchar(128) NOT NULL,
  request_owner varchar(255) NOT NULL,
  title text NOT NULL,
  goal varchar(16) NOT NULL,
  status varchar(32) NOT NULL,
  version BIGINT NOT NULL DEFAULT 0,
  result_json json NOT NULL,
  auto_review_id varchar(36) NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  deleted_at TIMESTAMP,
  PRIMARY KEY (id)
);
INSERT INTO academic_expansions (discovery_mode,seed_snapshot_json,discovery_json,progress_json,id,user_id,dataset_id,conversation_id,request_key,request_owner,title,goal,status,version,result_json,auto_review_id,created_at,updated_at,deleted_at) SELECT discovery_mode,seed_snapshot_json,discovery_json,progress_json,id,user_id,dataset_id,conversation_id,request_key,request_owner,title,goal,status,version,result_json,auto_review_id,created_at,updated_at,deleted_at FROM academic_expansions_previous;
DROP TABLE academic_expansions_previous;
CREATE INDEX idx_academic_expansions_user_id ON academic_expansions(user_id);
CREATE INDEX idx_academic_expansions_dataset_id ON academic_expansions(dataset_id);
CREATE UNIQUE INDEX idx_academic_expansions_conversation_id ON academic_expansions(conversation_id);
CREATE UNIQUE INDEX uk_academic_expansion_request ON academic_expansions(request_key, request_owner);
CREATE INDEX idx_academic_expansions_deleted_at ON academic_expansions(deleted_at);

-- +migrate Dialect postgres
ALTER TABLE academic_expansion_rounds ALTER COLUMN discovery_mode SET DEFAULT 'external';

-- +migrate Dialect sqlite
ALTER TABLE academic_expansion_rounds RENAME TO academic_expansion_rounds_previous;
CREATE TABLE academic_expansion_rounds (
 discovery_mode VARCHAR(32) NOT NULL DEFAULT 'external',
  id varchar(36) NOT NULL,
  expansion_id varchar(36) NOT NULL,
  request_key varchar(128) NOT NULL,
  status varchar(32) NOT NULL,
  version BIGINT NOT NULL DEFAULT 0,
  input_json json NOT NULL,
  result_json json NOT NULL,
  session_id varchar(36) NOT NULL DEFAULT '',
  attempt_id varchar(36) NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (id)
);
INSERT INTO academic_expansion_rounds (discovery_mode,id,expansion_id,request_key,status,version,input_json,result_json,session_id,attempt_id,created_at,updated_at) SELECT discovery_mode,id,expansion_id,request_key,status,version,input_json,result_json,session_id,attempt_id,created_at,updated_at FROM academic_expansion_rounds_previous;
DROP TABLE academic_expansion_rounds_previous;
CREATE UNIQUE INDEX uk_academic_round ON academic_expansion_rounds(expansion_id, request_key);
