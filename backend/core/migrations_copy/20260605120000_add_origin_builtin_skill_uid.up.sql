ALTER TABLE public.skill_resources
    ADD COLUMN IF NOT EXISTS origin_builtin_skill_uid character varying(64) DEFAULT ''::character varying NOT NULL;

CREATE INDEX IF NOT EXISTS idx_skill_resources_origin_builtin_uid
    ON public.skill_resources USING btree (origin_builtin_skill_uid);

CREATE UNIQUE INDEX IF NOT EXISTS uk_skill_resources_owner_origin_builtin_uid
    ON public.skill_resources USING btree (owner_user_id, origin_builtin_skill_uid)
    WHERE origin_builtin_skill_uid <> '' AND node_type = 'parent';
