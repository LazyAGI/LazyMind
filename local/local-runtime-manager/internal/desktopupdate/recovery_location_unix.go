//go:build !windows

package desktopupdate

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
)

func osAccountBase() (string, error) {
	account, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(account.HomeDir, "Library", "Application Support"), nil
	}
	return filepath.Join(account.HomeDir, ".config"), nil
}
