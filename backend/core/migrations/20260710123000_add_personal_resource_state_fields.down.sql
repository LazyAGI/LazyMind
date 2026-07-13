ALTER TABLE public.personal_resources
    DROP COLUMN IF EXISTS updated_by_name,
    DROP COLUMN IF EXISTS updated_by,
    DROP COLUMN IF EXISTS ext,
    DROP COLUMN IF EXISTS auto_evo_error,
    DROP COLUMN IF EXISTS auto_evo_finished_at,
    DROP COLUMN IF EXISTS auto_evo_started_at,
    DROP COLUMN IF EXISTS auto_evo_generation,
    DROP COLUMN IF EXISTS auto_evo_apply_status,
    DROP COLUMN IF EXISTS auto_evo;
