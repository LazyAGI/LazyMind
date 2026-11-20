//go:build windows

// Package storagepath locates the current user's installed Desktop data.
package storagepath

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
	DataKey     = `Software\LazyMind\DesktopStorage`
	InstallKey  = `Software\412e5ec7-d7ee-55e2-b3e8-1ea8840a484e`
	AppMarker   = ".lazymind-installed"
	OwnerMarker = ".lazymind-data-owner"
)

// Resolve returns an empty root for legacy/portable installations. A missing
// DataRoot is an error when the running or registered App identifies a new
// installation. This read-only check does not authorize deletion or stopping.
func Resolve(currentAppDir string) (root string, newLayout bool, err error) {
	marked, err := installedMarker(currentAppDir)
	if err != nil {
		return "", false, err
	}
	app, _, appErr := registeredString(InstallKey, "InstallLocation")
	if currentAppDir != "" {
		matches := appErr == nil && app != "" && strings.EqualFold(filepath.Clean(currentAppDir), filepath.Clean(app))
		if marked && !matches {
			return "", false, fmt.Errorf("installed LazyMind program registration is missing or does not match this App")
		}
		if !marked && !matches {
			// A ZIP or legacy executable outside the registered App keeps its
			// original profile even when another installation has bad DataRoot.
			return "", false, nil
		}
	}
	if appErr != nil {
		return "", false, appErr
	}
	root, present, err := registeredString(DataKey, "DataRoot")
	if err != nil {
		return "", false, err
	}
	if currentAppDir != "" && !marked && present {
		return "", false, fmt.Errorf("installed LazyMind storage marker is missing; repair the installation")
	}
	if !present {
		// Purge may have removed App and its marker before unregistering the
		// program failed. The reserved new-layout App leaf only rejects fallback;
		// it never authenticates a path for cleanup or process termination.
		if app != "" && strings.EqualFold(filepath.Base(filepath.Clean(app)), "App") {
			return "", false, fmt.Errorf("installed LazyMind data location is missing; repair the installation")
		}
		if !marked {
			marked, err = installedMarker(app)
			if err != nil {
				return "", false, err
			}
		}
		if marked {
			return "", false, fmt.Errorf("installed LazyMind data location is missing; repair the installation")
		}
		return "", false, nil
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || !strings.EqualFold(filepath.Base(root), "Data") {
		return "", false, fmt.Errorf("invalid installed LazyMind data location %q", root)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", false, fmt.Errorf("access installed LazyMind data: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("installed LazyMind data is not a regular directory")
	}
	expectedApp := filepath.Join(filepath.Dir(root), "App")
	if app != "" && !strings.EqualFold(filepath.Clean(app), expectedApp) {
		return "", false, fmt.Errorf("installed LazyMind program and data locations disagree")
	}
	ownerPath := filepath.Join(root, OwnerMarker)
	info, err = os.Lstat(ownerPath)
	if err != nil {
		return "", false, fmt.Errorf("read installed LazyMind data owner: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 512 {
		return "", false, fmt.Errorf("invalid installed LazyMind data owner marker")
	}
	owner, err := os.ReadFile(ownerPath)
	if err != nil {
		return "", false, fmt.Errorf("read installed LazyMind data owner: %w", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", false, fmt.Errorf("resolve current Windows user: %w", err)
	}
	if strings.TrimSpace(string(owner)) != user.User.Sid.String() {
		return "", false, fmt.Errorf("installed LazyMind data belongs to another Windows user")
	}
	return root, true, nil
}

func installedMarker(directory string) (bool, error) {
	if directory == "" {
		return false, nil
	}
	info, err := os.Lstat(filepath.Join(directory, AppMarker))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect installed storage marker: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != 0 {
		return false, fmt.Errorf("invalid installed storage marker in %s", directory)
	}
	return true, nil
}

func registeredString(path, name string) (string, bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read LazyMind installation registration: %w", err)
	}
	defer key.Close()
	value, kind, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil || kind != registry.SZ || strings.TrimSpace(value) == "" {
		return "", false, fmt.Errorf("invalid LazyMind installation registration %s", name)
	}
	if !filepath.IsAbs(value) {
		return "", false, fmt.Errorf("LazyMind installation registration %s is not absolute", name)
	}
	return value, true, nil
}
