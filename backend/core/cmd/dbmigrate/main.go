package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"lazymind/core/common/orm"
	"lazymind/core/log"
	coremigrate "lazymind/core/migrate"
)

func main() {
	log.Init()

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	switch cmd {
	case "migrate":
		migrateCmd(os.Args[2:])
	case "upgrade":
		upCmd(os.Args[2:])
	case "create":
		createCmd(os.Args[2:])
	case "up":
		upCmd(os.Args[2:])
	case "down":
		downCmd(os.Args[2:])
	case "goto":
		gotoCmd(os.Args[2:])
	case "version":
		versionCmd(os.Args[2:])
	case "force":
		forceCmd(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `dbmigrate: SQL migrations (text Flask-Migrate: migrate text, upgrade text).

  Flask text:
    flask db migrate [-m "msg"]  →  dbmigrate migrate [-m "msg"]
    flask db upgrade             →  dbmigrate upgrade  text  core Starttext RunUp()

Env: ACL_DB_DRIVER, ACL_DB_DSN, MIGRATIONS_DIR (default: ./migrations)

Commands:
  migrate [-m "message"]         text（text orm/models text DDL，text Postgres + ACL_DB_DSN）
  upgrade                       text（text）
  create -name <name> [-with-ddl]  textCreatetext（text -with-ddl text DDL）
  up [-n <steps>]               text upgrade；-n text
  down [-n <steps>]              text N text
  goto -version <v>              text
  version                        text
  force -version <v>              text dirty（textFailedtext）
`)
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func migrationsDir() string {
	return envOr("MIGRATIONS_DIR", "./migrations")
}

func dbConfigFromEnv() (driver, dsn string) {
	driver = envOr("ACL_DB_DRIVER", "sqlite")
	dsn = strings.TrimSpace(os.Getenv("ACL_DB_DSN"))
	if driver == "sqlite" && dsn == "" {
		dsn = "./acl.db"
	}
	return driver, dsn
}

// migrateCmd text Flask text flask db migrate：text DDL text（-m text）。
func migrateCmd(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	msg := fs.String("m", "auto", "migration message (used as migration name)")
	_ = fs.Parse(args)
	name := strings.TrimSpace(*msg)
	if name == "" {
		name = "auto"
	}
	createCmdWith(sanitizeName(name), true)
}

func createCmd(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	name := fs.String("name", "", "migration name, e.g. init or add_xxx")
	withDDL := fs.Bool("with-ddl", false, "generate full CREATE TABLE from orm/models (requires postgres + ACL_DB_DSN)")
	_ = fs.Parse(args)
	if strings.TrimSpace(*name) == "" {
		log.Logger.Error().Msg("create: -name is required")
		os.Exit(2)
	}
	createCmdWith(sanitizeName(*name), *withDDL)
}

func createCmdWith(name string, withDDL bool) {
	dir := migrationsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Logger.Error().Err(err).Msg("create: mkdir failed")
		os.Exit(1)
	}

	ts := time.Now().UTC().Format("20060102150405")
	base := fmt.Sprintf("%s_%s", ts, name)
	upPath := filepath.Join(dir, base+".up.sql")
	downPath := filepath.Join(dir, base+".down.sql")

	var upContent, downContent string
	if withDDL {
		driver, dsn := dbConfigFromEnv()
		if driver != "postgres" || strings.TrimSpace(dsn) == "" {
			log.Logger.Error().Msg("create -with-ddl requires ACL_DB_DRIVER=postgres and ACL_DB_DSN")
			os.Exit(2)
		}
		upSQL, downSQL, err := capturePostgresDDL(dsn)
		if err != nil {
			log.Logger.Error().Err(err).Msg("capture DDL failed")
			os.Exit(1)
		}
		upContent = fmt.Sprintf("-- %s\n-- +migrate Up\n\n%s", base, upSQL)
		downContent = fmt.Sprintf("-- %s\n-- +migrate Down\n\n%s", base, downSQL)
	} else {
		upContent = fmt.Sprintf("-- %s\n-- +migrate Up\n-- text DDL，text create -name xxx -with-ddl text。\n\n", base)
		downContent = fmt.Sprintf("-- %s\n-- +migrate Down\n-- text SQL。\n\n", base)
	}

	writeIfNotExists(upPath, upContent)
	writeIfNotExists(downPath, downContent)
	fmt.Println(upPath)
	fmt.Println(downPath)
}

// capturePostgresDDL text Postgres，text GORM text SQL，text up（CREATE TABLE）text down（DROP TABLE）。
func capturePostgresDDL(dsn string) (upSQL, downSQL string, err error) {
	var statements []string
	recorder := &sqlRecorder{collect: &statements}
	cfg := &gorm.Config{Logger: recorder}
	db, err := gorm.Open(gormpostgres.Open(dsn), cfg)
	if err != nil {
		return "", "", err
	}
	sqlDB, _ := db.DB()
	if sqlDB != nil {
		defer sqlDB.Close()
	}
	for _, m := range orm.AllModelsForDDL() {
		if err := db.Migrator().CreateTable(m); err != nil {
			return "", "", err
		}
	}
	var up strings.Builder
	for _, s := range statements {
		up.WriteString(s)
		if !strings.HasSuffix(strings.TrimSpace(s), ";") {
			up.WriteString(";")
		}
		up.WriteString("\n")
	}
	tables := orm.TableNamesForDDL()
	var down strings.Builder
	for i := len(tables) - 1; i >= 0; i-- {
		down.WriteString("DROP TABLE IF EXISTS ")
		down.WriteString(tables[i])
		down.WriteString(" CASCADE;\n")
	}
	return up.String(), down.String(), nil
}

type sqlRecorder struct {
	collect *[]string
}

func (s *sqlRecorder) LogMode(logger.LogLevel) logger.Interface { return s }

func (s *sqlRecorder) Info(context.Context, string, ...interface{})  {}
func (s *sqlRecorder) Warn(context.Context, string, ...interface{})  {}
func (s *sqlRecorder) Error(context.Context, string, ...interface{}) {}

func (s *sqlRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	sql = strings.TrimSpace(sql)
	if sql != "" {
		*s.collect = append(*s.collect, sql)
	}
}

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	out = strings.Trim(out, "_")
	if out == "" {
		return "migration"
	}
	return out
}

