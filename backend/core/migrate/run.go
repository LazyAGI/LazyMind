// Package migrate runs versioned SQL migrations for core.
package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"lazymind/core/log"
)

const (
	defaultSQLiteDSN     = "./acl.db"
	defaultMigrationsDir = "./migrations"
	stateTableName       = "schema_migrations"
	historyTableName     = "schema_migration_history"
	lockTableName        = "schema_migration_lock"
	lockKey              = "core"
)

var migrationFilePattern = regexp.MustCompile(`^(\d+)_(.+)\.(up|down)\.sql$`)

type Runner struct {
	driver string
	dsn    string
	dir    string
	db     *sql.DB
}

type migrationFile struct {
	Version  uint64
	Name     string
	UpPath   string
	DownPath string
}

type historyRecord struct {
	Version uint64
	Name    string
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type releaseFunc func()

func RunUp() error {
	driver := envOr("ACL_DB_DRIVER", "sqlite")
	dsn := strings.TrimSpace(os.Getenv("ACL_DB_DSN"))
	if driver == "sqlite" && dsn == "" {
		dsn = defaultSQLiteDSN
	}
	if dsn == "" {
		return nil
	}

	mDir := envOr("MIGRATIONS_DIR", defaultMigrationsDir)
	absDir, err := filepath.Abs(mDir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(absDir); os.IsNotExist(err) {
		log.Logger.Debug().Str("dir", absDir).Msg("migrations dir missing, skip RunUp")
		return nil
	}

	runner, err := NewRunner(driver, dsn, absDir)
	if err != nil {
		return err
	}
	defer runner.Close()

	if err := runner.Up(0); err != nil {
		return err
	}
	log.Logger.Info().Str("dir", absDir).Msg("SQL migrations applied")
	return nil
}

func NewRunnerFromEnv() (*Runner, error) {
	driver := envOr("ACL_DB_DRIVER", "sqlite")
	dsn := strings.TrimSpace(os.Getenv("ACL_DB_DSN"))
	if driver == "sqlite" && dsn == "" {
		dsn = defaultSQLiteDSN
	}
	if dsn == "" {
		return nil, fmt.Errorf("ACL_DB_DSN is empty")
	}
	return NewRunner(driver, dsn, envOr("MIGRATIONS_DIR", defaultMigrationsDir))
}

func NewRunner(driver, dsn, dir string) (*Runner, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("empty dsn")
	}
	if strings.TrimSpace(dir) == "" {
		dir = defaultMigrationsDir
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	db, _, err := openSQL(driver, dsn)
	if err != nil {
		return nil, err
	}
	return &Runner{
		driver: driver,
		dsn:    dsn,
		dir:    absDir,
		db:     db,
	}, nil
}

func (r *Runner) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *Runner) Up(limit int) error {
	if err := r.prepare(); err != nil {
		return err
	}
	release, err := r.acquireLock()
	if err != nil {
		return err
	}
	defer release()

	version, dirty, hasVersion, err := r.readState()
	if err != nil {
		return err
	}
	if dirty {
		return dirtyDatabaseError(version)
	}

	migrations, err := r.loadMigrations()
	if err != nil {
		return err
	}
	if err := r.bootstrapHistoryIfNeeded(migrations, hasVersion, version); err != nil {
		return err
	}

	applied, err := r.readHistory()
	if err != nil {
		return err
	}
	missing := missingUpMigrations(migrations, applied)
	if limit > 0 && len(missing) > limit {
		missing = missing[:limit]
	}

	currentMax := highestAppliedVersion(applied)
	for _, mig := range missing {
		if err := r.applyUpMigration(mig, currentMax); err != nil {
			return err
		}
		if mig.Version > currentMax {
			currentMax = mig.Version
		}
	}
	return nil
}

func (r *Runner) Down(steps int) error {
	if steps <= 0 {
		return fmt.Errorf("steps must be > 0")
	}
	if err := r.prepare(); err != nil {
		return err
	}
	release, err := r.acquireLock()
	if err != nil {
		return err
	}
	defer release()

	version, dirty, hasVersion, err := r.readState()
	if err != nil {
		return err
	}
	if dirty {
		return dirtyDatabaseError(version)
	}

	migrations, err := r.loadMigrations()
	if err != nil {
		return err
	}
	if err := r.bootstrapHistoryIfNeeded(migrations, hasVersion, version); err != nil {
		return err
	}
	applied, err := r.readHistory()
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		return nil
	}

	if steps > len(applied) {
		steps = len(applied)
	}
	for i := 0; i < steps; i++ {
		record := applied[len(applied)-1-i]
		mig, ok := findMigration(migrations, record.Version)
		if !ok {
			return fmt.Errorf("missing migration file for applied version %d", record.Version)
		}
		if err := r.applyDownMigration(mig, applied[:len(applied)-1-i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) Goto(target uint64) error {
	if err := r.prepare(); err != nil {
		return err
	}
	release, err := r.acquireLock()
	if err != nil {
		return err
	}
	defer release()

	version, dirty, hasVersion, err := r.readState()
	if err != nil {
		return err
	}
	if dirty {
		return dirtyDatabaseError(version)
	}

	migrations, err := r.loadMigrations()
	if err != nil {
		return err
	}
	if err := r.bootstrapHistoryIfNeeded(migrations, hasVersion, version); err != nil {
		return err
	}

	applied, err := r.readHistory()
	if err != nil {
		return err
	}

	for i := len(applied) - 1; i >= 0; i-- {
		if applied[i].Version <= target {
			break
		}
		mig, ok := findMigration(migrations, applied[i].Version)
		if !ok {
			return fmt.Errorf("missing migration file for applied version %d", applied[i].Version)
		}
		if err := r.applyDownMigration(mig, applied[:i]); err != nil {
			return err
		}
	}

	applied, err = r.readHistory()
	if err != nil {
		return err
	}
	appliedMap := make(map[uint64]historyRecord, len(applied))
	currentMax := highestAppliedVersion(applied)
	for _, item := range applied {
		appliedMap[item.Version] = item
	}
	for _, mig := range migrations {
		if mig.Version > target || mig.UpPath == "" {
			continue
		}
		if _, ok := appliedMap[mig.Version]; ok {
			continue
		}
		if err := r.applyUpMigration(mig, currentMax); err != nil {
			return err
		}
		appliedMap[mig.Version] = historyRecord{Version: mig.Version, Name: mig.Name}
		if mig.Version > currentMax {
			currentMax = mig.Version
		}
	}
	return nil
}

func (r *Runner) Version() (uint64, bool, error) {
	if err := r.ensureStateTable(); err != nil {
		return 0, false, err
	}
	version, dirty, hasVersion, err := r.readState()
	if err != nil {
		return 0, false, err
	}
	if !hasVersion {
		return 0, false, nil
	}
	return version, dirty, nil
}

func (r *Runner) Force(version uint64) error {
	if err := r.prepare(); err != nil {
		return err
	}
	release, err := r.acquireLock()
	if err != nil {
		return err
	}
	defer release()

	migrations, err := r.loadMigrations()
	if err != nil {
		return err
	}
	if count, err := r.historyCount(); err != nil {
		return err
	} else if count == 0 && version > 0 {
		if err := r.bootstrapHistoryIfNeeded(migrations, true, version); err != nil {
			return err
		}
	}
	if version == 0 {
		return r.writeState(r.db, nil, false)
	}
	return r.writeState(r.db, &version, false)
}

func (r *Runner) prepare() error {
	if _, err := os.Stat(r.dir); err != nil {
		return err
	}
	if err := r.ensureStateTable(); err != nil {
		return err
	}
	if err := r.ensureHistoryTable(); err != nil {
		return err
	}
	if err := r.ensureLockTable(); err != nil {
		return err
	}
	return nil
}

func (r *Runner) ensureStateTable() error {
	queries := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)`, stateTableName),
	}
	if r.driver == "sqlite" {
		queries = []string{
			fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (version uint64, dirty bool)`, stateTableName),
			fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS version_unique ON %s (version)`, stateTableName),
		}
	}
	for _, query := range queries {
		if _, err := r.db.Exec(query); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) ensureHistoryTable() error {
	query := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
  version bigint NOT NULL PRIMARY KEY,
  name varchar(255) NOT NULL DEFAULT '',
  applied_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
)`, historyTableName)
	_, err := r.db.Exec(query)
	return err
}

