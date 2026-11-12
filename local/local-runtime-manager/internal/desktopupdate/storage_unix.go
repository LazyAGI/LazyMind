//go:build !windows

package desktopupdate

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
func unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func replaceFile(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(to))
}
