package source

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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
	orm, err := gorm.Open(dialector, &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		return &SQLRepository{db: db}
	}
	return &SQLRepository{db: db, orm: orm}
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
		return r.repairSQLiteSchema()
	}
	return nil
}

func (r *SQLRepository) repairSQLiteSchema() error {
	if err := r.dedupeSQLiteCreateOperations(); err != nil {
		return err
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
			return mapSQLConstraint(err)
		}
	}
	return nil
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
	return mapSQLConstraint(r.orm.Exec(statement).Error)
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
