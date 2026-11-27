package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/storagepath"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winfile"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const uninstallRecoveryKey = "Software\\LazyMind\\DesktopUninstallRecovery"

type pendingUninstall struct {
	App, Stage string
	StageID    installDirectoryID
	Cleaning   bool
}

type uninstallObject struct {
	Name string
	ID   installDirectoryID
	Dir  bool
}

type uninstallState struct {
	AppID, BackupID, DataID               installDirectoryID
	Program, Data                         []uninstallObject
	Launcher                              payloadFile
	Modern, Purge, Moved, Deleting, Ready bool
}

func readPendingUninstall() (pendingUninstall, error) {
	var pending pendingUninstall
	value, err := readInstallStringRaw(uninstallRecoveryKey, "Pending")
	if err != nil || value == "" {
		return pending, err
	}
	if err := json.Unmarshal([]byte(value), &pending); err != nil {
		return pending, err
	}
	if !filepath.IsAbs(pending.App) || !filepath.IsAbs(pending.Stage) || pending.StageID == (installDirectoryID{}) ||
		!samePath(filepath.Dir(pending.Stage), filepath.Join(filepath.Dir(pending.App), ".install-tmp")) ||
		!strings.HasPrefix(filepath.Base(pending.Stage), "install-") {
		return pending, errors.New("invalid uninstall recovery locator")
	}
	return pending, nil
}

func writePendingUninstall(pending pendingUninstall) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallRecoveryKey, registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer key.Close()
	content, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	if err := key.SetStringValue("Pending", string(content)); err != nil {
		return err
	}
	return flushInstallRegistry(key)
}

func authenticateUninstall(pending pendingUninstall) (installSession, error) {
	session, err := loadPrivateSession(pending.Stage, pending.App)
	if err != nil {
		return session, err
	}
	if err := requireUninstallDirectory(pending.Stage, pending.StageID, session.Owner); err != nil {
		return session, err
	}
	if session.Removal == nil || session.Fresh != nil {
		return session, errors.New("session is not an uninstall receipt")
	}
	if session.Removal.Modern {
		if !strings.EqualFold(filepath.Base(session.App), "App") || !samePath(session.Data, filepath.Join(filepath.Dir(session.App), "Data")) {
			return session, errors.New("uninstall receipt does not describe the installed storage layout")
		}
		if err := safeStorageRoot(filepath.Dir(session.App)); err != nil {
			return session, err
		}
	} else {
		legacy, err := localAppDataRoot()
		if err != nil {
			return session, err
		}
		if !samePath(session.Data, legacy) || !samePath(session.App, filepath.Join(filepath.Dir(legacy), "Programs", appDataLeaf)) {
			return session, errors.New("uninstall receipt does not describe a legacy installation")
		}
	}
	return session, nil
}

func planUninstall(opts commandOptions) error {
	return withInstallRecoveryLock(func() error {
		pending, err := readPendingUninstall()
		if err != nil {
			return err
		}
		if pending.Stage != "" {
			if opts.Upgrade || !samePath(opts.InstallDir, pending.App) {
				return errors.New("this uninstaller is not the pending registered uninstall entry")
			}
			session, err := authenticateUninstall(pending)
			if err != nil {
				return err
			}
			if err := validateUninstallRegistration(session, false); err != nil {
				return err
			}
			if err := verifyUninstallLauncher(session); err != nil {
				return err
			}
			return writeLayoutFile(opts.Output, installLayout{App: session.App, Data: session.Data, Stage: pending.Stage})
		}
		layout, err := planInstallation(opts, false)
		if err != nil {
			return err
		}
		if !samePath(opts.InstallDir, layout.App) {
			return errors.New("uninstaller does not match the installed program location")
		}
		return writeLayoutFile(opts.Output, layout)
	})
}

