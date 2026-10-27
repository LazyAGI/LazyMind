package desktopupdate

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateSnapshotSQLiteDriveLetterIsNotAURIHost(t *testing.T) {
	address, err := url.Parse(snapshotSQLiteURI("C:/Users/fixture/state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if address.Host != "" || address.Path != "/C:/Users/fixture/state.db" || address.Query().Get("mode") != "ro" {
		t.Fatalf("Windows local path became a URI authority: %s", address)
	}
}

func TestUpdateSnapshotCannotHandoffAfterReservationIsLost(t *testing.T) {
	for _, kind := range []string{"missing", "sparse"} {
		t.Run(kind, func(t *testing.T) {
			root, plan, tx := snapshotFixture(t)
			m := readSnapshotManifest(t, root, plan, tx)
			path := m.Reservations[0].RecoveryPath
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if kind == "sparse" {
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.Truncate(m.Reservations[0].RecoveryBytes); err != nil {
					t.Fatal(err)
				}
				if err = f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.MarkInstallHandoff(); err == nil {
				t.Fatal("lost physical reserve still allows destructive handoff")
			}
		})
	}
}

func TestUpdateSnapshotTerminalStagesReleaseSpaceReservations(t *testing.T) {
	for _, kind := range []string{"abort", "commit"} {
		t.Run(kind, func(t *testing.T) {
			root, plan, tx := snapshotFixture(t)
			m := readSnapshotManifest(t, root, plan, tx)
			if kind == "abort" {
				if err := tx.AbortBeforeInstall(); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, step := range []func() error{tx.MarkInstallHandoff, tx.MarkCandidateValidation, func() error { return tx.Commit(plan.TargetID, plan.TargetDataProfile) }} {
					if err := step(); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, r := range m.Reservations {
				for _, path := range []string{r.InstallPath, r.RecoveryPath} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatal("terminal transaction retains native space reservation")
					}
				}
			}
		})
	}
}

func TestUpdateRestoreCanReenterWhenCandidateDeletedDataUnit(t *testing.T) {
	root, plan, tx := snapshotFixture(t)
	m := readSnapshotManifest(t, root, plan, tx)
	for _, step := range []func() error{tx.MarkInstallHandoff, tx.MarkCandidateValidation, tx.BeginRecovery, func() error { return tx.MarkSourceRestored(plan.SourceID, plan.SourceManifestSHA256) }} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(m.Units[0].ActivePath); err != nil {
		t.Fatal(err)
	}
	if err := tx.RestoreData(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := tx.Journal().ID
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	tx, err := ResumeUpdateTransaction(context.Background(), root, id)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if err = tx.RestoreData(context.Background()); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(m.Units[0].ActivePath, "records.txt")); err != nil || string(raw) != "source data" {
		t.Fatal("missing candidate unit not restored")
	}
}

func TestUpdatePlanCannotOmitSQLiteWALOrMutateThroughCaller(t *testing.T) {
	root, fileOnly := transactionFixture(t)
	database := filepath.Join(fileOnly.Units[0].Path, "state.db")
	fileOnly.SQLiteFiles = []string{database}
	fileOnly.Units = []SnapshotUnit{{Path: database}}
	if tx, err := BeginUpdateTransaction(context.Background(), root, fileOnly); err == nil {
		tx.Close()
		t.Fatal("SQLite main-only unit omitted possible WAL")
	}
	for _, exclude := range []string{"state.db", "state.db-wal"} {
		root, plan := transactionFixture(t)
		plan.SQLiteFiles = []string{filepath.Join(plan.Units[0].Path, "state.db")}
		plan.Units[0].Exclude = []string{exclude}
		if tx, err := BeginUpdateTransaction(context.Background(), root, plan); err == nil {
			tx.Close()
			t.Fatal("SQLite durable file excluded")
		}
	}
	_, plan, tx := beginTransactionFixture(t)
	source := tx.Journal().Plan.Units[0].Path
	plan.Units[0].Path = "changed"
	plan.Reservations[0].Directory = "changed"
	if tx.Journal().Plan.Units[0].Path != source || tx.Journal().Plan.Reservations[0].Directory == "changed" {
		t.Fatal("caller mutated transaction")
	}
}

func TestUpdateCommittedHistoryDoesNotFenceLaterUserPathChanges(t *testing.T) {
	root, plan, tx := snapshotFixture(t)
	for _, step := range []func() error{tx.MarkInstallHandoff, tx.MarkCandidateValidation, func() error { return tx.Commit(plan.TargetID, plan.TargetDataProfile) }} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(plan.Scope.DataRoots[0], plan.Scope.DataRoots[0]+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), plan.Scope.DataRoots[0]); err != nil {
		t.Fatal(err)
	}
	if err := CheckStartup(context.Background(), root, plan.Scope); err != nil {
		t.Fatalf("finished transaction revalidated obsolete data binding: %v", err)
	}
}
