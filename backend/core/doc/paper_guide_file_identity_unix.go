//go:build darwin || linux

package doc

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ctime catches in-place mutations even if the previous modification time is
// restored. Device and inode distinguish atomic replacements.
func guideFileIdentity(path string) (string, error) {
	var stat unix.Stat_t
	err := unix.Stat(path, &stat)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d/%d/%d/%d/%d", stat.Dev, stat.Ino, stat.Size, stat.Ctim.Sec, stat.Ctim.Nsec), nil
}