func runRecoverableUninstall(opts commandOptions, pending pendingUninstall,
	stop func(string, string) ([]runningProcess, error), removeQuarantine func(string) error) error {
	if !filepath.IsAbs(opts.InstallDir) || !filepath.IsAbs(opts.Output) {
		return errors.New("uninstall requires absolute --install-dir and --output")
	}
	if fresh, err := pendingFreshSession(); err != nil {
		return err
	} else if fresh.Stage != "" {
		return errors.New("finish the pending installation before uninstalling")
	}
	started := pending.Stage == ""
	if started {
		var err error
		pending, err = startUninstall(opts, stop)
		if err != nil {
			return err
		}
	} else if pending.Cleaning || !samePath(opts.InstallDir, pending.App) {
		return errors.New("uninstall request does not match the pending program removal")
	}
	session, err := authenticateUninstall(pending)
	if err != nil {
		return err
	}
	state := session.Removal
	if state.Purge != opts.Purge && !state.Ready && (!state.Purge || !state.Deleting) {
		return errors.New("confirm the original uninstall choice to continue; pending data is unchanged")
	}
	if err := validateUninstallRegistration(session, false); err != nil {
		return err
	}
	if err := verifyUninstallLauncher(session); err != nil {
		return err
	}
	if err := validateUninstallDataLocator(session); err != nil {
		return err
	}
	if !state.Ready {
		if err := leaveAppForMutation(session.App, pending.Stage); err != nil {
			return err
		}
		if !state.Moved {
			if !started {
				if _, err := stop(session.Data, session.App); err != nil {
					return err
				}
			}
			if err := moveUninstallProgram(pending.Stage, session); err != nil {
				return err
			}
			state.Moved = true
			if err := writeInstallSession(pending.Stage, session); err != nil {
				return err
			}
		}
		if state.Purge {
			if err := continueUninstallPurge(pending, session, opts.Purge, removeQuarantine); err != nil {
				return err
			}
		}
		state.Ready = true
		if err := writeInstallSession(pending.Stage, session); err != nil {
			return err
		}
	}
	return writeLayoutFile(opts.Output, installLayout{App: session.App, Data: session.Data, Stage: pending.Stage})
}

func startUninstall(opts commandOptions, stop func(string, string) ([]runningProcess, error)) (pendingUninstall, error) {
	var pending pendingUninstall
	legacy, err := localAppDataRoot()
	if err != nil {
		return pending, err
	}
	app, err := trustedInstallDir(legacy, opts.InstallDir)
	if err != nil {
		return pending, err
	}
	if app == "" {
		return pending, errors.New("uninstall requires an authenticated App")
	}
	data, modern, err := storagepath.Resolve("")
	if err != nil {
		return pending, err
	}
	if !modern {
		data = legacy
	}
	sid, err := currentUserSID()
	if err != nil {
		return pending, err
	}
	state := &uninstallState{Modern: modern, Purge: opts.Purge}
	pending.App = app
	session := installSession{App: app, Data: data, Owner: sid, Removal: state}
	if err := validateUninstallRegistration(session, true); err != nil {
		return pending, err
	}
	if _, err := stop(data, app); err != nil {
		return pending, err
	}
	state.AppID, err = directoryIdentity(app)
	if err != nil {
		return pending, err
	}
	state.Program, err = scanUninstallTree(app)
	if err != nil {
		return pending, err
	}
	state.Launcher, err = readUninstallLauncher(app)
	if err != nil {
		return pending, err
	}
	if err := verifyUninstallLauncher(session); err != nil {
		return pending, err
	}
	if opts.Purge {
		state.DataID, err = directoryIdentity(data)
		if err != nil && (!errors.Is(err, fs.ErrNotExist) || modern) {
			return pending, err
		}
		if state.DataID != (installDirectoryID{}) {
			if err := privateOwner(data, sid); err != nil {
				return pending, err
			}
			if state.DataID.Volume != state.AppID.Volume {
				return pending, errors.New("uninstall quarantine must be on the application's volume")
			}
			state.Data, err = scanUninstallTree(data)
			if err != nil {
				return pending, err
			}
		}
	}
	parent := filepath.Join(filepath.Dir(app), ".install-tmp")
	if err := createPrivateDirectory(parent, sid); err != nil {
		return pending, err
	}
	pending.Stage, err = os.MkdirTemp(parent, "install-")
	if err != nil {
		return pending, err
	}
	pending.StageID, err = directoryIdentity(pending.Stage)
	if err != nil {
		return pending, err
	}
	backup := filepath.Join(pending.Stage, "old-app")
	if err := createPrivateDirectory(backup, sid); err != nil {
		return pending, err
	}
	state.BackupID, err = directoryIdentity(backup)
	if err != nil {
		return pending, err
	}
	if err := writeInstallSession(pending.Stage, session); err != nil {
		return pending, err
	}
	return pending, writePendingUninstall(pending)
}

