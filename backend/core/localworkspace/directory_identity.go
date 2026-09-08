package localworkspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func currentDirectoryIdentity(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", os.ErrInvalid
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	realPath, err = filepath.Abs(realPath)
	if err != nil {
		return "", err
	}
	if !sameCanonicalPath(filepath.Clean(path), filepath.Clean(realPath)) {
		return "", os.ErrInvalid
	}
	info, err := os.Stat(realPath)
	if err != nil || !info.IsDir() {
		return "", os.ErrInvalid
	}
	return platformDirectoryIdentity(realPath, info)
}

func sameCanonicalPath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
