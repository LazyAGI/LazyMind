package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/episode"
	"lazymind/core/migrate"
	"lazymind/core/state"
)

func TestDesktopUpdateCoreFormatFixtureMatchesSchemaOwner(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "core.db")
	runner, err := migrate.NewRunner("sqlite", filename, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(0); err != nil {
		t.Fatal(err)
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open(filename), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err := episode.Initialize(db); err != nil {
		t.Fatal(err)
	}
	checkCoreFormatProducer(t, raw, "core")
	kvPath := filepath.Join(root, "core-state.db")
	kv, err := state.NewSQLiteStore(kvPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
	kvDB, err := sql.Open("sqlite", kvPath)
	if err != nil {
		t.Fatal(err)
	}
	defer kvDB.Close()
	checkCoreFormatProducer(t, kvDB, "core-state")
}

func checkCoreFormatProducer(t *testing.T, db *sql.DB, family string) {
	t.Helper()
	type object struct{ Type, Name, Table, SQL string }
	type migration struct {
		Version uint64
		Name    string
	}
	var fixture struct {
		Shape struct {
			Schema      []object
			CoreVersion uint64
			CoreHistory []migration
		}
	}
	file := filepath.Join("..", "..", "local", "local-runtime-manager", "testdata", "update-format", family+".json")
	data, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(data, &fixture) != nil {
		t.Fatalf("read update format fixture: %v", err)
	}
	rows, err := db.Query("SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE sql IS NOT NULL AND name NOT GLOB 'sqlite_*' ORDER BY type COLLATE BINARY,name COLLATE BINARY")
	if err != nil {
		t.Fatal(err)
	}
	var actual []object
	for rows.Next() {
		var entry object
		if err := rows.Scan(&entry.Type, &entry.Name, &entry.Table, &entry.SQL); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, entry)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, fixture.Shape.Schema) {
		t.Fatalf("%s producer drift: actual=%d objects fixture=%d; review the format recipe rather than silently accepting a new schema", family, len(actual), len(fixture.Shape.Schema))
	}
	if family == "core" {
		var version uint64
		if err := db.QueryRow("SELECT version FROM schema_migrations").Scan(&version); err != nil || version != fixture.Shape.CoreVersion {
			t.Fatalf("core migration version drift: %d %v", version, err)
		}
		historyRows, err := db.Query("SELECT version,name FROM schema_migration_history ORDER BY version")
		if err != nil {
			t.Fatal(err)
		}
		defer historyRows.Close()
		var history []migration
		for historyRows.Next() {
			var entry migration
			if err := historyRows.Scan(&entry.Version, &entry.Name); err != nil {
				t.Fatal(err)
			}
			history = append(history, entry)
		}
		if !reflect.DeepEqual(history, fixture.Shape.CoreHistory) {
			t.Fatal("core migration history fixture no longer matches its actual owner")
		}
	}
}
