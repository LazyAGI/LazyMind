-- +migrate Dialect postgres
ALTER TABLE public.skills ADD COLUMN IF NOT EXISTS call_mode VARCHAR(16) NOT NULL DEFAULT 'on_demand';
ALTER TABLE public.skills ADD COLUMN IF NOT EXISTS sort_rank BIGINT NOT NULL DEFAULT 0;
ALTER TABLE public.skills ADD COLUMN IF NOT EXISTS original_revision_id VARCHAR(36);
ALTER TABLE public.skills ADD COLUMN IF NOT EXISTS field TEXT NOT NULL DEFAULT '';
ALTER TABLE public.skills ADD COLUMN IF NOT EXISTS aliases JSON NOT NULL DEFAULT '[]';
ALTER TABLE public.skills ADD COLUMN IF NOT EXISTS keywords JSON NOT NULL DEFAULT '[]';
UPDATE public.skills SET call_mode = CASE WHEN call_mode = 'disabled' OR NOT is_enabled THEN 'manual' WHEN call_mode IS NULL OR call_mode = '' THEN 'on_demand' ELSE call_mode END;
UPDATE public.skills SET sort_rank = FLOOR(EXTRACT(EPOCH FROM created_at) * 1000) WHERE sort_rank = 0;
UPDATE public.skills SET original_revision_id = (
    SELECT r.id FROM public.skill_revisions r WHERE r.skill_id = skills.id
    ORDER BY r.revision_no ASC, r.created_at ASC, r.id ASC LIMIT 1
) WHERE original_revision_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_skills_owner_call_mode_sort ON public.skills(owner_user_id, call_mode, sort_rank DESC, created_at DESC);

-- +migrate Dialect sqlite
ALTER TABLE skills ADD COLUMN call_mode VARCHAR(16) NOT NULL DEFAULT 'on_demand';
ALTER TABLE skills ADD COLUMN sort_rank BIGINT NOT NULL DEFAULT 0;
ALTER TABLE skills ADD COLUMN original_revision_id VARCHAR(36);
ALTER TABLE skills ADD COLUMN field TEXT NOT NULL DEFAULT '';
ALTER TABLE skills ADD COLUMN aliases JSON NOT NULL DEFAULT '[]';
ALTER TABLE skills ADD COLUMN keywords JSON NOT NULL DEFAULT '[]';
UPDATE skills SET call_mode = CASE WHEN call_mode = 'disabled' OR NOT is_enabled THEN 'manual' WHEN call_mode IS NULL OR call_mode = '' THEN 'on_demand' ELSE call_mode END;
UPDATE skills SET sort_rank = CAST(strftime('%s', created_at) AS INTEGER) * 1000 WHERE sort_rank = 0;
UPDATE skills SET original_revision_id = (
    SELECT r.id FROM skill_revisions r WHERE r.skill_id = skills.id
    ORDER BY r.revision_no ASC, r.created_at ASC, r.id ASC LIMIT 1
) WHERE original_revision_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_skills_owner_call_mode_sort ON skills(owner_user_id, call_mode, sort_rank DESC, created_at DESC);
