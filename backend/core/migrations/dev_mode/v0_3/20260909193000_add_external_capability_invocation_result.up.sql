-- 20260909193000_add_external_capability_invocation_result
-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE external_capability_invocations
    ADD COLUMN IF NOT EXISTS result_json JSON NOT NULL DEFAULT '{}';

-- +migrate Dialect sqlite

ALTER TABLE external_capability_invocations
    ADD COLUMN result_json JSON NOT NULL DEFAULT '{}';
