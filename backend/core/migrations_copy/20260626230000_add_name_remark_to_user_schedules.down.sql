ALTER TABLE user_schedules
    DROP COLUMN IF EXISTS name,
    DROP COLUMN IF EXISTS remark,
    DROP COLUMN IF EXISTS run_count;
