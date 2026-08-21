-- plugin_step_intents belongs to the v0.2 schema. Rolling back this v0.3 repair
-- must preserve the table rather than reintroduce the schema drift.
-- +migrate Dialect postgres
SELECT 1;

-- +migrate Dialect sqlite
SELECT 1;
