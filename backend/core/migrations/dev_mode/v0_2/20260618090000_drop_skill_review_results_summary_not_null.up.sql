-- +migrate Dialect postgres
ALTER TABLE public.skill_review_results
ALTER COLUMN userid DROP DEFAULT,
ALTER COLUMN requestid DROP DEFAULT,
ALTER COLUMN summary DROP DEFAULT,
ALTER COLUMN summary DROP NOT NULL;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
