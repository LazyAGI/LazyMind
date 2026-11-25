ALTER TABLE plugin_sessions ADD COLUMN approval_preferences TEXT NOT NULL DEFAULT '{}';

-- +migrate Dialect postgres
UPDATE plugin_sessions SET approval_preferences = COALESCE((
    SELECT json_object_agg(step_id, approval_required)::text FROM workflow_approval_preferences
    WHERE user_id = plugin_sessions.create_user_id AND workflow_id = plugin_sessions.plugin_id
), '{}');

-- +migrate Dialect sqlite
UPDATE plugin_sessions SET approval_preferences = COALESCE((
    SELECT json_group_object(step_id, json(CASE WHEN approval_required THEN 'true' ELSE 'false' END)) FROM workflow_approval_preferences
    WHERE user_id = plugin_sessions.create_user_id AND workflow_id = plugin_sessions.plugin_id
), '{}');
