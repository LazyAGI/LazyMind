package dbbootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	BaselineVersion = "20260519101723_init"
	markerTable     = "scan_control_plane_schema_migrations"
	migrationFile   = BaselineVersion + ".up.sql"

	advisoryLockID int64 = 6034740619178369104
)

type State string

const (
	StateEmpty   State = "empty"
	StateNew     State = "new"
	StateLegacy  State = "legacy"
	StateUnknown State = "unknown"
)

type Options struct {
	MigrationFile string
}

type Result struct {
	State            State
	AppliedMigration bool
	ResetLegacy      bool
	RecordedMarker   bool
}

type schemaSnapshot struct {
	Tables         map[string]bool
	Columns        map[string]map[string]bool
	BaselineMarked bool
}

var currentTables = []string{
	"parse_task_dead_letters",
	"agent_commands",
	"agents",
	"data_source_create_operations",
	"source_sync_runs",
	"source_sync_checkpoints",
	"parse_tasks",
	"documents",
	"source_document_states",
	"source_object_index",
	"source_bindings",
	"sources",
}

var legacyOnlyTables = []string{
	"cloud_source_bindings",
	"cloud_object_index",
	"cloud_sync_checkpoints",
	"cloud_sync_runs",
	"manual_pull_jobs",
	"reconcile_snapshots",
	"source_baseline_snapshots",
	"source_file_snapshots",
	"source_file_snapshot_items",
	"source_snapshot_relations",
}

var resetTables = append(append([]string{}, currentTables...), legacyOnlyTables...)

func Bootstrap(ctx context.Context, db *sql.DB, options Options) (Result, error) {
	if db == nil {
		return Result{State: StateUnknown}, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin scan-control-plane db bootstrap: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", advisoryLockID); err != nil {
		return Result{}, fmt.Errorf("lock scan-control-plane db bootstrap: %w", err)
	}

	snapshot, err := loadSchemaSnapshot(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	state := classify(snapshot)
	result := Result{State: state}

	switch state {
	case StateNew:
		recorded, err := ensureBaselineMarker(ctx, tx)
		if err != nil {
			return Result{}, err
		}
		result.RecordedMarker = recorded
	case StateEmpty:
		if err := applyBaselineMigration(ctx, tx, options.MigrationFile); err != nil {
			return Result{}, err
		}
		if _, err := ensureBaselineMarker(ctx, tx); err != nil {
			return Result{}, err
		}
		result.AppliedMigration = true
		result.RecordedMarker = true
	case StateLegacy:
		if err := dropResetTables(ctx, tx); err != nil {
			return Result{}, err
		}
		if err := applyBaselineMigration(ctx, tx, options.MigrationFile); err != nil {
			return Result{}, err
		}
		if _, err := ensureBaselineMarker(ctx, tx); err != nil {
			return Result{}, err
		}
		result.ResetLegacy = true
		result.AppliedMigration = true
		result.RecordedMarker = true
	case StateUnknown:
		return Result{}, fmt.Errorf("unknown scan-control-plane database schema detected; refusing to reset automatically")
	default:
		return Result{}, fmt.Errorf("unsupported scan-control-plane database state %q", state)
	}

	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit scan-control-plane db bootstrap: %w", err)
	}
	return result, nil
}

func loadSchemaSnapshot(ctx context.Context, tx *sql.Tx) (schemaSnapshot, error) {
	tables := map[string]bool{}
	tableRows, err := tx.QueryContext(ctx, `
SELECT table_name
  FROM information_schema.tables
 WHERE table_schema = 'public'
   AND table_type = 'BASE TABLE'`)
	if err != nil {
		return schemaSnapshot{}, fmt.Errorf("list scan-control-plane tables: %w", err)
	}
	defer tableRows.Close()
	for tableRows.Next() {
		var table string
		if err := tableRows.Scan(&table); err != nil {
			return schemaSnapshot{}, fmt.Errorf("scan scan-control-plane table: %w", err)
		}
		tables[table] = true
	}
	if err := tableRows.Err(); err != nil {
		return schemaSnapshot{}, fmt.Errorf("scan scan-control-plane tables: %w", err)
	}

	columns := map[string]map[string]bool{}
	columnRows, err := tx.QueryContext(ctx, `
SELECT table_name, column_name
  FROM information_schema.columns
 WHERE table_schema = 'public'`)
	if err != nil {
		return schemaSnapshot{}, fmt.Errorf("list scan-control-plane columns: %w", err)
	}
	defer columnRows.Close()
	for columnRows.Next() {
		var table, column string
		if err := columnRows.Scan(&table, &column); err != nil {
			return schemaSnapshot{}, fmt.Errorf("scan scan-control-plane column: %w", err)
		}
		if columns[table] == nil {
			columns[table] = map[string]bool{}
		}
		columns[table][column] = true
	}
	if err := columnRows.Err(); err != nil {
		return schemaSnapshot{}, fmt.Errorf("scan scan-control-plane columns: %w", err)
	}

	baselineMarked, err := hasBaselineMarker(ctx, tx, tables[markerTable])
	if err != nil {
		return schemaSnapshot{}, err
	}
	return schemaSnapshot{Tables: tables, Columns: columns, BaselineMarked: baselineMarked}, nil
}

