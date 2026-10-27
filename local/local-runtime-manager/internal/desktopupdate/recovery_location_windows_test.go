//go:build windows

package desktopupdate

import (
	"golang.org/x/sys/windows"
	"testing"
)

func fixtureOSRecoveryBase(t *testing.T) string {
	t.Helper()
	base, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		t.Fatal(err)
	}
	return base
}
