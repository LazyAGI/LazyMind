package pdfir

import (
	"golang.org/x/sys/windows"
	"os"
)

// Python FileLock uses msvcrt.locking on the first byte on Windows.
func tryArchiveLock(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func releaseArchiveLock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
func archiveLockBusy(err error) bool { return err == windows.ERROR_LOCK_VIOLATION }