func hasBaselineMarker(ctx context.Context, tx *sql.Tx, markerExists bool) (bool, error) {
	if !markerExists {
		return false, nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM public.%s WHERE version = $1", markerTable), BaselineVersion).Scan(&count); err != nil {
		return false, fmt.Errorf("read scan-control-plane migration marker: %w", err)
	}
	return count > 0, nil
}

func classify(snapshot schemaSnapshot) State {
	if nonMarkerTableCount(snapshot.Tables) == 0 {
		return StateEmpty
	}
	if snapshot.BaselineMarked && isLegacySchema(snapshot) {
		return StateUnknown
	}
	if isLegacySchema(snapshot) {
		return StateLegacy
	}
	if isCurrentSchema(snapshot) {
		return StateNew
	}
	return StateUnknown
}

func nonMarkerTableCount(tables map[string]bool) int {
	count := 0
	for table := range tables {
		if table != markerTable {
			count++
		}
	}
	return count
}

func isCurrentSchema(snapshot schemaSnapshot) bool {
	for _, table := range currentTables {
		if !snapshot.Tables[table] {
			return false
		}
	}
	return hasColumn(snapshot, "sources", "source_id") &&
		hasColumn(snapshot, "source_bindings", "binding_id") &&
		hasColumn(snapshot, "documents", "document_id") &&
		hasColumn(snapshot, "parse_tasks", "task_id")
}

func isLegacySchema(snapshot schemaSnapshot) bool {
	for _, table := range legacyOnlyTables {
		if snapshot.Tables[table] {
			return true
		}
	}
	return hasColumn(snapshot, "sources", "root_path") ||
		(hasColumn(snapshot, "sources", "id") && !hasColumn(snapshot, "sources", "source_id")) ||
		(hasColumn(snapshot, "documents", "id") && !hasColumn(snapshot, "documents", "document_id")) ||
		(hasColumn(snapshot, "parse_tasks", "id") && !hasColumn(snapshot, "parse_tasks", "task_id"))
}

func hasColumn(snapshot schemaSnapshot, table, column string) bool {
	return snapshot.Columns[table] != nil && snapshot.Columns[table][column]
}

func dropResetTables(ctx context.Context, tx *sql.Tx) error {
	for _, table := range resetTables {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS public.%s CASCADE", table)); err != nil {
			return fmt.Errorf("drop legacy scan-control-plane table %s: %w", table, err)
		}
	}
	return nil
}

func applyBaselineMigration(ctx context.Context, tx *sql.Tx, migrationPath string) error {
	if strings.TrimSpace(migrationPath) == "" {
		migrationPath = defaultMigrationFile()
	}
	body, err := os.ReadFile(migrationPath)
	if err != nil {
		return fmt.Errorf("read scan-control-plane baseline migration %s: %w", migrationPath, err)
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("apply scan-control-plane baseline migration %s: %w", migrationPath, err)
	}
	return nil
}

func ensureBaselineMarker(ctx context.Context, tx *sql.Tx) (bool, error) {
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS public.%s (
    version text PRIMARY KEY,
    description text NOT NULL,
    applied_at timestamp with time zone NOT NULL DEFAULT now()
)`, markerTable)); err != nil {
		return false, fmt.Errorf("ensure scan-control-plane migration marker table: %w", err)
	}
	result, err := tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO public.%s (version, description)
VALUES ($1, $2)
ON CONFLICT (version) DO NOTHING`, markerTable), BaselineVersion, "scan-control-plane rewrite baseline")
	if err != nil {
		return false, fmt.Errorf("record scan-control-plane migration marker: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return true, nil
	}
	return affected > 0, nil
}

func defaultMigrationFile() string {
	candidates := []string{
		filepath.Join("migrations", migrationFile),
		filepath.Join("/app", "migrations", migrationFile),
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "migrations", migrationFile),
			filepath.Join(exeDir, "..", "migrations", migrationFile),
		)
	}
	for _, candidate := range uniqueStrings(candidates) {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return candidates[0]
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}
