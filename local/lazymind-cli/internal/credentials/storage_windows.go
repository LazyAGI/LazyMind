//go:build windows

package credentials

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	desktopStorageKey = `Software\LazyMind\DesktopStorage`
	// app-builder-lib 24.13.3 UUIDv5 for ai.lazymind.desktop, in the x64 NSIS view.
	desktopInstallKey = `Software\412e5ec7-d7ee-55e2-b3e8-1ea8840a484e`
)

// An empty result means legacy/portable, never an invalid new-layout location.
// Agents discover this independently: they may predate the Desktop process and
// must not rely on its inherited environment.
func installedDesktopDataRoot() (string, error) {
	root, present, err := desktopStorageString(desktopStorageKey, "DataRoot")
	if err != nil {
		return "", err
	}
	app, registered, err := desktopStorageString(desktopInstallKey, "InstallLocation")
	if err != nil {
		return "", err
	}
	if !present {
		if registered && app != "" {
			if !filepath.IsAbs(app) {
				return "", errors.New("LazyMind registered installation path must be absolute")
			}
			if strings.EqualFold(filepath.Base(filepath.Clean(app)), "App") {
				return "", errors.New("LazyMind desktop DataRoot is missing; repair the installation")
			}
			if _, err := os.Lstat(filepath.Join(app, ".lazymind-installed")); err == nil {
				return "", errors.New("LazyMind desktop DataRoot is missing; repair the installation")
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("check LazyMind installation marker: %w", err)
			}
		}
		return "", nil
	}
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) || !strings.EqualFold(filepath.Base(filepath.Clean(root)), "Data") {
		return "", errors.New("LazyMind desktop DataRoot must be an absolute Data directory")
	}
	root = filepath.Clean(root)
	if registered && (!filepath.IsAbs(app) || !strings.EqualFold(filepath.Clean(app), filepath.Join(filepath.Dir(root), "App"))) {
		return "", errors.New("LazyMind desktop DataRoot conflicts with the registered installation")
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("access LazyMind desktop DataRoot: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("LazyMind desktop DataRoot is not a directory")
	}
	ownerPath := filepath.Join(root, ".lazymind-data-owner")
	info, err = os.Lstat(ownerPath)
	if err != nil {
		return "", fmt.Errorf("read LazyMind desktop data ownership: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("LazyMind desktop data ownership marker must be a regular file")
	}
	owner, err := os.ReadFile(ownerPath)
	if err != nil {
		return "", fmt.Errorf("read LazyMind desktop data ownership: %w", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("identify LazyMind desktop data owner: %w", err)
	}
	if strings.TrimSpace(string(owner)) != user.User.Sid.String() {
		return "", errors.New("LazyMind desktop data belongs to another Windows user")
	}
	return root, nil
}

func desktopStorageString(path, name string) (string, bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read LazyMind desktop %s: %w", name, err)
	}
	defer key.Close()
	value, kind, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, fmt.Errorf("read LazyMind desktop %s: %w", name, err)
	}
	if kind != registry.SZ {
		return "", true, fmt.Errorf("LazyMind desktop %s must be REG_SZ", name)
	}
	return value, true, nil
}
