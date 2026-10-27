package desktopupdate

import (
	"os"
	"testing"
)

func TestUpdateSnapshotPhysicallyReservesInstallationAndRecoverySpace(t *testing.T) {
	root, plan, tx := snapshotFixture(t)
	manifest := readSnapshotManifest(t, root, plan, tx)
	for _, reservation := range manifest.Reservations {
		for path, required := range map[string]int64{reservation.InstallPath: reservation.InstallBytes, reservation.RecoveryPath: reservation.RecoveryBytes} {
			info, err := os.Stat(path)
			if err != nil || info.Size() != required {
				t.Fatalf("reservation missing: %v", err)
			}
			if allocated := fixtureAllocatedBytes(t, path); allocated < required {
				t.Fatalf("sparse placeholder: allocated=%d required=%d", allocated, required)
			}
		}
	}
	if err := tx.MarkInstallHandoff(); err != nil {
		t.Fatal(err)
	}
	for _, reservation := range manifest.Reservations {
		if _, err := os.Stat(reservation.InstallPath); !os.IsNotExist(err) {
			t.Fatal("installation budget not released at handoff")
		}
		if _, err := os.Stat(reservation.RecoveryPath); err != nil {
			t.Fatal("source recovery budget released before recovery")
		}
	}
	if err := tx.MarkCandidateValidation(); err != nil {
		t.Fatal(err)
	}
	if err := tx.BeginRecovery(); err != nil {
		t.Fatal(err)
	}
	for _, reservation := range manifest.Reservations {
		if _, err := os.Stat(reservation.RecoveryPath); !os.IsNotExist(err) {
			t.Fatal("recovery budget not released for source installer")
		}
	}
}
