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
