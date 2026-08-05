-- +migrate Dialect postgres
DROP TABLE IF EXISTS public.personal_resource_review_action_items;
DROP TABLE IF EXISTS public.personal_resource_review_action_batches;
DROP TABLE IF EXISTS public.personal_resource_review_sessions;
DROP TABLE IF EXISTS public.personal_resource_drafts;
DROP TABLE IF EXISTS public.personal_resource_revisions;
DROP TABLE IF EXISTS public.personal_resource_blobs;
DROP TABLE IF EXISTS public.personal_resources;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
