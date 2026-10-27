package desktopupdate

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	_ "github.com/glebarez/go-sqlite"
)

func (tx *UpdateTransaction) resolvedPlan() (TransactionPlan, error) {
	plan, err := normalizePlan(tx.root, tx.journal.Plan)
	if err != nil || !reflect.DeepEqual(plan.Scope, tx.journal.ResolvedScope) {
		return plan, failure("scope_changed", err)
	}
	return plan, nil
}

func (tx *UpdateTransaction) expectedSnapshot() (SnapshotManifest, error) {
	j := tx.journal
	plan, err := tx.resolvedPlan()
	if err != nil {
		return SnapshotManifest{}, err
	}
	base := filepath.Join(tx.root, "transactions", "v1", j.ID, "snapshot")
	m := SnapshotManifest{Version: 1, TransactionID: j.ID, Complete: true}
	for i, unit := range plan.Units {
		container := filepath.Join(filepath.Dir(unit.Path), ".lazymind-update-"+j.ID+"-"+strconv.Itoa(i))
		m.Units = append(m.Units, SnapshotRecord{ActivePath: unit.Path, BackupPath: filepath.Join(base, "units", strconv.Itoa(i)), ReadyPath: filepath.Join(container, "ready"), FailedPath: filepath.Join(container, "failed")})
	}
	for i, r := range plan.Reservations {
		container := filepath.Join(r.Directory, ".lazymind-update-"+j.ID+"-budget-"+strconv.Itoa(i))
		m.Reservations = append(m.Reservations, SnapshotReservation{InstallPath: filepath.Join(container, "install.reserve"), RecoveryPath: filepath.Join(container, "recovery.reserve"), InstallBytes: r.InstallBytes, RecoveryBytes: r.RecoveryBytes})
	}
	return m, nil
}

func snapshotExcludes(unit SnapshotUnit, sqliteFiles []string) []string {
	excludes := append([]string{}, unit.Exclude...)
	for _, db := range sqliteFiles {
		if within(unit.Path, db) {
			rel, err := filepath.Rel(unit.Path, db+"-shm")
			if err == nil {
				excludes = append(excludes, rel)
			}
		}
	}
	return excludes
}

