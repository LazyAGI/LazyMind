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
	return r.orm.AutoMigrate(
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
	)
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
