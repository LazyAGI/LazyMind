package desktopupdate

import "path/filepath"

func UpdaterRoot() (string, error) {
	base, err := RecoveryBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "LazyMindUpdater"), nil
}