func (tx *UpdateTransaction) PrepareSnapshots(ctx context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("STOPPED"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.persist("SNAPSHOT_PREPARING"); err != nil {
		return err
	}
	m, err := tx.expectedSnapshot()
	if err != nil {
		return err
	}
	plan, err := tx.resolvedPlan()
	if err != nil {
		return err
	}
	base := filepath.Join(tx.root, "transactions", "v1", tx.journal.ID, "snapshot")
	if err := ensureDirectory(filepath.Join(base, "units")); err != nil {
		return err
	}
	for _, reservation := range m.Reservations {
		if err := ensureDirectory(filepath.Dir(reservation.InstallPath)); err != nil {
			return err
		}
		if err := reserveSpace(ctx, reservation.InstallPath, reservation.InstallBytes); err != nil {
			return err
		}
		if err := reserveSpace(ctx, reservation.RecoveryPath, reservation.RecoveryBytes); err != nil {
			return err
		}
	}
	for i, unit := range plan.Units {
		record := &m.Units[i]
		excludes := snapshotExcludes(unit, plan.SQLiteFiles)
		digest, exists, err := treeDigest(ctx, unit.Path, excludes)
		if err != nil {
			return err
		}
		record.Digest, record.Existed = digest, exists
		if err = ensureDirectory(filepath.Dir(record.ReadyPath)); err != nil {
			return err
		}
		if err = copySnapshot(ctx, unit.Path, record.BackupPath, excludes); err != nil {
			return err
		}
		if err = copySnapshot(ctx, record.BackupPath, record.ReadyPath, nil); err != nil {
			return err
		}
		for _, path := range []string{unit.Path, record.BackupPath, record.ReadyPath} {
			actual, present, err := treeDigest(ctx, path, excludes)
			if err != nil || actual != digest || present != exists {
				return failure("snapshot_changed", err)
			}
		}
	}
	// Inspect the disposable same-volume copy, never checkpoint the original B.
	if err := tx.checkSnapshotSQLite(ctx, m); err != nil {
		return err
	}
	for _, record := range m.Units {
		digest, exists, err := treeDigest(ctx, record.ReadyPath, nil)
		if err != nil || digest != record.Digest || exists != record.Existed {
			return failure("snapshot_changed", err)
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(base, "manifest.json"), raw); err != nil {
		return err
	}
	tx.journal.SnapshotPath = base
	tx.journal.SnapshotSHA256 = digestBytes(raw)
	return tx.persist("SNAPSHOT_READY")
}

func snapshotSQLiteURI(path string) string {
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}
	return uri.String()
}

func (tx *UpdateTransaction) checkSnapshotSQLite(ctx context.Context, m SnapshotManifest) error {
	plan, err := tx.resolvedPlan()
	if err != nil {
		return err
	}
	for _, file := range plan.SQLiteFiles {
		for _, record := range m.Units {
			if !within(record.ActivePath, file) {
				continue
			}
			rel, err := filepath.Rel(record.ActivePath, file)
			if err != nil {
				return err
			}
			copied := filepath.Join(record.ReadyPath, rel)
			if _, err = os.Stat(copied); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return err
			}
			db, err := sql.Open("sqlite", snapshotSQLiteURI(copied))
			if err != nil {
				return failure("snapshot_sqlite", err)
			}
			db.SetMaxOpenConns(1)
			var result string
			err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result)
			closeErr := db.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil || result != "ok" {
				return failure("snapshot_sqlite", err)
			}
			// WAL is durable data; SHM is recreated coordination state.
			if err = os.Remove(copied + "-shm"); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (tx *UpdateTransaction) loadSnapshot() (SnapshotManifest, error) {
	var m SnapshotManifest
	if tx.journal.SnapshotPath == "" {
		return m, failure("snapshot_missing", nil)
	}
	if err := readMetadata(filepath.Join(tx.journal.SnapshotPath, "manifest.json"), &m); err != nil {
		return m, failure("snapshot_corrupt", err)
	}
	raw, err := json.Marshal(m)
	if err != nil || digestBytes(raw) != tx.journal.SnapshotSHA256 {
		return m, failure("snapshot_corrupt", err)
	}
	expected, err := tx.expectedSnapshot()
	if err != nil {
		return m, err
	}
	if m.Version != 1 || m.TransactionID != tx.journal.ID || !m.Complete || len(m.Units) != len(expected.Units) || len(m.Reservations) != len(expected.Reservations) {
		return m, failure("snapshot_corrupt", nil)
	}
	for i, r := range m.Units {
		want := expected.Units[i]
		if r.ActivePath != want.ActivePath || r.BackupPath != want.BackupPath || r.ReadyPath != want.ReadyPath || r.FailedPath != want.FailedPath || !digestPattern.MatchString(r.Digest) {
			return m, failure("snapshot_corrupt", nil)
		}
	}
	for i, r := range m.Reservations {
		if r != expected.Reservations[i] {
			return m, failure("snapshot_corrupt", nil)
		}
	}
	return m, nil
}

func verifySnapshotTree(ctx context.Context, path string, record SnapshotRecord) error {
	digest, exists, err := treeDigest(ctx, path, nil)
	if err != nil || digest != record.Digest || exists != record.Existed {
		return failure("snapshot_corrupt", err)
	}
	return nil
}

func removeReservation(path string) error {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func (tx *UpdateTransaction) releaseReservations() error {
	m, err := tx.expectedSnapshot()
	if err != nil {
		return err
	}
	for _, r := range m.Reservations {
		for _, path := range []string{r.InstallPath, r.RecoveryPath} {
			if err = removeReservation(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifyReservation(path string, size int64) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return failure("space_reservation", err)
	}
	allocated, err := allocatedDiskBytes(path)
	if err != nil || allocated < uint64(size) {
		return failure("space_reservation", err)
	}
	return nil
}

func (tx *UpdateTransaction) MarkInstallHandoff() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("SNAPSHOT_READY"); err != nil {
		return err
	}
	m, err := tx.loadSnapshot()
	if err != nil {
		return err
	}
	for _, record := range m.Units {
		for _, path := range []string{record.BackupPath, record.ReadyPath} {
			if err = verifySnapshotTree(context.Background(), path, record); err != nil {
				return err
			}
		}
	}
	for _, r := range m.Reservations {
		if err = verifyReservation(r.InstallPath, r.InstallBytes); err != nil {
			return err
		}
		if err = verifyReservation(r.RecoveryPath, r.RecoveryBytes); err != nil {
			return err
		}
	}
	// Write intent first. A crash while releasing reserves is an uncertain
	// handoff requiring recovery, never permission to launch a second installer.
	if err = tx.persist("INSTALL_HANDOFF"); err != nil {
		return err
	}
	for _, r := range m.Reservations {
		if err = removeReservation(r.InstallPath); err != nil {
			return err
		}
	}
	return nil
}

func (tx *UpdateTransaction) BeginRecovery() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("INSTALL_HANDOFF", "CANDIDATE_VALIDATING", "RECOVERING_SOURCE"); err != nil {
		return err
	}
	m, err := tx.loadSnapshot()
	if err != nil {
		return err
	}
	if tx.journal.Stage != "RECOVERING_SOURCE" {
		for _, r := range m.Reservations {
			if err = verifyReservation(r.RecoveryPath, r.RecoveryBytes); err != nil {
				return err
			}
		}
		if err = tx.persist("RECOVERING_SOURCE"); err != nil {
			return err
		}
	}
	for _, r := range m.Reservations {
		if err = removeReservation(r.RecoveryPath); err != nil {
			return err
		}
	}
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

type failedAbsence struct {
	TransactionID string `json:"transactionId"`
	ActivePath    string `json:"activePath"`
}

func (tx *UpdateTransaction) wasActiveAbsent(record SnapshotRecord) (bool, error) {
	path := filepath.Join(filepath.Dir(record.FailedPath), "failed-absent.json")
	exists, err := pathExists(path)
	if err != nil || !exists {
		return false, err
	}
	var marker failedAbsence
	if err = readMetadata(path, &marker); err != nil {
		return false, failure("snapshot_corrupt", err)
	}
	if marker.TransactionID != tx.journal.ID || marker.ActivePath != record.ActivePath {
		return false, failure("snapshot_corrupt", nil)
	}
	return true, nil
}

func (tx *UpdateTransaction) RestoreData(ctx context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("SOURCE_RESTORED", "RESTORING_DATA", "RECOVERY_VALIDATING"); err != nil {
		return err
	}
	m, err := tx.loadSnapshot()
	if err != nil {
		return err
	}
	// Validate every B and every as-yet-unused C before the first rename.
	// Presence plus digests reconciles a crash on either side of each rename.
	for _, record := range m.Units {
		if err = verifySnapshotTree(ctx, record.BackupPath, record); err != nil {
			return err
		}
		ready, err := pathExists(record.ReadyPath)
		if err != nil {
			return err
		}
		active, err := pathExists(record.ActivePath)
		if err != nil {
			return err
		}
		failed, err := pathExists(record.FailedPath)
		if err != nil {
			return err
		}
		absent, err := tx.wasActiveAbsent(record)
		if err != nil {
			return err
		}
		if absent && failed {
			return failure("restore_conflict", nil)
		}
		if record.Existed {
			if ready {
				if active && (failed || absent) {
					return failure("restore_conflict", nil)
				}
				if err = verifySnapshotTree(ctx, record.ReadyPath, record); err != nil {
					return err
				}
			} else {
				if !active || (!failed && !absent) {
					return failure("snapshot_corrupt", nil)
				}
				if err = verifySnapshotTree(ctx, record.ActivePath, record); err != nil {
					return err
				}
			}
		} else if ready || (active && (failed || absent)) {
			return failure("restore_conflict", nil)
		}
	}
	if tx.journal.Stage == "RECOVERY_VALIDATING" {
		return nil
	}
	if err = tx.persist("RESTORING_DATA"); err != nil {
		return err
	}
	for _, record := range m.Units {
		if err = ctx.Err(); err != nil {
			return err
		}
		active, err := pathExists(record.ActivePath)
		if err != nil {
			return err
		}
		failed, err := pathExists(record.FailedPath)
		if err != nil {
			return err
		}
		if active && !failed {
			absent, err := tx.wasActiveAbsent(record)
			if err != nil {
				return err
			}
			if absent {
				continue
			} // C already activated after a recorded absence.
			if err = renameData(record.ActivePath, record.FailedPath); err != nil {
				return err
			}
			active = false
			failed = true
		}
		if !active && !failed {
			raw, err := json.Marshal(failedAbsence{tx.journal.ID, record.ActivePath})
			if err != nil {
				return err
			}
			if err = atomicWrite(filepath.Join(filepath.Dir(record.FailedPath), "failed-absent.json"), raw); err != nil {
				return err
			}
		}
		if record.Existed && !active {
			if err = renameData(record.ReadyPath, record.ActivePath); err != nil {
				return err
			}
		}
	}
	return tx.persist("RECOVERY_VALIDATING")
}

func renameData(source, destination string) error {
	if exists, err := pathExists(destination); err != nil || exists {
		return failure("restore_conflict", err)
	}
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(source)); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}
