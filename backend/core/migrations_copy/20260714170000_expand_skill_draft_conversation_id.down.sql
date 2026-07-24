-- 20260714170000_expand_skill_draft_conversation_id
-- +migrate Down

ALTER TABLE public.personal_resource_drafts
    ALTER COLUMN conversation_id TYPE VARCHAR(36);

ALTER TABLE public.skill_drafts
    ALTER COLUMN conversation_id TYPE VARCHAR(36);
