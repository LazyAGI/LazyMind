-- +migrate Down
-- +migrate Dialect postgres,sqlite
ALTER TABLE datasets ADD COLUMN scenario_key VARCHAR(64) NOT NULL DEFAULT 'general';
