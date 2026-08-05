-- +migrate Dialect postgres
ALTER TABLE public.skill_market_items
    DROP COLUMN IF EXISTS tags;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
