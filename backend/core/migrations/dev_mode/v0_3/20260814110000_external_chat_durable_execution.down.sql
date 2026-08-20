-- +migrate Dialect postgres
DROP TABLE IF EXISTS external_chat_hosts;
DROP TABLE IF EXISTS external_chat_run_events;
DROP INDEX IF EXISTS idx_external_agent_runs_claim;
DROP INDEX IF EXISTS idx_external_agent_runs_actor_user_id;
DROP INDEX IF EXISTS idx_external_agent_runs_history_id;
ALTER TABLE external_agent_runs
    DROP COLUMN IF EXISTS completed_at,
    DROP COLUMN IF EXISTS next_event_sequence,
    DROP COLUMN IF EXISTS claim_count,
    DROP COLUMN IF EXISTS stop_requested,
    DROP COLUMN IF EXISTS last_heartbeat_at,
    DROP COLUMN IF EXISTS claimed_at,
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS host_id,
    DROP COLUMN IF EXISTS history_ext,
    DROP COLUMN IF EXISTS sequence,
    DROP COLUMN IF EXISTS query,
    DROP COLUMN IF EXISTS prompt;

-- +migrate Dialect sqlite
DROP TABLE IF EXISTS external_chat_hosts;
DROP TABLE IF EXISTS external_chat_run_events;
DROP INDEX IF EXISTS idx_external_agent_runs_claim;
DROP INDEX IF EXISTS idx_external_agent_runs_actor_user_id;
DROP INDEX IF EXISTS idx_external_agent_runs_history_id;
ALTER TABLE external_agent_runs DROP COLUMN completed_at;
ALTER TABLE external_agent_runs DROP COLUMN next_event_sequence;
ALTER TABLE external_agent_runs DROP COLUMN claim_count;
ALTER TABLE external_agent_runs DROP COLUMN stop_requested;
ALTER TABLE external_agent_runs DROP COLUMN last_heartbeat_at;
ALTER TABLE external_agent_runs DROP COLUMN claimed_at;
ALTER TABLE external_agent_runs DROP COLUMN lease_expires_at;
ALTER TABLE external_agent_runs DROP COLUMN lease_token;
ALTER TABLE external_agent_runs DROP COLUMN host_id;
ALTER TABLE external_agent_runs DROP COLUMN history_ext;
ALTER TABLE external_agent_runs DROP COLUMN sequence;
ALTER TABLE external_agent_runs DROP COLUMN query;
ALTER TABLE external_agent_runs DROP COLUMN prompt;
