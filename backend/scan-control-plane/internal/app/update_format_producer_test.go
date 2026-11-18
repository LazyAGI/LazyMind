package app

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	scanstate "github.com/lazymind/scan_control_plane/internal/state"
	sourcestore "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestUpdateFormatScanFixturesMatchActualOwners(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "scan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sourcestore.NewSQLRepositoryWithDriver("sqlite", db).AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	checkScanFormatProducer(t, db, "scan")
	filename := filepath.Join(root, "scan-state.db")
	state, err := scanstate.NewSQLiteStore(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	stateDB, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	defer stateDB.Close()
	checkScanFormatProducer(t, stateDB, "scan-state")
}

func checkScanFormatProducer(t *testing.T, db *sql.DB, family string) {
	t.Helper()
	type object struct{ Type, Name, Table, SQL string }
	var fixture struct{ Shape struct{ Schema []object } }
	file := filepath.Join("..", "..", "..", "..", "local", "local-runtime-manager", "testdata", "update-format", family+".json")
	data, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(data, &fixture) != nil {
		t.Fatalf("read update format fixture: %v", err)
	}
	rows, err := db.Query("SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE sql IS NOT NULL AND name NOT GLOB 'sqlite_*' ORDER BY type COLLATE BINARY,name COLLATE BINARY")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []object
	for rows.Next() {
		var entry object
		if err := rows.Scan(&entry.Type, &entry.Name, &entry.Table, &entry.SQL); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, entry)
	}
	if !reflect.DeepEqual(actual, fixture.Shape.Schema) {
		t.Fatalf("%s producer drift: actual=%d objects fixture=%d; update the supported format deliberately", family, len(actual), len(fixture.Shape.Schema))
	}
}
