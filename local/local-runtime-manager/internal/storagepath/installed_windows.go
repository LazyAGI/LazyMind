//go:build windows

// Package storagepath locates the current user's installed Desktop data.
package storagepath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	if err := validateInstalledData(root, app); err != nil {
		return "", false, err
	}
	return root, true, nil
}

func validateInstalledData(root, app string) error {
	if !filepath.IsAbs(root) || !strings.EqualFold(filepath.Base(root), "Data") {
		return fmt.Errorf("invalid installed LazyMind data location %q", root)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("access installed LazyMind data: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("installed LazyMind data is not a regular directory")
	}
	expectedApp := filepath.Join(filepath.Dir(root), "App")
	if app != "" && !strings.EqualFold(filepath.Clean(app), expectedApp) {
		return fmt.Errorf("installed LazyMind program and data locations disagree")
	}
	ownerPath := filepath.Join(root, OwnerMarker)
	info, err = os.Lstat(ownerPath)
	if err != nil {
		return fmt.Errorf("read installed LazyMind data owner: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 512 {
		return fmt.Errorf("invalid installed LazyMind data owner marker")
	}
	owner, err := os.ReadFile(ownerPath)
	if err != nil {
		return fmt.Errorf("read installed LazyMind data owner: %w", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("resolve current Windows user: %w", err)
	}
	if strings.TrimSpace(string(owner)) != user.User.Sid.String() {
		return fmt.Errorf("installed LazyMind data belongs to another Windows user")
	}
	return nil
}

// ResolveExplicitLocal distinguishes an installed Data root from a Local root
// whose physical location is independent of the registered installation.
func ResolveExplicitLocal(root, legacyRoot string) (installedRoot string, independent bool, err error) {
	data, present, err := registeredString(DataKey, "DataRoot")
	if err != nil {
		return "", false, err
	}
	if !present {
		installedRoot, _, err = Resolve("")
		if err == nil && installedRoot != "" {
			err = fmt.Errorf("registered Local data location changed during startup")
		}
		if err != nil || strings.EqualFold(filepath.Clean(root), filepath.Clean(legacyRoot)) {
			return "", false, err
		}
		physicalRoot, err := physicalLocation(root, false)
		if err != nil {
			return "", false, fmt.Errorf("identify explicit Local storage: %w", err)
		}
		legacy, err := physicalLocation(legacyRoot, false)
		if err != nil {
			return "", false, fmt.Errorf("identify legacy Local storage: %w", err)
		}
		independent, err := classifyLegacyLocalRoot(physicalRoot, legacy)
		return "", independent, err
	}
	data = filepath.Clean(data)
	app := filepath.Join(filepath.Dir(data), "App")
	registeredApp, _, err := registeredString(InstallKey, "InstallLocation")
	if err != nil {
		return "", false, err
	}
	if !strings.EqualFold(filepath.Base(data), "Data") || (registeredApp != "" && !strings.EqualFold(filepath.Clean(registeredApp), app)) {
		return "", false, fmt.Errorf("installed LazyMind program and data registration disagree")
	}
	if strings.EqualFold(filepath.Clean(root), data) {
		if err := validateInstalledData(data, registeredApp); err != nil {
			return "", false, err
		}
		return data, false, nil
	}
	physicalRoot, err := physicalLocation(root, false)
	if err != nil {
		return "", false, fmt.Errorf("verify Local root against installed data registration: %w", err)
	}
	for _, location := range []string{data, app} {
		if pathWithin(root, location) || pathWithin(location, root) {
			return "", false, fmt.Errorf("explicit Local root overlaps registered LazyMind storage")
		}
		registered, err := physicalLocation(location, false)
		if err != nil {
			return "", false, fmt.Errorf("identify registered LazyMind storage: %w", err)
		}
		if pathWithin(physicalRoot, registered) || pathWithin(registered, physicalRoot) {
			return "", false, fmt.Errorf("explicit Local root overlaps registered LazyMind storage")
		}
	}
	legacy, err := physicalLocation(legacyRoot, false)
	if err != nil {
		return "", false, fmt.Errorf("identify legacy Local storage: %w", err)
	}
	if independent, err := classifyLegacyLocalRoot(physicalRoot, legacy); err != nil || !independent {
		return "", false, err
	}
	if err := validateInstalledData(data, registeredApp); err != nil {
		return "", true, nil
	}
	return data, true, nil
}

func classifyLegacyLocalRoot(root, legacy string) (bool, error) {
	if strings.EqualFold(root, legacy) {
		return false, nil
	}
	if pathWithin(root, legacy) || pathWithin(legacy, root) {
		return false, fmt.Errorf("explicit Local root overlaps legacy LazyMind data")
	}
	return true, nil
}

// CheckLocalPath also checks paths that are not yet created by resolving their
// existing ancestors. A child path that redirects outside the root is unsafe.
func CheckLocalPath(root, path string) error {
	if !pathWithin(path, root) {
		return fmt.Errorf("credential location is outside the independent Local root")
	}
	physicalRoot, err := physicalLocation(root, false)
	if err != nil {
		return err
	}
	physicalPath, err := physicalLocation(path, true)
	if err != nil {
		return err
	}
	if !pathWithin(physicalPath, physicalRoot) {
		return fmt.Errorf("credential location redirects outside the independent Local root")
	}
	return nil
}

// PublishLocalIdentity renames a completed temporary file without replacing an
// identity already published by another service. Both paths are in one directory.
func PublishLocalIdentity(temporary, path string) error {
	source, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	destination, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFile(source, destination)
}

func pathWithin(path, root string) bool {
	path = strings.ToLower(filepath.Clean(path))
	root = strings.ToLower(filepath.Clean(root))
	return path == root || strings.HasPrefix(path, strings.TrimRight(root, `\`)+`\`)
}

func physicalLocation(path string, rejectFileHardlinks bool) (string, error) {
	physical, _, release, err := openPhysicalLocation(path, rejectFileHardlinks, true)
	if err != nil {
		return "", err
	}
	release()
	return physical, nil
}

// HoldLocalLocation pins the handle of the nearest existing ancestor without
// delete sharing. Use the returned volume-GUID path for credential publication.
func HoldLocalLocation(path string) (string, func(), error) {
	physical, _, release, err := openPhysicalLocation(path, false, false)
	return physical, release, err
}

// HoldCredentialStores pins the exact database files. Publication also reserves
// missing names until release. An existing SQLite log prevents a safe scan.
func HoldCredentialStores(root, physicalRoot string, paths []string, reserveMissing, legacyDesktop bool) (readPaths []string, release func(), err error) {
	var closes []func()
	releaseAll := func() {
		for i := len(closes) - 1; i >= 0; i-- {
			closes[i]()
		}
	}
	defer func() {
		if err != nil {
			releaseAll()
		}
	}()
	type reservation struct {
		path   string
		exists bool
	}
	reservations := map[string]reservation{}
	plan := func(path, scope string, log bool) (physical, missing string, err error) {
		physical, missing, close, err := openPhysicalLocation(path, true, false)
		if err != nil {
			return "", "", err
		}
		closes = append(closes, close)
		if !pathWithin(physical, scope) {
			return "", "", fmt.Errorf("credential database redirects outside the verified storage root")
		}
		if log && missing == "" {
			return "", "", fmt.Errorf("SQLite log is present; close the database safely before retrying credential setup")
		}
		name := physical
		if missing != "" {
			name = missing
		}
		key := strings.ToLower(name)
		if previous, ok := reservations[key]; ok && previous.exists != (missing == "") {
			return "", "", fmt.Errorf("credential database path changed during setup")
		}
		reservations[key] = reservation{path: name, exists: missing == ""}
		return physical, missing, nil
	}
	for _, path := range paths {
		scope := physicalRoot
		if !pathWithin(path, root) {
			if !legacyDesktop || !filepath.IsAbs(path) {
				return nil, nil, fmt.Errorf("credential database is outside the verified storage root")
			}
			// A legacy Desktop may configure its actual database outside the
			// default profile. Authorize only that database and its log names
			// under a pinned parent, independent of any installed App/Data.
			parent, close, err := HoldLocalLocation(filepath.Dir(path))
			if err != nil {
				return nil, nil, err
			}
			closes = append(closes, close)
			if _, independent, err := ResolveExplicitLocal(parent, physicalRoot); err != nil {
				return nil, nil, err
			} else if !independent {
				return nil, nil, fmt.Errorf("external Desktop database overlaps registered or legacy storage")
			}
			scope = parent
		}
		physical, missing, err := plan(path, scope, false)
		if err != nil {
			return nil, nil, err
		}
		if missing == "" || strings.EqualFold(missing, physical) {
			parent, close, err := HoldLocalLocation(filepath.Dir(path))
			if err != nil {
				return nil, nil, err
			}
			closes = append(closes, close)
			// SQLite derives log names from the database name. A different
			// physical leaf could have logs under the original name as well.
			if !strings.EqualFold(physical, filepath.Join(parent, filepath.Base(path))) {
				return nil, nil, fmt.Errorf("credential database name redirects to another file")
			}
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if _, _, err := plan(physical+suffix, scope, true); err != nil {
					return nil, nil, err
				}
			}
		}
		// The placeholder for a missing ancestor also blocks all its children.
		if missing != "" {
			physical = ""
		}
		readPaths = append(readPaths, physical)
	}
	keys := make([]string, 0, len(reservations))
	for key := range reservations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		reservation := reservations[key]
		if !reserveMissing && !reservation.exists {
			continue
		}
		close, err := holdCredentialFile(reservation.path, reservation.exists)
		if err != nil {
			return nil, nil, err
		}
		closes = append(closes, close)
	}
	return readPaths, releaseAll, nil
}

func holdCredentialFile(path string, exists bool) (func(), error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access, share, disposition := uint32(windows.GENERIC_READ), uint32(windows.FILE_SHARE_READ), uint32(windows.OPEN_EXISTING)
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if !exists {
		access, share, disposition = windows.DELETE, 0, windows.CREATE_NEW
		flags |= windows.FILE_ATTRIBUTE_TEMPORARY | windows.FILE_FLAG_DELETE_ON_CLOSE
	}
	handle, err := windows.CreateFile(name, access, share, nil, disposition, flags, 0)
	if err != nil {
		return nil, err
	}
	close := func() { windows.CloseHandle(handle) }
	if exists {
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			close()
			return nil, err
		}
		physical, err := physicalHandlePath(handle, true)
		if err != nil || !strings.EqualFold(physical, path) || info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
			close()
			return nil, fmt.Errorf("credential database changed during setup")
		}
	}
	return close, nil
}

func openPhysicalLocation(path string, rejectFileHardlinks, shareDelete bool) (string, string, func(), error) {
	if !filepath.IsAbs(path) {
		return "", "", nil, fmt.Errorf("storage path is not absolute")
	}
	current, suffix, firstMissing := filepath.Clean(path), "", ""
	for {
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", nil, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", nil, fmt.Errorf("storage volume is unavailable")
		}
		firstMissing = filepath.Base(current)
		suffix = filepath.Join(firstMissing, suffix)
		current = parent
	}
	name, err := windows.UTF16PtrFromString(current)
	if err != nil {
		return "", "", nil, err
	}
	sharing := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
	if shareDelete {
		sharing |= windows.FILE_SHARE_DELETE
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, sharing, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", "", nil, err
	}
	physical, err := physicalHandlePath(handle, rejectFileHardlinks && suffix == "")
	if err != nil {
		windows.CloseHandle(handle)
		return "", "", nil, err
	}
	if firstMissing != "" {
		firstMissing = filepath.Join(physical, firstMissing)
	}
	return filepath.Join(physical, suffix), firstMissing, func() { windows.CloseHandle(handle) }, nil
}

func physicalHandlePath(handle windows.Handle, rejectFileHardlinks bool) (string, error) {
	if rejectFileHardlinks {
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			return "", err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 && info.NumberOfLinks > 1 {
			return "", fmt.Errorf("credential file has multiple filesystem names")
		}
	}
	const volumeNameGUID = 1
	buffer := make([]uint16, 32768)
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), volumeNameGUID)
	if err != nil {
		return "", err
	}
	if length >= uint32(len(buffer)) {
		return "", fmt.Errorf("resolved storage path is too long")
	}
	return windows.UTF16ToString(buffer[:length]), nil
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
