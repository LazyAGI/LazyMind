-- +migrate Dialect *
-- Global importance has no library provenance. Discard those marks rather than
-- copying them into unrelated libraries.
DROP TABLE academic_user_paper_states;
CREATE TABLE academic_user_paper_states (
  updated_at TIMESTAMP NOT NULL,
  user_id varchar(255) NOT NULL,
  dataset_id varchar(255) NOT NULL,
  paper_id varchar(36) NOT NULL,
  is_important BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (user_id, dataset_id, paper_id)
);