func validateUninstallRegistration(session installSession, initial bool) error {
	registered, err := readInstallStringRaw(storagepath.InstallKey, "InstallLocation")
	if err != nil {
		return err
	}
	if (registered != "" && !samePath(registered, session.App)) || (registered == "" && initial) {
		return errors.New("program registration conflicts with the uninstall receipt")
	}
	found := 0
	for _, name := range []string{"UninstallString", "QuietUninstallString"} {
		command, err := readInstallStringRaw(uninstallKey, name)
		if err != nil {
			return err
		}
		if command == "" {
			continue
		}
		found++
		argv, err := windows.DecomposeCommandLine(command)
		want := []string{"/currentuser"}
		if name == "QuietUninstallString" {
			want = append(want, "/S")
		}
		if err != nil || len(argv) == 0 || !filepath.IsAbs(argv[0]) ||
			!samePath(argv[0], filepath.Join(session.App, freshUninstaller)) || !slices.Equal(argv[1:], want) {
			return fmt.Errorf("invalid registered uninstall command: %s", name)
		}
	}
	if (initial && found != 2) || (found == 0 && !session.Removal.Ready) {
		return errors.New("the Windows uninstall entry is missing")
	}
	return nil
}

func validateUninstallDataLocator(session installSession) error {
	value, err := readInstallStringRaw(storagepath.DataKey, "DataRoot")
	if err != nil {
		return err
	}
	state := session.Removal
	if state.Modern {
		if value != "" && samePath(value, session.Data) {
			return nil
		}
		if value == "" && state.Purge && state.Deleting {
			return nil
		}
	} else if value == "" {
		return nil
	}
	return errors.New("the registered uninstall data location changed")
}

func verifyUninstallLauncher(session installSession) error {
	if err := requireUninstallDirectory(session.App, session.Removal.AppID, session.Owner); err != nil {
		return err
	}
	for _, entry := range session.Removal.Program {
		if strings.EqualFold(entry.Name, freshUninstaller) && !entry.Dir {
			name := filepath.Join(session.App, entry.Name)
			id, _, err := uninstallObjectIdentity(name)
			if err != nil {
				return err
			}
			if id == entry.ID {
				return checkFreshFile(name, session.Removal.Launcher, false)
			}
		}
	}
	return errors.New("the installed uninstaller is missing or was replaced")
}

func readUninstallLauncher(app string) (payloadFile, error) {
	name := filepath.Join(app, freshUninstaller)
	if err := noReparsePath(name); err != nil {
		return payloadFile{}, err
	}
	file, err := os.Open(name)
	if err != nil {
		return payloadFile{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	return payloadFile{Path: freshUninstaller, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, err
}

func uninstallObjectIdentity(name string) (installDirectoryID, bool, error) {
	if err := noReparsePath(name); err != nil {
		return installDirectoryID{}, false, err
	}
	handle, err := windows.CreateFile(windows.StringToUTF16Ptr(name), windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return installDirectoryID{}, false, err
	}
	defer windows.CloseHandle(handle)
	return uninstallHandleIdentity(handle)
}

func uninstallHandleIdentity(handle windows.Handle) (installDirectoryID, bool, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return installDirectoryID{}, false, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return installDirectoryID{}, false, errors.New("uninstall will not follow a reparse point")
	}
	return installDirectoryID{Volume: info.VolumeSerialNumber, File: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)},
		info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0, nil
}

func requireUninstallDirectory(path string, expected installDirectoryID, owner string) error {
	id, err := directoryIdentity(path)
	if err != nil {
		return err
	}
	if id != expected {
		return fmt.Errorf("uninstall directory was replaced: %s", path)
	}
	return privateOwner(path, owner)
}

func scanUninstallTree(root string) ([]uninstallObject, error) {
	var objects []uninstallObject
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || name == root {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		id, dir, err := uninstallObjectIdentity(name)
		if err != nil {
			return err
		}
		if entry.IsDir() != dir {
			return fmt.Errorf("uninstall object changed while scanning: %s", name)
		}
		objects = append(objects, uninstallObject{Name: relative, ID: id, Dir: dir})
		return nil
	})
	return objects, err
}

