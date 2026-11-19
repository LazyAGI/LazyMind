//go:build !windows

package pdfir

import (
	"os"
	"syscall"
)

func tryArchiveLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
func releaseArchiveLock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
func archiveLockBusy(err error) bool      { return err == syscall.EWOULDBLOCK || err == syscall.EAGAIN }
