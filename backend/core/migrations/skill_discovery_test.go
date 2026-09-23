package migrations_test

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func applySQL(t *testing.T, db *sql.DB, path, dialect string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	active := true
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "-- +migrate Dialect ") {
			active = false
			for _, supported := range strings.Split(strings.TrimPrefix(line, "-- +migrate Dialect "), ",") {
				if strings.EqualFold(strings.TrimSpace(supported), dialect) || strings.TrimSpace(supported) == "*" {
					active = true
					break
				}
			}
			continue
		}
		if active {
			lines = append(lines, line)
		}
	}
	if _, err := db.Exec(strings.Join(lines, "\n")); err != nil {
		t.Fatalf("%s %s: %v", dialect, path, err)
	}
}

func openDatabase(t *testing.T, dialect string) *sql.DB {
	t.Helper()
	if dialect == "sqlite" {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	dsn := os.Getenv("MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_POSTGRES_DSN required")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("skill_discovery_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); admin.Exec("DROP DATABASE " + name + " WITH (FORCE)"); admin.Close() })
	return db
}

func migrationPath(t *testing.T, pattern string) string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil || len(paths) != 1 {
		t.Fatalf("migration %q paths=%v err=%v", pattern, paths, err)
	}
	return paths[0]
}

func TestSkillDiscoveryMigrationBackfillAndRollback(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openDatabase(t, dialect)
			_, err := db.Exec(`CREATE TABLE skills(id VARCHAR(36) PRIMARY KEY, owner_user_id TEXT NOT NULL, head_revision_id VARCHAR(36), is_enabled BOOLEAN NOT NULL, tags JSON, created_at TIMESTAMP NOT NULL);
CREATE TABLE skill_revisions(id VARCHAR(36) PRIMARY KEY,skill_id VARCHAR(36),revision_no BIGINT,created_at TIMESTAMP);
INSERT INTO skills VALUES ('history','u1','latest',FALSE,'["existing"]','2026-09-19 00:00:00'),('draft','u1',NULL,TRUE,'[]','2026-09-19 00:00:00');
INSERT INTO skill_revisions VALUES ('latest','history',2,'2026-09-19 00:00:00'),('original','history',1,'2026-09-20 00:00:00');`)
			if err != nil {
				t.Fatal(err)
			}
			up := migrationPath(t, "dev_mode/v0_3/*skill_discovery_and_original.up.sql")
			applySQL(t, db, up, dialect)
			var original, mode, tags string
			var enabled bool
			var rank int64
			if err := db.QueryRow("SELECT original_revision_id,call_mode,tags,is_enabled,sort_rank FROM skills WHERE id='history'").Scan(&original, &mode, &tags, &enabled, &rank); err != nil {
				t.Fatal(err)
			}
			if original != "original" || mode != "manual" || enabled || rank <= 0 || tags != `["existing"]` {
				t.Fatalf("backfill original=%s mode=%s enabled=%v rank=%d tags=%s", original, mode, enabled, rank, tags)
			}
			var draftOriginal sql.NullString
			if err := db.QueryRow("SELECT original_revision_id FROM skills WHERE id='draft'").Scan(&draftOriginal); err != nil || draftOriginal.Valid {
				t.Fatalf("draft original=%v err=%v", draftOriginal, err)
			}
			applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			var head string
			if err := db.QueryRow("SELECT head_revision_id FROM skills WHERE id='history'").Scan(&head); err != nil || head != "latest" {
				t.Fatalf("rollback head=%s err=%v", head, err)
			}
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM skill_revisions").Scan(&count); err != nil || count != 2 {
				t.Fatalf("history count=%d err=%v", count, err)
			}
		})
	}
}

func sqliteSkillSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	var lines []string
	for _, table := range []string{"skills", "skill_revisions"} {
		rows, err := db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var cid, notnull, pk int
			var name, kind string
			var def sql.NullString
			if err := rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprintf("%s|%s|%s|%d|%s|%d", table, name, strings.ToLower(kind), notnull, def.String, pk))
		}
		rows.Close()
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestSQLiteCurrentSkillAggregateMatchesDevAndRollsBack(t *testing.T) {
	release, dev := openDatabase(t, "sqlite"), openDatabase(t, "sqlite")
	for _, db := range []*sql.DB{release, dev} {
		for _, version := range []string{"v0_1", "v0_2"} {
			applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), "sqlite")
		}
	}
	baseline := sqliteSkillSchema(t, release)
	aggregate := migrationPath(t, "version_mode/v0_3/*.up.sql")
	applySQL(t, release, aggregate, "sqlite")
	paths, err := filepath.Glob("dev_mode/v0_3/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	for _, path := range paths {
		applySQL(t, dev, path, "sqlite")
	}
	if a, b := sqliteSkillSchema(t, release), sqliteSkillSchema(t, dev); a != b {
		t.Fatalf("aggregate and dev skill schemas differ\n%s\n---\n%s", a, b)
	}
	applySQL(t, release, strings.Replace(aggregate, ".up.sql", ".down.sql", 1), "sqlite")
	if got := sqliteSkillSchema(t, release); got != baseline {
		t.Fatalf("aggregate down differs from baseline\n%s\n---\n%s", got, baseline)
	}
}
