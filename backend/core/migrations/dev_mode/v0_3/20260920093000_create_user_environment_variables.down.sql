-- +migrate Dialect postgres,sqlite
DROP INDEX IF EXISTS idx_user_environment_variables_deleted_at;
DROP INDEX IF EXISTS idx_user_env_user_enabled;
DROP INDEX IF EXISTS idx_user_env_user_name_active;
DROP TABLE IF EXISTS user_environment_variables;
