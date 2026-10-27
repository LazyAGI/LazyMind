package desktopupdate

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

func TestUpdateWALFixtureChild(t *testing.T) {
	file := os.Getenv("LAZYMIND_UNIT_UPDATE_WAL_FILE")
	if file == "" {
		return
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		os.Exit(2)
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE records(value INTEGER); INSERT INTO records VALUES(123)"); err != nil {
		os.Exit(3)
	}
	// An exited unit-test process leaves a genuine committed WAL without a
	// final SQLite Close checkpoint. No product service or user DB is involved.
	os.Exit(0)
}

func TestUpdateSnapshotRestoresCommittedWALAndExcludesEphemeralFiles(t *testing.T) {
	root, plan := transactionFixture(t)
	dbPath := filepath.Join(plan.Units[0].Path, "state.db")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, exe, "-test.run=^TestUpdateWALFixtureChild$")
	child.Env = append(os.Environ(), "LAZYMIND_UNIT_UPDATE_WAL_FILE="+dbPath)
	if err = child.Run(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dbPath + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("no real WAL fixture: %v", err)
	}
	if err = os.WriteFile(filepath.Join(plan.Units[0].Path, "process.pid"), []byte("99999"), 0600); err != nil {
		t.Fatal(err)
	}
	plan.Units[0].Exclude = []string{"process.pid"}
	plan.SQLiteFiles = []string{dbPath}
	tx, err := BeginUpdateTransaction(ctx, root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	for _, step := range []func() error{tx.BeginDrain, func() error { return tx.ConfirmStopped(ctx) }, func() error { return tx.PrepareSnapshots(ctx) }} {
		if err = step(); err != nil {
			t.Fatal(err)
		}
	}
	manifest := readSnapshotManifest(t, root, plan, tx)
	for _, copyRoot := range []string{manifest.Units[0].BackupPath, manifest.Units[0].ReadyPath} {
		if _, err = os.Stat(filepath.Join(copyRoot, "process.pid")); !os.IsNotExist(err) {
			t.Fatal("ephemeral process identity backed up for restoration")
		}
		if _, err = os.Stat(filepath.Join(copyRoot, "state.db-wal")); err != nil {
			t.Fatal("WAL omitted from cold set")
		}
	}
	if err = tx.MarkInstallHandoff(); err != nil {
		t.Fatal(err)
	}
	if err = tx.MarkCandidateValidation(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO records VALUES(999)"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{tx.BeginRecovery, func() error { return tx.MarkSourceRestored(plan.SourceID, plan.SourceManifestSHA256) }, func() error { return tx.RestoreData(ctx) }} {
		if err = step(); err != nil {
			t.Fatal(err)
		}
	}
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count, total int
	if err = db.QueryRow("SELECT count(*),sum(value) FROM records").Scan(&count, &total); err != nil || count != 1 || total != 123 {
		t.Fatalf("restored WAL data missing: count=%d total=%d err=%v", count, total, err)
	}
	if _, err = os.Stat(filepath.Join(plan.Units[0].Path, "process.pid")); !os.IsNotExist(err) {
		t.Fatal("stale PID restored")
	}
}

func TestUpdateSnapshotPreservesFilesAndOriginalAbsence(t *testing.T) {
	root, plan := transactionFixture(t)
	settings := filepath.Join(plan.Scope.DataRoots[0], "settings.json")
	missing := filepath.Join(plan.Scope.DataRoots[0], "not-created.json")
	if err := os.WriteFile(settings, []byte("source settings"), 0600); err != nil {
		t.Fatal(err)
	}
	plan.Units = []SnapshotUnit{{Path: settings}, {Path: missing}}
	tx, err := BeginUpdateTransaction(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	for _, step := range []func() error{tx.BeginDrain, func() error { return tx.ConfirmStopped(context.Background()) }, func() error { return tx.PrepareSnapshots(context.Background()) }, tx.MarkInstallHandoff, tx.MarkCandidateValidation} {
		if err = step(); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{settings, missing} {
		if err = os.WriteFile(file, []byte("failed candidate value"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []func() error{tx.BeginRecovery, func() error { return tx.MarkSourceRestored(plan.SourceID, plan.SourceManifestSHA256) }, func() error { return tx.RestoreData(context.Background()) }} {
		if err = step(); err != nil {
			t.Fatal(err)
		}
	}
	if raw, err := os.ReadFile(settings); err != nil || string(raw) != "source settings" {
		t.Fatalf("settings lost: %v", err)
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("original absence not restored")
	}
}
