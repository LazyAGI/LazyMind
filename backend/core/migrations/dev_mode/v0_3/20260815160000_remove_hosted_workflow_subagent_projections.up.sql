-- Hosted Workflow attempts are owned by plugin_session_steps. Older builds also
-- created workflow_step SubAgent rows for the same attempt; remove only those
-- mechanically identifiable duplicate projections and their dependent rows.
-- +migrate Dialect postgres
DELETE FROM sub_agent_artifacts
WHERE task_id IN (
    SELECT task.id
    FROM sub_agent_tasks AS task
    JOIN plugin_session_steps AS attempt ON attempt.task_id = task.id
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE task.agent_type = 'workflow_step'
      AND session.controller_host = 'external-agent'
);

DELETE FROM sub_agent_steps
WHERE task_id IN (
    SELECT task.id
    FROM sub_agent_tasks AS task
    JOIN plugin_session_steps AS attempt ON attempt.task_id = task.id
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE task.agent_type = 'workflow_step'
      AND session.controller_host = 'external-agent'
);

DELETE FROM sub_agent_tasks
WHERE id IN (
    SELECT attempt.task_id
    FROM plugin_session_steps AS attempt
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE session.controller_host = 'external-agent'
);

-- +migrate Dialect sqlite
DELETE FROM sub_agent_artifacts
WHERE task_id IN (
    SELECT task.id
    FROM sub_agent_tasks AS task
    JOIN plugin_session_steps AS attempt ON attempt.task_id = task.id
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE task.agent_type = 'workflow_step'
      AND session.controller_host = 'external-agent'
);

DELETE FROM sub_agent_steps
WHERE task_id IN (
    SELECT task.id
    FROM sub_agent_tasks AS task
    JOIN plugin_session_steps AS attempt ON attempt.task_id = task.id
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE task.agent_type = 'workflow_step'
      AND session.controller_host = 'external-agent'
);

DELETE FROM sub_agent_tasks
WHERE id IN (
    SELECT attempt.task_id
    FROM plugin_session_steps AS attempt
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE session.controller_host = 'external-agent'
);
