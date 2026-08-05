package source

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	stdlog "log"
	"os"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type SQLRepository struct {
	db  *sql.DB
	orm *gorm.DB
}

func NewSQLRepository(db *sql.DB) *SQLRepository {
	return NewSQLRepositoryWithDriver("postgres", db)
}

func NewSQLRepositoryWithDriver(driver string, db *sql.DB) *SQLRepository {
	if db == nil {
		return &SQLRepository{}
	}
	var dialector gorm.Dialector
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "sqlite":
		dialector = sqlite.Dialector{Conn: db}
	default:
		dialector = postgres.New(postgres.Config{Conn: db})
	}
	orm, err := gorm.Open(dialector, &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               newGORMLogger(os.Stdout),
	})
	if err != nil {
		return &SQLRepository{db: db}
	}
	return &SQLRepository{db: db, orm: orm}
}

func newGORMLogger(out io.Writer) gormlogger.Interface {
	return gormlogger.New(stdlog.New(out, "\r\n", stdlog.LstdFlags), gormlogger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  gormlogger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
		Colorful:                  true,
	})
}

func (r *SQLRepository) AutoMigrate() error {
	if r.orm == nil {
		return NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	if err := r.orm.AutoMigrate(
		&ormSource{},
		&ormBinding{},
		&ormSourceObject{},
		&ormDocumentState{},
		&ormDocument{},
		&ormParseTask{},
		&ormSyncCheckpoint{},
		&ormSyncRun{},
		&ormCreateOperation{},
		&ormAgent{},
		&ormAgentCommand{},
		&ormParseTaskDeadLetter{},
	); err != nil {
		return err
	}
	if r.orm.Dialector.Name() == "sqlite" {
		if err := r.repairSQLiteSchema(); err != nil {
			return err
		}
		return r.orm.Exec("CREATE INDEX IF NOT EXISTS idx_agent_commands_current_pending ON agent_commands (queue_generation, agent_id, status, next_retry_at, created_at)").Error
	}
	return nil
}

func (r *SQLRepository) repairSQLiteSchema() error {
	for _, repair := range []func() error{
		r.dedupeSQLiteBindingTargets,
		r.dedupeSQLiteParseTasks,
		r.dedupeSQLiteDocuments,
		r.dedupeSQLiteSyncRuns,
		r.dedupeSQLiteCreateOperations,
	} {
		if err := repair(); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS uk_source_binding_current_target ON source_bindings (source_id, connector_type, target_type, target_fingerprint) WHERE status <> 'DELETING'",
		"CREATE UNIQUE INDEX IF NOT EXISTS uk_documents_object ON documents (source_id, binding_id, object_key)",
		"CREATE UNIQUE INDEX IF NOT EXISTS uk_parse_task_idempotency ON parse_tasks (idempotency_key)",
		"CREATE UNIQUE INDEX IF NOT EXISTS uk_parse_task_active ON parse_tasks (source_id, binding_id, object_key, target_version_id, task_action) WHERE status IN ('PENDING', 'RUNNING', 'SUBMITTED')",
		"CREATE UNIQUE INDEX IF NOT EXISTS uk_source_sync_runs_scheduled_fire ON source_sync_runs (binding_id, binding_generation, scheduled_fire_at) WHERE trigger_type = 'scheduled' AND scheduled_fire_at IS NOT NULL AND status IN ('PENDING', 'RUNNING')",
		"CREATE UNIQUE INDEX IF NOT EXISTS uk_create_operation ON data_source_create_operations (caller_id, request_id)",
	} {
		if err := r.orm.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *SQLRepository) dedupeSQLiteBindingTargets() error {
	statement := `
UPDATE source_bindings
SET status = 'DELETING',
	deleted_at = COALESCE(deleted_at, CURRENT_TIMESTAMP),
	updated_at = CURRENT_TIMESTAMP
WHERE rowid IN (
	SELECT rowid
	FROM (
		SELECT rowid,
			ROW_NUMBER() OVER (
				PARTITION BY source_id, connector_type, target_type, target_fingerprint
				ORDER BY updated_at DESC, created_at DESC, binding_id DESC
			) AS rn
		FROM source_bindings
		WHERE status <> 'DELETING'
	)
	WHERE rn > 1
)`
	return r.orm.Exec(statement).Error
}

func (r *SQLRepository) dedupeSQLiteParseTasks() error {
	for _, statement := range []string{
		`
DELETE FROM parse_tasks
WHERE rowid IN (
	SELECT rowid
	FROM (
		SELECT rowid,
			ROW_NUMBER() OVER (
				PARTITION BY idempotency_key
				ORDER BY updated_at DESC, created_at DESC, task_id DESC
			) AS rn
		FROM parse_tasks
	)
	WHERE rn > 1
)`,
		`
DELETE FROM parse_tasks
WHERE rowid IN (
	SELECT rowid
	FROM (
		SELECT rowid,
			ROW_NUMBER() OVER (
				PARTITION BY source_id, binding_id, object_key, target_version_id, task_action
				ORDER BY updated_at DESC, created_at DESC, task_id DESC
			) AS rn
		FROM parse_tasks
		WHERE status IN ('PENDING', 'RUNNING', 'SUBMITTED')
	)
	WHERE rn > 1
)`,
	} {
		if err := r.orm.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *SQLRepository) dedupeSQLiteDocuments() error {
	for _, statement := range []string{
		`
UPDATE parse_tasks
SET document_id = (
	SELECT keeper.document_id
	FROM (
		SELECT source_id, binding_id, object_key, document_id
		FROM (
			SELECT source_id, binding_id, object_key, document_id,
				ROW_NUMBER() OVER (
					PARTITION BY source_id, binding_id, object_key
					ORDER BY updated_at DESC, created_at DESC, document_id DESC
				) AS rn
			FROM documents
		)
		WHERE rn = 1
	) AS keeper
	WHERE keeper.source_id = parse_tasks.source_id
	  AND keeper.binding_id = parse_tasks.binding_id
	  AND keeper.object_key = parse_tasks.object_key
)
WHERE EXISTS (
	SELECT 1
	FROM documents current_document
	WHERE current_document.document_id = parse_tasks.document_id
)
AND document_id <> (
	SELECT keeper.document_id
	FROM (
		SELECT source_id, binding_id, object_key, document_id
		FROM (
			SELECT source_id, binding_id, object_key, document_id,
				ROW_NUMBER() OVER (
					PARTITION BY source_id, binding_id, object_key
					ORDER BY updated_at DESC, created_at DESC, document_id DESC
				) AS rn
			FROM documents
		)
		WHERE rn = 1
	) AS keeper
	JOIN documents current_document ON current_document.document_id = parse_tasks.document_id
	WHERE keeper.source_id = current_document.source_id
	  AND keeper.binding_id = current_document.binding_id
	  AND keeper.object_key = current_document.object_key
)`,
		`
DELETE FROM documents
WHERE rowid IN (
	SELECT rowid
	FROM (
		SELECT rowid,
			ROW_NUMBER() OVER (
				PARTITION BY source_id, binding_id, object_key
				ORDER BY updated_at DESC, created_at DESC, document_id DESC
			) AS rn
		FROM documents
	)
	WHERE rn > 1
)`,
	} {
		if err := r.orm.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *SQLRepository) dedupeSQLiteSyncRuns() error {
	statement := `
DELETE FROM source_sync_runs
WHERE rowid IN (
	SELECT rowid
	FROM (
		SELECT rowid,
			ROW_NUMBER() OVER (
				PARTITION BY binding_id, binding_generation, scheduled_fire_at
				ORDER BY started_at DESC, run_id DESC
			) AS rn
		FROM source_sync_runs
		WHERE trigger_type = 'scheduled'
		  AND scheduled_fire_at IS NOT NULL
		  AND status IN ('PENDING', 'RUNNING')
	)
	WHERE rn > 1
)`
	return r.orm.Exec(statement).Error
}

func (r *SQLRepository) dedupeSQLiteCreateOperations() error {
	statement := `
DELETE FROM data_source_create_operations
WHERE rowid IN (
	SELECT rowid
	FROM (
		SELECT rowid,
			ROW_NUMBER() OVER (
				PARTITION BY caller_id, request_id
				ORDER BY
					CASE status
						WHEN 'SUCCEEDED' THEN 0
						WHEN 'SUCCEEDED_WITH_WARNING' THEN 1
						ELSE 2
					END,
					updated_at DESC,
					created_at DESC,
					operation_id DESC
			) AS rn
		FROM data_source_create_operations
	)
	WHERE rn > 1
)`
	return r.orm.Exec(statement).Error
}

func (r *SQLRepository) ormDB(ctx context.Context) *gorm.DB {
	if r.orm == nil {
		return nil
	}
	return r.orm.WithContext(ctx)
}

func (r *SQLRepository) withORMTx(ctx context.Context, fn func(*gorm.DB) error) error {
	if r.orm == nil {
		return NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	err := r.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(tx)
	})
	return mapSQLConstraint(err)
}

type scanner interface {
	Scan(dest ...any) error
}

func mapSQLConstraint(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	for _, constraint := range []string{"uk_source_binding_current_target", "uk_create_operation", "uk_parse_task_idempotency"} {
		if strings.Contains(text, constraint) {
			return MapConstraintError(constraint)
		}
	}
	return err
}

func mustJSON(value JSON) any {
	if value == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return string(body)
}

func decodeJSON(body []byte) JSON {
	if len(body) == 0 {
		return nil
	}
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	return JSON(value)
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func normalizeSQLPage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	return page, normalizeSQLPageSize(pageSize)
}

func normalizeSQLPageSize(pageSize int) int {
	if pageSize <= 0 {
		return 20
	}
	return pageSize
}

// Migrate applies incremental schema changes not covered by the baseline migration (idempotent).
func (r *SQLRepository) Migrate(ctx context.Context) error {
	if r.orm == nil {
		return nil
	}
	for _, statement := range []string{
		"ALTER TABLE source_bindings ADD COLUMN IF NOT EXISTS chat_enabled BOOLEAN NOT NULL DEFAULT TRUE",
		"ALTER TABLE agent_commands ADD COLUMN IF NOT EXISTS queue_generation BIGINT NOT NULL DEFAULT 1",
		"CREATE INDEX IF NOT EXISTS idx_agent_commands_current_pending ON agent_commands (queue_generation, agent_id, status, next_retry_at, created_at)",
		"ALTER TABLE documents DROP CONSTRAINT IF EXISTS documents_binding_id_fkey",
		"ALTER TABLE parse_tasks DROP CONSTRAINT IF EXISTS parse_tasks_binding_id_fkey",
		"ALTER TABLE source_sync_runs DROP CONSTRAINT IF EXISTS source_sync_runs_binding_id_fkey",
	} {
		if err := r.orm.WithContext(ctx).Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}
