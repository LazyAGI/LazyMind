#!/usr/bin/env bash
set -euo pipefail

IMAGE="${POSTGRES_IMAGE:-postgres:16}"
CONTAINER="${POSTGRES_CONTAINER:-scan-control-plane-pg-smoke}"
DB="${POSTGRES_DB:-scan_smoke}"
USER="${POSTGRES_USER:-postgres}"
PASSWORD="${POSTGRES_PASSWORD:-postgres}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MIGRATION="$ROOT_DIR/migrations/20260519101723_init.up.sql"

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cleanup
docker run --name "$CONTAINER" \
  -e POSTGRES_PASSWORD="$PASSWORD" \
  -e POSTGRES_DB="$DB" \
  -d "$IMAGE" >/dev/null

for _ in $(seq 1 40); do
  if docker exec "$CONTAINER" pg_isready -U "$USER" -d "$DB" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

docker exec -i "$CONTAINER" psql -v ON_ERROR_STOP=1 -U "$USER" -d "$DB" < "$MIGRATION" >/dev/null

docker exec -i "$CONTAINER" psql -v ON_ERROR_STOP=1 -U "$USER" -d "$DB" <<'SQL'
DO $$
DECLARE
  expected_tables text[] := ARRAY[
    'sources',
    'source_bindings',
    'source_object_index',
    'source_document_states',
    'documents',
    'parse_tasks',
    'parse_task_dead_letters',
    'source_sync_checkpoints',
    'source_sync_runs',
    'data_source_create_operations',
    'agents',
    'agent_commands'
  ];
  expected_table text;
  table_count int;
BEGIN
  FOREACH expected_table IN ARRAY expected_tables LOOP
    SELECT COUNT(*)
      INTO table_count
      FROM information_schema.tables
     WHERE table_schema = 'public'
       AND table_name = expected_table;
    IF table_count <> 1 THEN
      RAISE EXCEPTION 'missing table %', expected_table;
    END IF;
  END LOOP;

  FOREACH expected_table IN ARRAY ARRAY[
    'cloud_source_bindings',
    'cloud_object_index',
    'cloud_sync_checkpoints',
    'cloud_sync_runs'
  ] LOOP
    SELECT COUNT(*)
      INTO table_count
      FROM information_schema.tables
     WHERE table_schema = 'public'
       AND table_name = expected_table;
    IF table_count <> 0 THEN
      RAISE EXCEPTION 'legacy table still exists %', expected_table;
    END IF;
  END LOOP;
END $$;

DO $$
DECLARE
  forbidden_columns text[] := ARRAY['root_path', 'agent_id', 'provider', 'target_ref'];
  forbidden_column text;
  column_count int;
BEGIN
  FOREACH forbidden_column IN ARRAY forbidden_columns LOOP
    SELECT COUNT(*)
      INTO column_count
      FROM information_schema.columns
     WHERE table_schema = 'public'
       AND table_name = 'sources'
       AND column_name = forbidden_column;
    IF column_count <> 0 THEN
      RAISE EXCEPTION 'sources contains forbidden column %', forbidden_column;
    END IF;
  END LOOP;
END $$;

DO $$
DECLARE
  expected_indexes text[] := ARRAY[
    'idx_sources_user_updated',
    'idx_sources_name',
    'idx_source_bindings_source',
    'uk_source_binding_current_target',
    'idx_source_object_children',
    'idx_source_object_search',
    'idx_source_document_states_binding_state',
    'uk_documents_object',
    'idx_documents_binding',
    'idx_parse_tasks_due',
    'uk_parse_task_idempotency',
    'uk_parse_task_active',
    'idx_source_sync_due',
    'idx_source_sync_runs_binding_started',
    'uk_source_sync_runs_scheduled_fire',
    'uk_create_operation',
    'idx_agents_tenant_status',
    'idx_agent_commands_pending',
    'idx_parse_task_dead_letters_task',
    'idx_parse_task_dead_letters_failed_at'
  ];
  expected_index text;
  index_count int;
BEGIN
  FOREACH expected_index IN ARRAY expected_indexes LOOP
    SELECT COUNT(*)
      INTO index_count
      FROM pg_indexes
     WHERE schemaname = 'public'
       AND indexname = expected_index;
    IF index_count <> 1 THEN
      RAISE EXCEPTION 'missing index %', expected_index;
    END IF;
  END LOOP;
END $$;

INSERT INTO sources (
  source_id, created_by, name, dataset_id, status, created_at, updated_at
) VALUES (
  'source-1', 'user-1', 'Docs', 'dataset-1', 'ACTIVE', now(), now()
);