func verifyUninstallTree(root string, expected []uninstallObject) ([]uninstallObject, error) {
	actual, err := scanUninstallTree(root)
	if err != nil {
		return nil, err
	}
	known := make(map[string]uninstallObject, len(expected))
	for _, entry := range expected {
		known[strings.ToLower(entry.Name)] = entry
	}
	for _, entry := range actual {
		want, found := known[strings.ToLower(entry.Name)]
		if !found || entry.ID != want.ID || entry.Dir != want.Dir {
			return nil, fmt.Errorf("uninstall will not touch an unattributed object: %s", filepath.Join(root, entry.Name))
		}
	}
	return actual, nil
}

func retainUninstallLauncher(name string) bool {
	return strings.EqualFold(name, freshUninstaller) || strings.EqualFold(name, storagepath.AppMarker)
}

func moveUninstallProgram(stage string, session installSession) error {
	backup := filepath.Join(stage, "old-app")
	if err := requireUninstallDirectory(backup, session.Removal.BackupID, session.Owner); err != nil {
		return err
	}
	if err := requireUninstallDirectory(session.App, session.Removal.AppID, session.Owner); err != nil {
		return err
	}
	appObjects, err := verifyUninstallTree(session.App, session.Removal.Program)
	if err != nil {
		return err
	}
	if _, err := verifyUninstallTree(backup, session.Removal.Program); err != nil {
		return err
	}
	for _, entry := range appObjects {
		if filepath.Dir(entry.Name) != "." || retainUninstallLauncher(entry.Name) {
			continue
		}
		if err := moveUninstallObject(filepath.Join(session.App, entry.Name), filepath.Join(backup, entry.Name), entry, session.Removal.BackupID); err != nil {
			return err
		}
	}
	return nil
}

func moveUninstallObject(source, destination string, expected uninstallObject, parentID installDirectoryID) error {
	return winfile.RetryOperation(context.Background(), func() error {
		if err := noReparsePath(source); err != nil {
			return err
		}
		handle, err := windows.CreateFile(windows.StringToUTF16Ptr(source), windows.FILE_READ_ATTRIBUTES|windows.DELETE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return err
		}
		defer windows.CloseHandle(handle)
		id, dir, err := uninstallHandleIdentity(handle)
		if err != nil {
			return err
		}
		if id != expected.ID || dir != expected.Dir {
			return fmt.Errorf("uninstall object changed before moving: %s", source)
		}
		return moveInstallHandle(handle, destination, id.Volume, &parentID)
	}, winfile.RetryOptions{MaxWait: purgeRetryLimit})
}

