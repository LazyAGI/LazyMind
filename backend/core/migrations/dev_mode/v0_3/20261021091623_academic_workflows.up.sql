-- +migrate Dialect *
ALTER TABLE datasets ADD COLUMN scenario_key VARCHAR(64) NOT NULL DEFAULT 'general';
CREATE TABLE academic_document_states (
 metadata_json JSON NOT NULL DEFAULT '{}',
  document_id varchar(128) NOT NULL,
  dataset_id varchar(255) NOT NULL,
  kind varchar(16) NOT NULL,
  status varchar(16) NOT NULL,
  manual BOOLEAN NOT NULL,
  revision BIGINT NOT NULL,
  paper_id varchar(36) NOT NULL DEFAULT '',
  fingerprint varchar(128) NOT NULL DEFAULT '',
  error text NOT NULL DEFAULT '',
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (document_id)
);
CREATE INDEX idx_academic_document_states_dataset_id ON academic_document_states(dataset_id);
CREATE TABLE academic_user_paper_states (
  updated_at TIMESTAMP NOT NULL,
  user_id varchar(255) NOT NULL,
  paper_id varchar(36) NOT NULL,
  is_important BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (user_id, paper_id)
);
CREATE TABLE academic_expansions (
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
CREATE INDEX idx_academic_expansions_user_id ON academic_expansions(user_id);
CREATE INDEX idx_academic_expansions_dataset_id ON academic_expansions(dataset_id);
CREATE UNIQUE INDEX idx_academic_expansions_conversation_id ON academic_expansions(conversation_id);
CREATE UNIQUE INDEX uk_academic_expansion_request ON academic_expansions(request_key, request_owner);
CREATE INDEX idx_academic_expansions_deleted_at ON academic_expansions(deleted_at);
CREATE TABLE academic_expansion_rounds (
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
CREATE UNIQUE INDEX uk_academic_round ON academic_expansion_rounds(expansion_id, request_key);
CREATE TABLE academic_handoffs (
 dataset_id VARCHAR(255) NOT NULL DEFAULT '',
 reflection_first BOOLEAN NOT NULL DEFAULT false,
 mock_result_json JSON NOT NULL DEFAULT '{}',
  id varchar(36) NOT NULL,
  user_id varchar(255) NOT NULL,
  request_key varchar(128) NOT NULL,
  expansion_id varchar(36) NOT NULL DEFAULT '',
  version BIGINT NOT NULL DEFAULT 0,
  kind varchar(32) NOT NULL,
  conversation_id varchar(36) NOT NULL,
  input_json json NOT NULL,
  contents_json json NOT NULL,
  created_at TIMESTAMP NOT NULL,
  PRIMARY KEY (id)
);
CREATE UNIQUE INDEX uk_academic_handoff ON academic_handoffs(user_id, request_key);
CREATE INDEX idx_academic_handoffs_expansion_id ON academic_handoffs(expansion_id);
CREATE TABLE academic_paper_contents (
 parser_version VARCHAR(64) NOT NULL DEFAULT 'pdf-reader-v1',
 source_hash VARCHAR(64) NOT NULL DEFAULT '',
 source_dataset_id VARCHAR(255) NOT NULL DEFAULT '',
 source_document_id VARCHAR(128) NOT NULL DEFAULT '',
  id varchar(36) NOT NULL,
  user_id varchar(255) NOT NULL,
  paper_id varchar(36) NOT NULL,
  status varchar(16) NOT NULL,
  content_hash varchar(64) NOT NULL DEFAULT '',
  pages_json json NOT NULL,
  error text NOT NULL DEFAULT '',
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (id)
);
CREATE UNIQUE INDEX uk_academic_content ON academic_paper_contents(user_id, paper_id, parser_version);
