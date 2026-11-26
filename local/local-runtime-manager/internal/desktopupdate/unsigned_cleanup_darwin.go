//go:build darwin && lazymind_unsigned_test

package desktopupdate

import (
	"context"
	"os"
	"path/filepath"
)

// ReleaseCompletedUnsignedInstall is best-effort after the runtime has
// verified the target and released the update seal. Later starts can retry
// cleanup using the durable completion record.
func ReleaseCompletedUnsignedInstall(ctx context.Context, root, tx string) {
	p, err := UnsignedPaths()
	if err != nil || root != p.Updater {
		return
	}
	_ = discardCompletedUnsignedInstall(ctx, root, tx)
}

func discardCompletedUnsignedInstall(ctx context.Context, root, tx string) error {
	if len(tx) != 32 || !digestPattern.MatchString(tx+tx) || !filepath.IsAbs(root) {
		return failure("update_request_invalid", nil)
	}
	if err := NoUnsignedPathAliases(filepath.Join(root, "installs", tx)); err != nil {
		return err
	}
	lock, err := nativeStateLock(ctx, root)
	if err != nil {
		return err
	}
	defer releaseLock(lock)
	if record, err := ReadNativeRecord(root); err != nil || record != nil {
		return failure("update_incomplete", err)
	}
	return discardUnsignedInstallFiles(root, tx)
}

// Caller holds the native-state lock. A pending cancellation may remove its
// own preparation before retiring the record; it cannot touch a foreign tx.
func discardCancelledUnsignedInstall(root string, r *NativeRecord) error {
	if r.SigningIdentity != "adhoc" || r.Channel != "personal-test" || !inPlaceTestDomain.MatchString(r.TrustDomain) {
		return nil
	}
	return discardUnsignedInstallFiles(root, r.TransactionID)
}

func discardUnsignedInstallFiles(root, tx string) error {
	if len(tx) != 32 || !digestPattern.MatchString(tx+tx) || NoUnsignedPathAliases(filepath.Join(root, "installs", tx)) != nil {
		return failure("path_invalid", nil)
	}
	updater, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer updater.Close()
	stages, err := updater.OpenRoot("installs")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer stages.Close()
	stage, err := stages.Lstat(tx)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !stage.IsDir() {
		return failure("path_invalid", err)
	}
	path := filepath.Join(root, "installs", tx)
	var loc unsignedWorkspaceLocation
	if err := readMetadata(filepath.Join(path, "workspace.json"), &loc); err == nil {
		account, system, e := UnsignedApplicationLocations()
		if e != nil || root != account.Updater || !IsUnsignedApplication(loc.Application, account, system) {
			return failure("unsigned_test_workspace", e)
		}
		if e := removeUnsignedWorkspace(path, tx, loc.Application); e != nil {
			return e
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return stages.RemoveAll(tx)
}
