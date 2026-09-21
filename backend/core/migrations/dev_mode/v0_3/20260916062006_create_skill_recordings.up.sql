CREATE TABLE IF NOT EXISTS skill_recordings (
    evidence TEXT NOT NULL DEFAULT '{}',
    attempt INTEGER NOT NULL DEFAULT 0,
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL,
    conversation_id VARCHAR(128) NOT NULL,
    skill_id VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    frames TEXT NOT NULL DEFAULT '[]',
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_skill_recordings_owner_conversation ON skill_recordings(user_id, conversation_id);
CREATE INDEX IF NOT EXISTS idx_skill_recordings_skill ON skill_recordings(skill_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_recordings_active_user ON skill_recordings(user_id) WHERE status = 'generating';
