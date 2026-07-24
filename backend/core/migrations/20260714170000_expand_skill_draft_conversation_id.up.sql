-- 20260714170000_expand_skill_draft_conversation_id
-- +migrate Up

ALTER TABLE public.skill_drafts
    ALTER COLUMN conversation_id TYPE VARCHAR(128);

ALTER TABLE public.personal_resource_drafts
    ALTER COLUMN conversation_id TYPE VARCHAR(128);