func continueUninstallPurge(pending pendingUninstall, session installSession, confirmed bool, removeQuarantine func(string) error) error {
	state := session.Removal
	target := filepath.Join(pending.Stage, "old-data")
	sourceID, sourceErr := directoryIdentity(session.Data)
	targetID, targetErr := directoryIdentity(target)
	if sourceErr != nil && !errors.Is(sourceErr, fs.ErrNotExist) {
		return sourceErr
	}
	if targetErr != nil && !errors.Is(targetErr, fs.ErrNotExist) {
		return targetErr
	}
	switch {
	case sourceErr == nil:
		if !confirmed {
			return errors.New("confirm data removal again to continue the pending uninstall")
		}
		if state.Deleting || sourceID != state.DataID || targetErr == nil {
			return errors.New("the original data path changed before purge")
		}
		if _, err := verifyUninstallTree(session.Data, state.Data); err != nil {
			return err
		}
		if err := moveUninstallObject(session.Data, target, uninstallObject{ID: state.DataID, Dir: true}, pending.StageID); err != nil {
			return err
		}
		fallthrough
	case targetErr == nil:
		if !confirmed {
			return errors.New("confirm data removal again to continue the pending uninstall")
		}
		if targetErr == nil && targetID != state.DataID {
			return errors.New("the quarantined data directory was replaced")
		}
		if err := requireUninstallDirectory(target, state.DataID, session.Owner); err != nil {
			return err
		}
		if _, err := verifyUninstallTree(target, state.Data); err != nil {
			return err
		}
		if !state.Deleting {
			state.Deleting = true
			if err := writeInstallSession(pending.Stage, session); err != nil {
				return err
			}
		}
		if removeQuarantine != nil {
			if err := removeQuarantine(target); err != nil {
				return err
			}
		} else if err := removeUninstallTree(target, state.DataID, session.Owner, state.Data); err != nil {
			return err
		}
		if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
			return errors.New("the verified quarantine has not been fully removed")
		}
	default:
		if !state.Deleting && state.DataID != (installDirectoryID{}) {
			return errors.New("the recorded uninstall data is missing")
		}
	}
	if state.Modern {
		return removeUninstalledDataLocator(session.Data)
	}
	return nil
}

func removeUninstalledDataLocator(expected string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, storagepath.DataKey, registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	value, kind, err := key.GetStringValue("DataRoot")
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if kind != registry.SZ || value == "" || !samePath(value, expected) {
		return errors.New("the uninstall data locator was replaced")
	}
	if err := key.DeleteValue("DataRoot"); err != nil {
		return err
	}
	return flushInstallRegistry(key)
}

func removeUninstallTree(root string, id installDirectoryID, owner string, expected []uninstallObject) error {
	if err := requireUninstallDirectory(root, id, owner); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	objects, err := verifyUninstallTree(root, expected)
	if err != nil {
		return err
	}
	for i := len(objects) - 1; i >= 0; i-- {
		if err := removeUninstallObject(filepath.Join(root, objects[i].Name), objects[i]); err != nil {
			return err
		}
	}
	return removeUninstallObject(root, uninstallObject{ID: id, Dir: true})
}

func removeUninstallObject(path string, expected uninstallObject) error {
	return winfile.RetryOperation(context.Background(), func() error {
		if err := noReparsePath(path); err != nil {
			return err
		}
		handle, err := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.FILE_READ_ATTRIBUTES|windows.DELETE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer windows.CloseHandle(handle)
		id, dir, err := uninstallHandleIdentity(handle)
		if err != nil {
			return err
		}
		if id != expected.ID || dir != expected.Dir {
			return fmt.Errorf("uninstall object changed before deletion: %s", path)
		}
		deleteFile := byte(1)
		return windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &deleteFile, 1)
	}, winfile.RetryOptions{MaxWait: purgeRetryLimit})
}

func checkUninstallRegistrationRemoved() error {
	for _, item := range [][2]string{{storagepath.InstallKey, "InstallLocation"}, {uninstallKey, "UninstallString"}, {uninstallKey, "QuietUninstallString"}} {
		value, err := readInstallStringRaw(item[0], item[1])
		if err != nil {
			return err
		}
		if value != "" {
			return errors.New("program registration was not removed; retry the uninstall from Windows settings")
		}
	}
	return nil
}

