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
	return stages.RemoveAll(tx)
}
