package desktopupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func readSnapshotManifest(t *testing.T, root string, plan TransactionPlan, tx *UpdateTransaction) SnapshotManifest {
	t.Helper()
	canonical := func(path string) string {
		t.Helper()
		resolved, err := filepath.EvalSymlinks(path)
		if os.IsNotExist(err) {
			parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
			if parentErr != nil {
				t.Fatal(parentErr)
			}
			return filepath.Join(parent, filepath.Base(path))
		}
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	id := tx.Journal().ID
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) {
		t.Fatal("invalid transaction id")
	}
	expectedBackup := filepath.Join(canonical(root), "transactions", "v1", id, "snapshot")
	if tx.Journal().SnapshotPath != expectedBackup {
		t.Fatal("snapshot escaped fixture recovery root")
	}
	raw, err := os.ReadFile(filepath.Join(tx.Journal().SnapshotPath, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest SnapshotManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 || !manifest.Complete || manifest.TransactionID != tx.Journal().ID {
		t.Fatal("snapshot completion not bound to transaction")
	}
	if len(manifest.Units) != len(plan.Units) || len(manifest.Reservations) != len(plan.Reservations) {
		t.Fatal("snapshot omitted units or native-space reservations")
	}
	for i, unit := range manifest.Units {
		active := canonical(plan.Units[i].Path)
		container := filepath.Join(filepath.Dir(active), ".lazymind-update-"+id+"-"+strconv.Itoa(i))
		if unit.ActivePath != active || unit.BackupPath != filepath.Join(expectedBackup, "units", strconv.Itoa(i)) || unit.ReadyPath != filepath.Join(container, "ready") || unit.FailedPath != filepath.Join(container, "failed") {
			t.Fatal("snapshot paths not bound to the supplied temporary fixture")
		}
	}
	for i, reservation := range manifest.Reservations {
		container := filepath.Join(canonical(plan.Reservations[i].Directory), ".lazymind-update-"+id+"-budget-"+strconv.Itoa(i))
		if reservation.InstallPath != filepath.Join(container, "install.reserve") || reservation.RecoveryPath != filepath.Join(container, "recovery.reserve") || reservation.InstallBytes != plan.Reservations[i].InstallBytes || reservation.RecoveryBytes != plan.Reservations[i].RecoveryBytes {
			t.Fatal("native reservation not bound to plan")
		}
	}
	return manifest
}

func snapshotFixture(t *testing.T) (string, TransactionPlan, *UpdateTransaction) {
	t.Helper()
	root, plan, tx := beginTransactionFixture(t)
	if err := tx.BeginDrain(); err != nil {
		t.Fatal(err)
	}
	if err := tx.ConfirmStopped(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tx.PrepareSnapshots(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tx.Journal().Stage != "SNAPSHOT_READY" || tx.Journal().SnapshotPath == "" {
		t.Fatal("cold snapshot not made durable")
	}
	return root, plan, tx
}

func TestUpdateSnapshotKeepsOriginalBackupAndRestoresPreparedCopy(t *testing.T) {
	root, plan, tx := snapshotFixture(t)
	backup := tx.Journal().SnapshotPath
	manifest := readSnapshotManifest(t, root, plan, tx)
	if len(manifest.Units) != 1 {
		t.Fatal("snapshot unit missing")
	}
	ready, err := os.ReadFile(filepath.Join(manifest.Units[0].ReadyPath, "records.txt"))
	if err != nil || string(ready) != "source data" {
		t.Fatalf("same-volume recovery copy C not prepared: %q %v", ready, err)
	}
	before, err := os.ReadFile(filepath.Join(backup, "units", "0", "records.txt"))
	if err != nil || string(before) != "source data" {
		t.Fatalf("invalid original backup: %q %v", before, err)
	}
	if err = tx.MarkInstallHandoff(); err != nil {
		t.Fatal(err)
	}
	if err = tx.MarkCandidateValidation(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(plan.Units[0].Path, "records.txt"), []byte("failed migrated data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = tx.BeginRecovery(); err != nil {
		t.Fatal(err)
	}
	if err = tx.RestoreData(context.Background()); ErrorCode(err) != "transaction_stage" {
		t.Fatalf("data restored before application: %v", err)
	}
	if err = tx.MarkSourceRestored("wrong-source", plan.SourceManifestSHA256); err == nil {
		t.Fatal("wrong source restoration accepted")
	}
	if err = tx.MarkSourceRestored(plan.SourceID, plan.SourceManifestSHA256); err != nil {
		t.Fatal(err)
	}
	if err = tx.RestoreData(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(plan.Units[0].Path, "records.txt"))
	if err != nil || string(got) != "source data" {
		t.Fatalf("source data not restored: %q %v", got, err)
	}
	after, err := os.ReadFile(filepath.Join(backup, "units", "0", "records.txt"))
	if err != nil || string(after) != string(before) {
		t.Fatal("original B consumed during restore")
	}
	if err = CheckStartup(context.Background(), root, plan.Scope); ErrorCode(err) != "update_in_progress" {
		t.Fatal("business opened before source health acceptance")
	}
	if err = tx.CompleteRecovery(plan.SourceID, plan.DataProfile); err != nil {
		t.Fatal(err)
	}
	if err = CheckStartup(context.Background(), root, plan.Scope); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateSnapshotRejectsDamagedMaterialAndInsufficientReserve(t *testing.T) {
	for _, kind := range []string{"backup", "ready", "manifest", "space"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "space" {
				root, plan := transactionFixture(t)
				plan.Reservations[0].RecoveryBytes = 1 << 50
				tx, err := BeginUpdateTransaction(context.Background(), root, plan)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Close()
				if err = tx.BeginDrain(); err != nil {
					t.Fatal(err)
				}
				if err = tx.ConfirmStopped(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err = tx.PrepareSnapshots(context.Background()); ErrorCode(err) != "space_insufficient" {
					t.Fatalf("recovery peak ignored: %v", err)
				}
				if tx.Journal().Stage == "SNAPSHOT_READY" {
					t.Fatal("space failure accepted")
				}
				return
			}
			root, plan, tx := snapshotFixture(t)
			manifest := readSnapshotManifest(t, root, plan, tx)
			switch kind {
			case "backup":
				file := filepath.Join(manifest.Units[0].BackupPath, "records.txt")
				if err := os.Chmod(file, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("damaged"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ready":
				if err := os.WriteFile(filepath.Join(manifest.Units[0].ReadyPath, "records.txt"), []byte("damaged"), 0600); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				manifest.TransactionID = "another-transaction"
				raw, _ := json.Marshal(manifest)
				if err := os.WriteFile(filepath.Join(tx.Journal().SnapshotPath, "manifest.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.MarkInstallHandoff(); err == nil {
				t.Fatal("damaged restore material accepted for install")
			}
		})
	}
}

func twoUnitRecoveryFixture(t *testing.T) (string, TransactionPlan, *UpdateTransaction, SnapshotManifest) {
	t.Helper()
	root, plan := transactionFixture(t)
	second := filepath.Join(filepath.Dir(plan.Units[0].Path), "extra-data")
	if err := os.MkdirAll(second, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "other.txt"), []byte("other source"), 0600); err != nil {
		t.Fatal(err)
	}
	plan.Scope.DataRoots = append(plan.Scope.DataRoots, second)
	plan.Units = append(plan.Units, SnapshotUnit{Path: second})
	tx, err := BeginUpdateTransaction(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Close() })
	for _, step := range []func() error{tx.BeginDrain, func() error { return tx.ConfirmStopped(context.Background()) }, func() error { return tx.PrepareSnapshots(context.Background()) }, tx.MarkInstallHandoff, tx.MarkCandidateValidation, tx.BeginRecovery, func() error { return tx.MarkSourceRestored(plan.SourceID, plan.SourceManifestSHA256) }} {
		if err = step(); err != nil {
			t.Fatal(err)
		}
	}
	manifest := readSnapshotManifest(t, root, plan, tx)
	if len(manifest.Units) != 2 {
		t.Fatal("second unit omitted")
	}
	for _, unit := range manifest.Units {
		if err = os.WriteFile(filepath.Join(unit.ActivePath, "failed.txt"), []byte("failed candidate"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root, plan, tx, manifest
}

func TestUpdateRestoreReconcilesInterruptionBetweenUnitRenames(t *testing.T) {
	for _, window := range []string{"after-save-active", "after-activate-ready"} {
		t.Run(window, func(t *testing.T) {
			root, plan, tx, manifest := twoUnitRecoveryFixture(t)
			first := manifest.Units[0]
			if err := os.Rename(first.ActivePath, first.FailedPath); err != nil {
				t.Fatal(err)
			}
			if window == "after-activate-ready" {
				if err := os.Rename(first.ReadyPath, first.ActivePath); err != nil {
					t.Fatal(err)
				}
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
			for i, unit := range manifest.Units {
				name, want := "records.txt", "source data"
				if i == 1 {
					name, want = "other.txt", "other source"
				}
				for _, dir := range []string{unit.ActivePath, unit.BackupPath} {
					if content, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(content) != want {
						t.Fatalf("source or immutable B lost: %q %v", content, err)
					}
				}
				if _, err = os.Stat(filepath.Join(unit.ActivePath, "failed.txt")); !os.IsNotExist(err) {
					t.Fatal("failed active state survived restoration")
				}
				if content, err := os.ReadFile(filepath.Join(unit.FailedPath, "failed.txt")); err != nil || string(content) != "failed candidate" {
					t.Fatal("failed evidence overwritten")
				}
			}
			if err = tx.RestoreData(context.Background()); err != nil {
				t.Fatalf("completed restore not safely reentrant: %v", err)
			}
			if err = CheckStartup(context.Background(), root, plan.Scope); ErrorCode(err) != "update_in_progress" {
				t.Fatalf("recovered data reopened business before health acceptance: %v", err)
			}
		})
	}
}

func TestUpdateRestoreRechecksAllMaterialsBeforeSwitchingAnyUnit(t *testing.T) {
	for _, damage := range []string{"ready", "manifest"} {
		t.Run(damage, func(t *testing.T) {
			root, plan, tx, manifest := twoUnitRecoveryFixture(t)
			// Preserve a real interrupted-restore failure directory, while the
			// second unit is still the candidate's active data.
			first, second := manifest.Units[0], manifest.Units[1]
			if err := os.Rename(first.ActivePath, first.FailedPath); err != nil {
				t.Fatal(err)
			}
			if damage == "ready" {
				if err := os.WriteFile(filepath.Join(second.ReadyPath, "other.txt"), []byte("damaged after handoff"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				manifest.TransactionID = "another-transaction"
				raw, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(tx.Journal().SnapshotPath, "manifest.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.RestoreData(context.Background()); err == nil {
				t.Fatal("post-handoff corrupt recovery material accepted")
			}
			// The first unit could be activated successfully in isolation, but
			// must remain untouched until every unit has passed verification.
			if _, err := os.Lstat(first.ActivePath); !os.IsNotExist(err) {
				t.Fatal("first C activated before validating all recovery material")
			}
			for _, file := range []string{filepath.Join(first.FailedPath, "failed.txt"), filepath.Join(second.ActivePath, "failed.txt")} {
				if content, err := os.ReadFile(file); err != nil || string(content) != "failed candidate" {
					t.Fatal("existing failed evidence or active candidate changed")
				}
			}
			for i, unit := range manifest.Units {
				name, want := "records.txt", "source data"
				if i == 1 {
					name, want = "other.txt", "other source"
				}
				if content, err := os.ReadFile(filepath.Join(unit.BackupPath, name)); err != nil || string(content) != want {
					t.Fatal("immutable B changed on rejected restore")
				}
			}
			if tx.Journal().Stage == "RECOVERY_VALIDATING" || tx.Journal().Stage == "RECOVERED" {
				t.Fatal("invalid restore recorded as successful")
			}
			if err := CheckStartup(context.Background(), root, plan.Scope); ErrorCode(err) != "update_in_progress" {
				t.Fatalf("corrupt recovery reopened business: %v", err)
			}
		})
	}
}

func TestUpdateSnapshotDoesNotRollbackCommittedUserData(t *testing.T) {
	root, plan, tx := snapshotFixture(t)
	if err := tx.MarkInstallHandoff(); err != nil {
		t.Fatal(err)
	}
	if err := tx.MarkCandidateValidation(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit("another-release", plan.DataProfile); err == nil {
		t.Fatal("wrong candidate committed")
	}
	if err := tx.Commit(plan.TargetID, plan.DataProfile); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plan.Units[0].Path, "records.txt"), []byte("new user data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := tx.BeginRecovery(); ErrorCode(err) != "transaction_stage" {
		t.Fatal("post-commit rollback admitted")
	}
	if err := tx.RestoreData(context.Background()); ErrorCode(err) != "transaction_stage" {
		t.Fatal("post-commit data overwritten")
	}
	if err := CheckStartup(context.Background(), root, plan.Scope); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(plan.Units[0].Path, "records.txt"))
	if string(got) != "new user data" {
		t.Fatal("committed data lost")
	}
}

func TestUpdateSnapshotRejectsHardLinksAndCancellationBeforeHandoff(t *testing.T) {
	for _, kind := range []string{"hardlink", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			_, plan, tx := beginTransactionFixture(t)
			if err := tx.BeginDrain(); err != nil {
				t.Fatal(err)
			}
			if err := tx.ConfirmStopped(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if kind == "hardlink" {
				if err := os.Link(filepath.Join(plan.Units[0].Path, "records.txt"), filepath.Join(plan.Units[0].Path, "shared-records.txt")); err != nil {
					t.Fatal(err)
				}
			} else {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := tx.PrepareSnapshots(ctx); err == nil {
				t.Fatal("unsafe snapshot accepted")
			}
			if tx.Journal().Stage == "SNAPSHOT_READY" {
				t.Fatal("partial snapshot became installable")
			}
			got, _ := os.ReadFile(filepath.Join(plan.Units[0].Path, "records.txt"))
			if string(got) != "source data" {
				t.Fatal("preparation changed active data")
			}
		})
	}
}

func TestUpdateSnapshotPreservesSymbolicLinksWithoutCopyingExternalData(t *testing.T) {
	root, plan := transactionFixture(t)
	external := t.TempDir()
	link := filepath.Join(plan.Units[0].Path, "workspace-link")
	if err := os.WriteFile(filepath.Join(external, "external.txt"), []byte("external data must stay outside backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	tx, err := BeginUpdateTransaction(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	for _, step := range []func() error{tx.BeginDrain, func() error { return tx.ConfirmStopped(context.Background()) }, func() error { return tx.PrepareSnapshots(context.Background()) }} {
		if err = step(); err != nil {
			t.Fatal(err)
		}
	}
	manifest := readSnapshotManifest(t, root, plan, tx)
	for _, copyRoot := range []string{manifest.Units[0].BackupPath, manifest.Units[0].ReadyPath} {
		if target, err := os.Readlink(filepath.Join(copyRoot, "workspace-link")); err != nil || target != external {
			t.Fatalf("link was followed or lost: %q %v", target, err)
		}
	}
	if content, err := os.ReadFile(filepath.Join(external, "external.txt")); err != nil || string(content) != "external data must stay outside backup" {
		t.Fatal("external workspace changed")
	}
}
