package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"lazymind/core/internal/updateformattest"
	"lazymind/core/state"
)

func TestDesktopUpdateCoreFormatFixtureMatchesSchemaOwner(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "core.db")
	started := time.Now()
	if err := updateformattest.CreateCoreDatabase(filename); err != nil {
		t.Fatal(err)
	}
	t.Logf("generated Core SQLite fixture in %s", time.Since(started).Round(time.Millisecond))
	if err := verifyBootstrapDatabase(t.Context(), filename, expectedCoreFormatSHA, true, false); err != nil {
		t.Fatalf("Core schema or migration history does not match the pinned format: %v", err)
	}
	t.Run("rejects schema drift", func(t *testing.T) {
		db, err := sql.Open("sqlite", filename)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec("CREATE TABLE unexpected_core_fixture_table (id INTEGER)")
		if err := errors.Join(err, db.Close()); err != nil {
			t.Fatal(err)
		}
		if err := verifyBootstrapDatabase(t.Context(), filename, expectedCoreFormatSHA, true, false); err == nil {
			t.Fatal("accepted Core schema drift against the pinned format")
		}
	})
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
	checkCoreStateFormatProducer(t, kvDB)
}

func checkCoreStateFormatProducer(t *testing.T, db *sql.DB) {
	t.Helper()
	type object struct{ Type, Name, Table, SQL string }
	var fixture struct {
		Shape struct {
			Schema []object
		}
	}
	file := filepath.Join("..", "..", "local", "local-runtime-manager", "testdata", "update-format", "core-state.json")
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
		t.Fatalf("core-state producer drift: actual=%d objects fixture=%d; review the format recipe rather than silently accepting a new schema", len(actual), len(fixture.Shape.Schema))
	}
}
