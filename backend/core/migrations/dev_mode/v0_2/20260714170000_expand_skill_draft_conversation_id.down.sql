-- 20260714170000_expand_skill_draft_conversation_id
-- +migrate Down
-- +migrate Dialect postgres

ALTER TABLE public.personal_resource_drafts
    ALTER COLUMN conversation_id TYPE VARCHAR(36);

ALTER TABLE public.skill_drafts
    ALTER COLUMN conversation_id TYPE VARCHAR(36);

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
