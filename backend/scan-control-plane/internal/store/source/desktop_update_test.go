package source

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestDesktopUpdateCountsPendingRunningAndSubmittedScanWork(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "scan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{`CREATE TABLE parse_tasks (status TEXT)`, `CREATE TABLE source_sync_runs (status TEXT)`, `INSERT INTO parse_tasks VALUES ('PENDING'),('RUNNING'),('SUBMITTED'),('FAILED'),('SUCCEEDED'),('SUPERSEDED')`, `INSERT INTO source_sync_runs VALUES ('PENDING'),('RUNNING'),('FAILED')`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewSQLRepositoryWithDriver("sqlite", db)
	n, err := repo.CountDesktopUpdateWork(context.Background())
	if err != nil || n != 5 {
		t.Fatal(n, err)
	}
	if _, err := db.Exec(`DROP TABLE parse_tasks`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CountDesktopUpdateWork(context.Background()); err == nil {
		t.Fatal("unreadable tasks counted as idle")
	}
}