func unregisterUninstall(opts commandOptions) error {
	return withInstallRecoveryLock(func() error {
		pending, err := readPendingUninstall()
		if err != nil {
			return err
		}
		if pending.Stage == "" {
			if _, err := os.Lstat(opts.StageDir); errors.Is(err, fs.ErrNotExist) {
				return checkUninstallRegistrationRemoved()
			}
			return errors.New("unregistration does not match the pending uninstall")
		}
		if !samePath(pending.App, opts.InstallDir) || !samePath(pending.Stage, opts.StageDir) {
			return errors.New("unregistration does not match the pending uninstall")
		}
		session, err := authenticateUninstall(pending)
		if err != nil {
			return err
		}
		if !session.Removal.Ready {
			return errors.New("program removal has not reached unregistration")
		}
		if err := validateUninstallRegistration(session, false); err != nil {
			return err
		}
		if err := validateUninstallCompletionData(session); err != nil {
			return err
		}
		if err := verifyUninstallLauncher(session); err != nil {
			return err
		}
		root, err := registry.OpenKey(registry.CURRENT_USER, "", registry.QUERY_VALUE|registry.WOW64_64KEY)
		if err != nil {
			return err
		}
		defer root.Close()
		// Keep the Windows settings entry until the program registration is gone.
		for _, path := range []string{storagepath.InstallKey, uninstallKey} {
			if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil && !errors.Is(err, registry.ErrNotExist) {
				return err
			}
			if err := flushInstallRegistry(root); err != nil {
				return err
			}
		}
		return checkUninstallRegistrationRemoved()
	})
}

func validateUninstallCompletionData(session installSession) error {
	if !session.Removal.Purge {
		return validateUninstallDataLocator(session)
	}
	value, err := readInstallStringRaw(storagepath.DataKey, "DataRoot")
	if err != nil {
		return err
	}
	if value != "" {
		return errors.New("data location is still registered after purge")
	}
	if _, err := os.Lstat(session.Data); !errors.Is(err, fs.ErrNotExist) {
		return errors.New("the original data path is not absent after purge")
	}
	return nil
}

func verifyUninstalled() error {
	return withInstallRecoveryLock(func() error {
		pending, err := readPendingUninstall()
		if err != nil {
			return err
		}
		return finishUninstallation(pending)
	})
}

func completePurgeSession(opts commandOptions) error {
	return withInstallRecoveryLock(func() error {
		pending, err := readPendingUninstall()
		if err != nil {
			return err
		}
		if pending.Stage == "" {
			// Idempotent completion never authorizes removal of an arbitrary stage.
			if _, err := os.Lstat(opts.StageDir); !errors.Is(err, fs.ErrNotExist) {
				return errors.New("unrecognized uninstall completion stage")
			}
		} else if !samePath(pending.Stage, opts.StageDir) || !samePath(pending.App, opts.InstallDir) {
			return errors.New("uninstall completion does not match the pending session")
		}
		return finishUninstallation(pending)
	})
}

func finishUninstallation(pending pendingUninstall) error {
	if err := checkUninstallRegistrationRemoved(); err != nil || pending.Stage == "" {
		return err
	}
	session, err := authenticateUninstall(pending)
	if err != nil {
		if pending.Cleaning && errors.Is(err, fs.ErrNotExist) {
			return removeUninstallReceipt(pending)
		}
		return err
	}
	state := session.Removal
	if !state.Ready {
		return errors.New("uninstall has not finished removing the selected program and data")
	}
	if err := validateUninstallCompletionData(session); err != nil {
		return err
	}
	entries, err := os.ReadDir(pending.Stage)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "old-app" && entry.Name() != "session.json" {
			return fmt.Errorf("uninstall will retain unattributed staging entry: %s", entry.Name())
		}
	}
	var appFiles, backupFiles []uninstallObject
	for _, entry := range state.Program {
		if retainUninstallLauncher(entry.Name) {
			appFiles = append(appFiles, entry)
		} else {
			backupFiles = append(backupFiles, entry)
		}
	}
	if err := removeUninstallTree(filepath.Join(pending.Stage, "old-app"), state.BackupID, session.Owner, backupFiles); err != nil {
		return err
	}
	if err := removeUninstallTree(session.App, state.AppID, session.Owner, appFiles); err != nil {
		return err
	}
	pending.Cleaning = true
	if err := writePendingUninstall(pending); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(pending.Stage, "session.json")); err != nil {
		return err
	}
	return removeUninstallReceipt(pending)
}

func removeUninstallReceipt(pending pendingUninstall) error {
	// An interrupted final cleanup can remove only the original empty stage.
	if err := removeUninstallObject(pending.Stage, uninstallObject{ID: pending.StageID, Dir: true}); err != nil {
		return err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallRecoveryKey, registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.DeleteValue("Pending"); err != nil {
		return err
	}
	return flushInstallRegistry(key)
}
