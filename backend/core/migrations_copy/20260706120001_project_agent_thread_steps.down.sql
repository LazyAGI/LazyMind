-- 20260706120000_project_agent_thread_steps
-- +migrate Down

DROP INDEX IF EXISTS public.idx_agent_thread_steps_stage;

ALTER TABLE public.agent_thread_steps
    ADD COLUMN IF NOT EXISTS next_step_run_id character varying(128) DEFAULT ''::character varying NOT NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'agent_thread_steps'
          AND column_name = 'next_step_id'
    ) THEN
        UPDATE public.agent_thread_steps
        SET next_step_run_id = next_step_id
        WHERE next_step_run_id = ''
          AND next_step_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
    END IF;
END $$;

ALTER TABLE public.agent_thread_steps
    DROP COLUMN IF EXISTS next_step_id;

ALTER TABLE public.agent_thread_steps
    DROP COLUMN IF EXISTS stage;
