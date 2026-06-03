#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MIGRATION="${MIGRATION:-$ROOT_DIR/migrations/20260519101723_init.up.sql}"
DB_DSN="${SCAN_CONTROL_PLANE_DB_DSN:-${DATABASE_URL:-}}"

if [[ -z "$DB_DSN" ]]; then
  echo "SCAN_CONTROL_PLANE_DB_DSN or DATABASE_URL is required" >&2
  exit 2
fi

OWNED_TABLES=(
  parse_task_dead_letters
  agent_commands
  agents
  data_source_create_operations
  source_sync_runs
  source_sync_checkpoints
  parse_tasks
  documents
  source_document_states
  source_object_index
  source_bindings
  sources
)

echo "This reset will drop only scan-control-plane owned tables in public schema:"
for table in "${OWNED_TABLES[@]}"; do
  echo "  public.${table}"
done

if [[ "${SCAN_CONTROL_PLANE_RESET_CONFIRM:-}" != "drop-scan-control-plane-owned-tables" ]]; then
  echo "Set SCAN_CONTROL_PLANE_RESET_CONFIRM=drop-scan-control-plane-owned-tables to continue." >&2
  exit 3
fi

drop_sql="BEGIN;"
for table in "${OWNED_TABLES[@]}"; do
  drop_sql+=" DROP TABLE IF EXISTS public.${table};"
done
drop_sql+=" COMMIT;"

psql "$DB_DSN" -v ON_ERROR_STOP=1 -c "$drop_sql"
psql "$DB_DSN" -v ON_ERROR_STOP=1 -f "$MIGRATION"

echo "scan-control-plane reset completed; migration reapplied from $MIGRATION"
