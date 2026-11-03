-- +migrate Dialect postgres,sqlite
CREATE TABLE paper_libraries (
    dataset_id VARCHAR(255) NOT NULL PRIMARY KEY,
    data_json JSON NOT NULL,
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
