package desktopupdate

import "golang.org/x/sys/windows"

func osAccountBase() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
}