func writeIfNotExists(path, content string) {
	if _, err := os.Stat(path); err == nil {
		log.Logger.Error().Str("path", path).Msg("create: already exists")
		os.Exit(1)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		log.Logger.Error().Err(err).Str("path", path).Msg("create: write failed")
		os.Exit(1)
	}
}

func upCmd(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	n := fs.Int("n", 0, "steps to apply (0 = all)")
	_ = fs.Parse(args)

	runner, err := coremigrate.NewRunnerFromEnv()
	if err != nil {
		log.Logger.Error().Err(err).Msg("open runner failed")
		os.Exit(1)
	}
	defer runner.Close()

	if err := runner.Up(*n); err != nil {
		log.Logger.Error().Err(err).Msg("up failed")
		os.Exit(1)
	}
	log.Logger.Info().Msg("up ok")
}

func downCmd(args []string) {
	fs := flag.NewFlagSet("down", flag.ExitOnError)
	n := fs.Int("n", 1, "steps to rollback")
	_ = fs.Parse(args)

	if *n <= 0 {
		log.Logger.Error().Msg("down: -n must be > 0")
		os.Exit(2)
	}

	runner, err := coremigrate.NewRunnerFromEnv()
	if err != nil {
		log.Logger.Error().Err(err).Msg("open runner failed")
		os.Exit(1)
	}
	defer runner.Close()

	if err := runner.Down(*n); err != nil {
		log.Logger.Error().Err(err).Msg("down failed")
		os.Exit(1)
	}
	log.Logger.Info().Msg("down ok")
}

func gotoCmd(args []string) {
	fs := flag.NewFlagSet("goto", flag.ExitOnError)
	v := fs.Uint("version", 0, "target version, e.g. 20260312093000")
	_ = fs.Parse(args)
	if *v == 0 {
		log.Logger.Error().Msg("goto: -version is required")
		os.Exit(2)
	}

	runner, err := coremigrate.NewRunnerFromEnv()
	if err != nil {
		log.Logger.Error().Err(err).Msg("open runner failed")
		os.Exit(1)
	}
	defer runner.Close()

	target := uint64(*v)
	if err := runner.Goto(target); err != nil {
		log.Logger.Error().Err(err).Uint64("version", target).Msg("goto failed")
		os.Exit(1)
	}
	log.Logger.Info().Uint64("version", target).Msg("goto ok")
}

func versionCmd(args []string) {
	_ = args

	runner, err := coremigrate.NewRunnerFromEnv()
	if err != nil {
		log.Logger.Error().Err(err).Msg("open runner failed")
		os.Exit(1)
	}
	defer runner.Close()

	v, dirty, err := runner.Version()
	if err != nil {
		log.Logger.Error().Err(err).Msg("version failed")
		os.Exit(1)
	}
	if dirty {
		log.Logger.Info().Uint64("version", v).Msg("version: dirty")
		return
	}
	log.Logger.Info().Uint64("version", v).Msg("version: clean")
}

// forceCmd text schema_migrations text dirty，text "Dirty database version" text。
// text（text）。
func forceCmd(args []string) {
	fs := flag.NewFlagSet("force", flag.ExitOnError)
	v := fs.Uint("version", 0, "version to set, e.g. 20260315095955")
	_ = fs.Parse(args)
	if *v == 0 {
		log.Logger.Error().Msg("force: -version is required (e.g. 20260315095955)")
		os.Exit(2)
	}

	runner, err := coremigrate.NewRunnerFromEnv()
	if err != nil {
		log.Logger.Error().Err(err).Msg("open runner failed")
		os.Exit(1)
	}
	defer runner.Close()

	target := uint64(*v)
	if err := runner.Force(target); err != nil {
		log.Logger.Error().Err(err).Uint64("version", target).Msg("force failed")
		os.Exit(1)
	}
	log.Logger.Info().Uint64("version", target).Msg("force ok")
}
