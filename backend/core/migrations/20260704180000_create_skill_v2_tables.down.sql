DROP INDEX IF EXISTS public.idx_skill_share_items_source_skill;

ALTER TABLE public.skill_share_items
    DROP COLUMN IF EXISTS source_skill_id;

DROP TABLE IF EXISTS public.skill_market_items;
DROP TABLE IF EXISTS public.skill_search_indexes;
DROP TABLE IF EXISTS public.skill_draft_entries;
DROP TABLE IF EXISTS public.skill_drafts;
DROP TABLE IF EXISTS public.skill_revision_entries;
DROP TABLE IF EXISTS public.skill_revisions;
DROP TABLE IF EXISTS public.skill_blobs;
DROP TABLE IF EXISTS public.skills;
