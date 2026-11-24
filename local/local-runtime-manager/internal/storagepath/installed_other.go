//go:build !windows

package storagepath

import "os"

func Resolve(currentAppDir string) (string, bool, error) { return "", false, nil }

func ResolveExplicitLocal(root, legacyRoot string) (string, bool, error) { return "", false, nil }

func CheckLocalPath(root, path string) error { return nil }

func HoldLocalLocation(path string) (string, func(), error) { return path, func() {}, nil }

func HoldCredentialStores(root, physicalRoot string, paths []string, reserveMissing, legacyDesktop bool) ([]string, func(), error) {
	return paths, func() {}, nil
}

func PublishLocalIdentity(temporary, path string) error { return os.Link(temporary, path) }
