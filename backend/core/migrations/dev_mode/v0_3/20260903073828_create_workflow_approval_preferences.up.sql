-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.workflow_approval_preferences (
    user_id VARCHAR(255) NOT NULL,
    workflow_id VARCHAR(64) NOT NULL,
    step_id VARCHAR(64) NOT NULL,
    approval_required BOOLEAN NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (user_id, workflow_id, step_id)
);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS workflow_approval_preferences (
    user_id VARCHAR(255) NOT NULL,
    workflow_id VARCHAR(64) NOT NULL,
    step_id VARCHAR(64) NOT NULL,
    approval_required BOOLEAN NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (user_id, workflow_id, step_id)
);
