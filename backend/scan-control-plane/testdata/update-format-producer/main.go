// This test fixture producer is never included in the service binary.
package main

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "github.com/glebarez/go-sqlite"
	"github.com/lazymind/scan_control_plane/internal/state"
	"github.com/lazymind/scan_control_plane/internal/store/source"
)

func main() {
	root := os.Args[1]
	db, err := sql.Open("sqlite", filepath.Join(root, "scan.db"))
	if err != nil {
		panic(err)
	}
	if err := source.NewSQLRepositoryWithDriver("sqlite", db).AutoMigrate(); err != nil {
		panic(err)
	}
	if err := db.Close(); err != nil {
		panic(err)
	}
	store, err := state.NewSQLiteStore(filepath.Join(root, "scan-state.db"))
	if err != nil {
		panic(err)
	}
	if err := store.Close(); err != nil {
		panic(err)
	}
}
