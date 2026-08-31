-- 20260821120000_create_dataset_user_states
-- +migrate Down
-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.uk_dataset_user_states_user_dataset;
DROP TABLE IF EXISTS public.dataset_user_states;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS uk_dataset_user_states_user_dataset;
DROP TABLE IF EXISTS dataset_user_states;
