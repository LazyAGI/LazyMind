-- Roll back SQLite tables repaired by the matching up migration.
-- +migrate Dialect postgres
SELECT 1;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_plugin_repair_runs_draft;
DROP INDEX IF EXISTS idx_plugin_generation_analyses_draft;
DROP INDEX IF EXISTS idx_skill_market_installs_skill;
DROP INDEX IF EXISTS idx_skill_market_installs_user;

DROP TABLE IF EXISTS plugin_repair_runs;
DROP TABLE IF EXISTS plugin_generation_analyses;
DROP TABLE IF EXISTS skill_market_installs;
