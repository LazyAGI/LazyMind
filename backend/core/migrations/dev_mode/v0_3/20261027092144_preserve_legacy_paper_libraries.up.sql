-- +migrate Dialect postgres,sqlite
CREATE TABLE paper_library_legacy_states (
 dataset_id VARCHAR(255) NOT NULL PRIMARY KEY,
 extras_json JSON NOT NULL,
 migrated_at TIMESTAMP,
 updated_at TIMESTAMP NOT NULL
);

-- +migrate Dialect postgres
UPDATE datasets SET scene='academic_papers'
 WHERE scene='general' AND ext->>'library_kind'='paper';

-- +migrate Dialect sqlite
UPDATE datasets SET scene='academic_papers'
 WHERE scene='general' AND CASE WHEN json_valid(ext) THEN json_extract(ext,'$.library_kind') ELSE NULL END='paper';
