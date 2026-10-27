//go:build !windows

package desktopupdate

import (
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func hasMultipleLinks(_ string, info os.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, failure("snapshot_metadata", nil)
	}
	return stat.Nlink > 1, nil
}
func freeDiskBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}

func allocatedDiskBytes(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, failure("snapshot_metadata", nil)
	}
	return uint64(stat.Blocks) * 512, nil
}
