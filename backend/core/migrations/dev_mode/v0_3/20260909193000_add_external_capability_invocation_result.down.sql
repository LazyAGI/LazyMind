-- 20260909193000_add_external_capability_invocation_result
-- +migrate Down
-- +migrate Dialect postgres

ALTER TABLE external_capability_invocations DROP COLUMN IF EXISTS result_json;

-- +migrate Dialect sqlite

ALTER TABLE external_capability_invocations DROP COLUMN result_json;
