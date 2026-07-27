-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.idx_skill_market_installs_skill;
DROP INDEX IF EXISTS public.idx_skill_market_installs_user;

DROP TABLE IF EXISTS public.skill_market_installs;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