func (r *Runner) ensureLockTable() error {
	query := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
  lock_key varchar(64) NOT NULL PRIMARY KEY,
  owner_id varchar(128) NOT NULL,
  expires_at timestamp NOT NULL
)`, lockTableName)
	_, err := r.db.Exec(query)
	return err
}

func (r *Runner) readState() (uint64, bool, bool, error) {
	var version uint64
	var dirty bool
	err := r.db.QueryRow(fmt.Sprintf(`SELECT version, dirty FROM %s LIMIT 1`, stateTableName)).Scan(&version, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, err
	}
	return version, dirty, true, nil
}

func (r *Runner) historyCount() (int, error) {
	var count int
	err := r.db.QueryRow(fmt.Sprintf(`SELECT COUNT(1) FROM %s`, historyTableName)).Scan(&count)
	return count, err
}

func (r *Runner) acquireLock() (releaseFunc, error) {
	owner := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UTC().UnixNano())
	deadline := time.Now().UTC().Add(30 * time.Second)

	for {
		tx, err := r.db.Begin()
		if err != nil {
			return nil, err
		}

		if _, err := tx.Exec(r.lockDeleteExpiredSQL(), time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if _, err := tx.Exec(r.lockInsertSQL(), lockKey, owner, time.Now().UTC().Add(30*time.Minute)); err != nil {
			_ = tx.Rollback()
			if isLockConflict(err) && time.Now().UTC().Before(deadline) {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			if isLockConflict(err) {
				return nil, fmt.Errorf("timed out waiting for migration lock")
			}
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}

		return func() {
			if _, err := r.db.Exec(r.lockReleaseSQL(), lockKey, owner); err != nil {
				log.Logger.Warn().Err(err).Str("owner", owner).Msg("release migration lock failed")
			}
		}, nil
	}
}

func (r *Runner) bootstrapHistoryIfNeeded(migrations []migrationFile, hasVersion bool, version uint64) error {
	count, err := r.historyCount()
	if err != nil {
		return err
	}
	if count > 0 || !hasVersion {
		return nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	seeded := 0
	for _, mig := range migrations {
		if mig.Version > version || mig.UpPath == "" {
			continue
		}
		if err := r.insertHistory(tx, mig.Version, mig.Name); err != nil {
			_ = tx.Rollback()
			return err
		}
		seeded++
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if seeded > 0 {
		log.Logger.Info().
			Uint64("version", version).
			Int("count", seeded).
			Msg("bootstrapped schema_migration_history from schema_migrations state")
	}
	return nil
}

func (r *Runner) readHistory() ([]historyRecord, error) {
	rows, err := r.db.Query(fmt.Sprintf(`SELECT version, name FROM %s ORDER BY version`, historyTableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []historyRecord
	for rows.Next() {
		var item historyRecord
		if err := rows.Scan(&item.Version, &item.Name); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *Runner) loadMigrations() ([]migrationFile, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil, err
	}

	seen := make(map[uint64]*migrationFile)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := migrationFilePattern.FindStringSubmatch(entry.Name())
		if len(matches) != 4 {
			continue
		}
		version, err := strconv.ParseUint(matches[1], 10, 64)
		if err != nil {
			return nil, err
		}
		name := matches[2]
		direction := matches[3]

		item, ok := seen[version]
		if !ok {
			item = &migrationFile{Version: version, Name: name}
			seen[version] = item
		} else if item.Name != name {
			return nil, fmt.Errorf("duplicate migration version %d with different names: %s vs %s", version, item.Name, name)
		}

		fullPath := filepath.Join(r.dir, entry.Name())
		switch direction {
		case "up":
			if item.UpPath != "" {
				return nil, fmt.Errorf("duplicate up migration for version %d", version)
			}
			item.UpPath = fullPath
		case "down":
			if item.DownPath != "" {
				return nil, fmt.Errorf("duplicate down migration for version %d", version)
			}
			item.DownPath = fullPath
		}
	}

	out := make([]migrationFile, 0, len(seen))
	for _, item := range seen {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func missingUpMigrations(migrations []migrationFile, applied []historyRecord) []migrationFile {
	appliedSet := make(map[uint64]struct{}, len(applied))
	for _, item := range applied {
		appliedSet[item.Version] = struct{}{}
	}

	var out []migrationFile
	for _, mig := range migrations {
		if mig.UpPath == "" {
			continue
		}
		if _, ok := appliedSet[mig.Version]; ok {
			continue
		}
		out = append(out, mig)
	}
	return out
}

func highestAppliedVersion(applied []historyRecord) uint64 {
	if len(applied) == 0 {
		return 0
	}
	return applied[len(applied)-1].Version
}

func findMigration(migrations []migrationFile, version uint64) (migrationFile, bool) {
	for _, mig := range migrations {
		if mig.Version == version {
			return mig, true
		}
	}
	return migrationFile{}, false
}

func (r *Runner) applyUpMigration(mig migrationFile, currentMax uint64) error {
	body, err := os.ReadFile(mig.UpPath)
	if err != nil {
		return err
	}
	if err := r.writeState(r.db, &mig.Version, true); err != nil {
		return err
	}

	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(string(body)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := r.insertHistory(tx, mig.Version, mig.Name); err != nil {
		_ = tx.Rollback()
		return err
	}
	nextVersion := currentMax
	if mig.Version > nextVersion {
		nextVersion = mig.Version
	}
	if err := r.writeState(tx, &nextVersion, false); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Logger.Info().Uint64("version", mig.Version).Str("name", mig.Name).Msg("migration up applied")
	return nil
}

func (r *Runner) applyDownMigration(mig migrationFile, remaining []historyRecord) error {
	if mig.DownPath == "" {
		return fmt.Errorf("missing down migration for version %d", mig.Version)
	}
	body, err := os.ReadFile(mig.DownPath)
	if err != nil {
		return err
	}
	if err := r.writeState(r.db, &mig.Version, true); err != nil {
		return err
	}

	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	if err := r.deleteHistory(tx, mig.Version); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(string(body)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if len(remaining) == 0 {
		if err := r.writeState(tx, nil, false); err != nil {
			_ = tx.Rollback()
			return err
		}
	} else {
		nextVersion := remaining[len(remaining)-1].Version
		if err := r.writeState(tx, &nextVersion, false); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Logger.Info().Uint64("version", mig.Version).Str("name", mig.Name).Msg("migration down applied")
	return nil
}

func (r *Runner) insertHistory(exec sqlExecer, version uint64, name string) error {
	_, err := exec.Exec(r.historyInsertSQL(), version, name, time.Now().UTC())
	return err
}

func (r *Runner) deleteHistory(exec sqlExecer, version uint64) error {
	_, err := exec.Exec(r.historyDeleteSQL(), version)
	return err
}

func (r *Runner) writeState(exec sqlExecer, version *uint64, dirty bool) error {
	if _, err := exec.Exec(fmt.Sprintf(`DELETE FROM %s`, stateTableName)); err != nil {
		return err
	}
	if version == nil && !dirty {
		return nil
	}
	_, err := exec.Exec(r.stateInsertSQL(), *version, dirty)
	return err
}

func (r *Runner) stateInsertSQL() string {
	if r.driver == "postgres" {
		return fmt.Sprintf(`INSERT INTO %s (version, dirty) VALUES ($1, $2)`, stateTableName)
	}
	return fmt.Sprintf(`INSERT INTO %s (version, dirty) VALUES (?, ?)`, stateTableName)
}

func (r *Runner) historyInsertSQL() string {
	if r.driver == "postgres" {
		return fmt.Sprintf(`INSERT INTO %s (version, name, applied_at) VALUES ($1, $2, $3)`, historyTableName)
	}
	return fmt.Sprintf(`INSERT INTO %s (version, name, applied_at) VALUES (?, ?, ?)`, historyTableName)
}

func (r *Runner) historyDeleteSQL() string {
	if r.driver == "postgres" {
		return fmt.Sprintf(`DELETE FROM %s WHERE version = $1`, historyTableName)
	}
	return fmt.Sprintf(`DELETE FROM %s WHERE version = ?`, historyTableName)
}

func (r *Runner) lockDeleteExpiredSQL() string {
	if r.driver == "postgres" {
		return fmt.Sprintf(`DELETE FROM %s WHERE expires_at < $1`, lockTableName)
	}
	return fmt.Sprintf(`DELETE FROM %s WHERE expires_at < ?`, lockTableName)
}

func (r *Runner) lockInsertSQL() string {
	if r.driver == "postgres" {
		return fmt.Sprintf(`INSERT INTO %s (lock_key, owner_id, expires_at) VALUES ($1, $2, $3)`, lockTableName)
	}
	return fmt.Sprintf(`INSERT INTO %s (lock_key, owner_id, expires_at) VALUES (?, ?, ?)`, lockTableName)
}

func (r *Runner) lockReleaseSQL() string {
	if r.driver == "postgres" {
		return fmt.Sprintf(`DELETE FROM %s WHERE lock_key = $1 AND owner_id = $2`, lockTableName)
	}
	return fmt.Sprintf(`DELETE FROM %s WHERE lock_key = ? AND owner_id = ?`, lockTableName)
}

func isLockConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "unique violation")
}

func dirtyDatabaseError(version uint64) error {
	return fmt.Errorf("dirty database version %d", version)
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func openSQL(driver, dsn string) (*sql.DB, string, error) {
	switch driver {
	case "sqlite":
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, "", err
		}
		return db, dsn, nil
	case "postgres":
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, "", err
		}
		return db, "", nil
	case "mysql":
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, "", err
		}
		return db, "", nil
	default:
		return nil, "", fmt.Errorf("unsupported driver: %s", driver)
	}
}
