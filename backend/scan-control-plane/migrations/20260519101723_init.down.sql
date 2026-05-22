-- 20260519101723_init
-- +migrate Down

DROP TABLE IF EXISTS sources CASCADE;
DROP TABLE IF EXISTS source_snapshot_relations CASCADE;
DROP TABLE IF EXISTS source_file_snapshots CASCADE;
DROP TABLE IF EXISTS source_file_snapshot_items CASCADE;
DROP TABLE IF EXISTS source_document_states CASCADE;
DROP TABLE IF EXISTS source_baseline_snapshots CASCADE;
DROP TABLE IF EXISTS reconcile_snapshots CASCADE;
DROP TABLE IF EXISTS parse_tasks CASCADE;
DROP TABLE IF EXISTS parse_task_dead_letters CASCADE;
DROP TABLE IF EXISTS manual_pull_jobs CASCADE;
DROP TABLE IF EXISTS documents CASCADE;
DROP TABLE IF EXISTS cloud_sync_runs CASCADE;
DROP TABLE IF EXISTS cloud_sync_checkpoints CASCADE;
DROP TABLE IF EXISTS cloud_source_bindings CASCADE;
DROP TABLE IF EXISTS cloud_object_index CASCADE;
DROP TABLE IF EXISTS agents CASCADE;
DROP TABLE IF EXISTS agent_commands CASCADE;
