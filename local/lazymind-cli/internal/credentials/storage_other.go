//go:build !windows

package credentials

func installedDesktopDataRoot() (string, error) { return "", nil }
