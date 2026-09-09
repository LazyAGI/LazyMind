//go:build !windows

package localworkspace

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
)

func platformDirectoryIdentity(_ string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", os.ErrInvalid
	}
	return fmt.Sprintf("fsid:%s:%d:%d", runtime.GOOS, stat.Dev, stat.Ino), nil
}

func fileHasAliases(file *os.File, info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return !ok || stat.Nlink != 1
}

func openedDirectoryIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	return platformDirectoryIdentity("", info)
}
