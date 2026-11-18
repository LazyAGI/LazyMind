package desktopupdate

import "path/filepath"

// StateNamespace is fixed at build time; isolated test builds use their own namespace.
var StateNamespace = "LazyMindUpdater"

func UpdaterRoot() (string, error) {
	base, err := RecoveryBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, StateNamespace), nil
}
