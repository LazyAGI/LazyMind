//go:build !windows

package storagepath

func Resolve(currentAppDir string) (string, bool, error) { return "", false, nil }
