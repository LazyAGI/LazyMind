-- +migrate Dialect *
ALTER TABLE academic_expansions DROP COLUMN preparation_json;
ALTER TABLE academic_expansions DROP COLUMN preparation_revision;
ALTER TABLE academic_expansions DROP COLUMN budget_mode;
ALTER TABLE academic_expansions DROP COLUMN reading_json;
ALTER TABLE academic_expansions DROP COLUMN report_json;
ALTER TABLE academic_expansion_rounds DROP COLUMN report_json;
ALTER TABLE academic_paper_contents DROP COLUMN quality_json;
ALTER TABLE task_notifications DROP COLUMN target_json;
ALTER TABLE task_notifications DROP COLUMN read_at;
UPDATE academic_expansions SET conversation_id='p' || SUBSTR(id,2) WHERE conversation_id IS NULL;

-- +migrate Dialect postgres
ALTER TABLE academic_expansions ALTER COLUMN conversation_id SET NOT NULL;
ALTER TABLE academic_expansions ALTER COLUMN conversation_id DROP DEFAULT;

-- +migrate Dialect sqlite
CREATE TABLE academic_expansions_previous (
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
INSERT INTO academic_expansions_previous (discovery_mode,seed_snapshot_json,discovery_json,progress_json,id,user_id,dataset_id,conversation_id,request_key,request_owner,title,goal,status,version,result_json,auto_review_id,created_at,updated_at,deleted_at) SELECT discovery_mode,seed_snapshot_json,discovery_json,progress_json,id,user_id,dataset_id,conversation_id,request_key,request_owner,title,goal,status,version,result_json,auto_review_id,created_at,updated_at,deleted_at FROM academic_expansions;
DROP TABLE academic_expansions;
ALTER TABLE academic_expansions_previous RENAME TO academic_expansions;
CREATE INDEX idx_academic_expansions_user_id ON academic_expansions(user_id);
CREATE INDEX idx_academic_expansions_dataset_id ON academic_expansions(dataset_id);
CREATE UNIQUE INDEX idx_academic_expansions_conversation_id ON academic_expansions(conversation_id);
CREATE UNIQUE INDEX uk_academic_expansion_request ON academic_expansions(request_key, request_owner);
CREATE INDEX idx_academic_expansions_deleted_at ON academic_expansions(deleted_at);
