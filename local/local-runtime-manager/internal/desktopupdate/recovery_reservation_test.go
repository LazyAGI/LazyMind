package desktopupdate

import (
	"context"
	"os"
	"testing"
)

func TestUpdateRecoveryRevalidatesPhysicalReserveBeforeFirstEntry(t *testing.T) {
	for _, damage := range []string{"missing", "sparse", "short"} {
		t.Run(damage, func(t *testing.T) {
			root, plan, tx := snapshotFixture(t)
			m := readSnapshotManifest(t, root, plan, tx)
			if err := tx.MarkInstallHandoff(); err != nil {
				t.Fatal(err)
			}
			path := m.Reservations[0].RecoveryPath
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if damage != "missing" {
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				size := m.Reservations[0].RecoveryBytes
				if damage == "short" {
					size = 1
				}
				if err = f.Truncate(size); err != nil {
					t.Fatal(err)
				}
				if err = f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.BeginRecovery(); err == nil {
				t.Fatal("invalid recovery reserve admitted")
			}
			if tx.Journal().Stage != "INSTALL_HANDOFF" {
				t.Fatal("failed reserve validation advanced journal")
			}
			if err := CheckStartup(context.Background(), root, plan.Scope); ErrorCode(err) != "update_in_progress" {
				t.Fatal("damaged reserve opened business")
			}
		})
	}
}

func TestUpdateRecoveryReserveReleaseCanResumeAfterRecordedIntent(t *testing.T) {
	root, plan, tx := snapshotFixture(t)
	m := readSnapshotManifest(t, root, plan, tx)
	if err := tx.MarkInstallHandoff(); err != nil {
		t.Fatal(err)
	}
	if err := tx.BeginRecovery(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Reservations[0].RecoveryPath); !os.IsNotExist(err) {
		t.Fatal("reserve not released")
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
	if err = tx.BeginRecovery(); err != nil {
		t.Fatalf("already released reserve prevented resume: %v", err)
	}
	if tx.Journal().Stage != "RECOVERING_SOURCE" {
		t.Fatal("resume changed phase")
	}
}
