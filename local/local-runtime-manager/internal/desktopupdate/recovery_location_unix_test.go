//go:build !windows

package desktopupdate

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func fixtureOSRecoveryBase(t *testing.T) string {
	t.Helper()
	account, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(account.HomeDir, "Library", "Application Support")
	}
	return filepath.Join(account.HomeDir, ".config")
}
