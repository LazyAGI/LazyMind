-- +migrate Dialect postgres,sqlite
DROP TABLE paper_library_attachments;
DROP TABLE paper_library_notes;
DROP TABLE paper_library_entries;
DROP TABLE paper_library_folders;
CREATE TABLE IF NOT EXISTS paper_libraries (
    dataset_id VARCHAR(255) NOT NULL PRIMARY KEY,
    data_json JSON NOT NULL,
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

-- +migrate Dialect postgres
ALTER TABLE academic_expansions ALTER COLUMN discovery_mode SET DEFAULT 'legacy_mock';

-- +migrate Dialect sqlite
ALTER TABLE academic_expansions RENAME TO academic_expansions_previous;
CREATE TABLE academic_expansions (
 discovery_mode VARCHAR(32) NOT NULL DEFAULT 'legacy_mock',
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
ALTER TABLE academic_expansion_rounds ALTER COLUMN discovery_mode SET DEFAULT 'legacy_mock';

-- +migrate Dialect sqlite
ALTER TABLE academic_expansion_rounds RENAME TO academic_expansion_rounds_previous;
CREATE TABLE academic_expansion_rounds (
 discovery_mode VARCHAR(32) NOT NULL DEFAULT 'legacy_mock',
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
