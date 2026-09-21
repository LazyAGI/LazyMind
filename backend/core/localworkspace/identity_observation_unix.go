//go:build !windows

package localworkspace

import "os"

func observeIdentityPath(path string) (os.FileInfo, error) { return os.Lstat(path) }
