-- +migrate Dialect postgres
DROP TABLE IF EXISTS public.skill_draft_review_action_items;
DROP TABLE IF EXISTS public.skill_draft_review_action_batches;
DROP TABLE IF EXISTS public.skill_draft_review_sessions;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
