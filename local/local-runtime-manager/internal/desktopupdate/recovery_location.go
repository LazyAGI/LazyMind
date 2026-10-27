package desktopupdate

import (
	"path/filepath"
	"runtime"
)

// RecoveryBase is tied to the OS account, not HOME inherited from a scoped
// Python/process-compose service. It is shared by every admission participant.
func RecoveryBase() (string, error) {
	base, err := osAccountBase()
	if err != nil || !filepath.IsAbs(base) {
		return "", failure("trust_location", err)
	}
	return RecoveryBaseForPlatform(runtime.GOOS, func(string) string { return base }, func() (string, error) { return base, nil })
}

// The platform selector is also used by the helper's existing path contract.
// Production supplies OS account locations rather than mutable environment.
func RecoveryBaseForPlatform(platform string, localLocation func(string) string, userConfigDir func() (string, error)) (string, error) {
	if platform == "windows" {
		base := localLocation("LOCALAPPDATA")
		if !filepath.IsAbs(base) {
			return "", failure("trust_location", nil)
		}
		return base, nil
	}
	return userConfigDir()
}