INSERT INTO source_bindings (
  binding_id, source_id, binding_type, connector_type, target_type, target_ref,
  target_fingerprint, tree_key, binding_generation, core_parent_document_id,
  core_parent_document_name, sync_mode, status, created_at, updated_at
) VALUES (
  'binding-1', 'source-1', 'connector_target', 'local_fs', 'local_path', '/workspace/docs',
  'local_fs:agent-1:path:/workspace/docs', 'binding-1', 1, 'core-folder-1', 'Docs', 'manual',
  'ACTIVE', now(), now()
);

DO $$
BEGIN
  INSERT INTO source_bindings (
    binding_id, source_id, binding_type, connector_type, target_type, target_ref,
    target_fingerprint, tree_key, binding_generation, core_parent_document_id,
    core_parent_document_name, sync_mode, status, created_at, updated_at
  ) VALUES (
    'binding-dup', 'source-1', 'connector_target', 'local_fs', 'local_path', '/workspace/docs',
    'local_fs:agent-1:path:/workspace/docs', 'binding-dup', 1, 'core-folder-dup', 'Docs', 'manual',
    'ACTIVE', now(), now()
  );
  RAISE EXCEPTION 'expected duplicate active binding target to fail';
EXCEPTION WHEN unique_violation THEN
  NULL;
END $$;

INSERT INTO source_object_index (
  source_id, binding_id, tree_key, object_key, display_name, search_name,
  object_type, is_document, is_container, has_children, depth, created_at, updated_at
) VALUES (
  'source-1', 'binding-1', 'binding-1', 'doc-1', 'Doc 1', 'doc 1',
  'file', true, false, false, 0, now(), now()
);

INSERT INTO source_document_states (
  source_id, binding_id, binding_generation, object_key, source_state, sync_state,
  document_list_visible, selectable, created_at, updated_at
) VALUES (
  'source-1', 'binding-1', 1, 'doc-1', 'NEW', 'PENDING',
  true, true, now(), now()
);

INSERT INTO documents (
  document_id, source_id, binding_id, object_key, display_name, parse_status,
  created_at, updated_at
) VALUES (
  'document-1', 'source-1', 'binding-1', 'doc-1', 'Doc 1', 'PENDING',
  now(), now()
);

DO $$
BEGIN
  INSERT INTO documents (
    document_id, source_id, binding_id, object_key, display_name, parse_status,
    created_at, updated_at
  ) VALUES (
    'document-dup', 'source-1', 'binding-1', 'doc-1', 'Doc 1', 'PENDING',
    now(), now()
  );
  RAISE EXCEPTION 'expected duplicate document object to fail';
EXCEPTION WHEN unique_violation THEN
  NULL;
END $$;

INSERT INTO parse_tasks (
  task_id, source_id, binding_id, binding_generation, object_key, document_id,
  task_action, target_version_id, source_version, core_parent_document_id,
  idempotency_key, status, next_run_at, created_at, updated_at
) VALUES (
  'task-1', 'source-1', 'binding-1', 1, 'doc-1', 'document-1',
  'CREATE', 'target-v1', 'source-v1', 'core-folder-1',
  'idem-1', 'PENDING', now(), now(), now()
);

DO $$
BEGIN
  INSERT INTO parse_tasks (
    task_id, source_id, binding_id, binding_generation, object_key, document_id,
    task_action, target_version_id, source_version, core_parent_document_id,
    idempotency_key, status, next_run_at, created_at, updated_at
  ) VALUES (
    'task-dup-idem', 'source-1', 'binding-1', 1, 'doc-1', 'document-1',
    'CREATE', 'target-v2', 'source-v2', 'core-folder-1',
    'idem-1', 'PENDING', now(), now(), now()
  );
  RAISE EXCEPTION 'expected duplicate task idempotency key to fail';
EXCEPTION WHEN unique_violation THEN
  NULL;
END $$;

INSERT INTO parse_task_dead_letters (
  dead_letter_id, task_id, source_id, binding_id, binding_generation, object_key,
  document_id, task_action, target_version_id, retry_count, error_code, failed_at, created_at
) VALUES (
  'dead-letter-task-1', 'task-1', 'source-1', 'binding-1', 1, 'doc-1',
  'document-1', 'CREATE', 'target-v1', 3, 'CORE_SUBMIT_FAILED', now(), now()
);

INSERT INTO source_sync_checkpoints (
  source_id, binding_id, binding_generation, retry_count, created_at, updated_at
) VALUES (
  'source-1', 'binding-1', 1, 0, now(), now()
);

INSERT INTO source_sync_runs (
  run_id, source_id, binding_id, binding_generation, trigger_type, scope_type,
  coverage_json, status, started_at
) VALUES (
  'run-1', 'source-1', 'binding-1', 1, 'manual', 'full',
  '{"complete":true}'::jsonb, 'PENDING', now()
);
SQL

echo "Postgres migration smoke passed for $CONTAINER using $IMAGE"
