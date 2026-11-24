package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/storagepath"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	freshRecoveryKey = "Software\\LazyMind\\DesktopInstallRecovery"
	freshUninstaller = "Uninstall LazyMind.exe"
)

type installDirectoryID struct {
	Volume uint32
	File   uint64
}

type freshInstallState struct {
	InstallerPID                         uint32
	InstallerStart                       uint64
	AppExisted, DataExisted, AppVerified bool
	AppID, DataID                        installDirectoryID
	Payload                              []payloadFile
	Program, Uninstall                   map[string]string
	UninstallNumbers                     map[string]uint64
}

type pendingInstallation struct {
	Stage    string
	ID       installDirectoryID
	Cleaning bool
}

func (p pendingInstallation) app() string {
	return filepath.Join(filepath.Dir(filepath.Dir(p.Stage)), "App")
}

func withInstallRecoveryLock(action func() error) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	handle, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr("Global\\LazyMindInstallRecovery-"+sid))
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, 30000)
	if err != nil {
		return err
	}
	if result != windows.WAIT_OBJECT_0 && result != windows.WAIT_ABANDONED {
		return errors.New("another LazyMind setup operation is active")
	}
	defer windows.ReleaseMutex(handle)
	return action()
}

func directoryIdentity(name string) (installDirectoryID, error) {
	if err := noReparsePath(name); err != nil {
		return installDirectoryID{}, err
	}
	handle, err := windows.CreateFile(windows.StringToUTF16Ptr(name), windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return installDirectoryID{}, err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return installDirectoryID{}, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return installDirectoryID{}, errors.New("installation directory identity changed")
	}
	return installDirectoryID{info.VolumeSerialNumber, uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)}, nil
}

func pendingFreshSession() (pendingInstallation, error) {
	var pending pendingInstallation
	value, err := readInstallStringRaw(freshRecoveryKey, "Pending")
	if err != nil || value == "" {
		return pending, err
	}
	if err := json.Unmarshal([]byte(value), &pending); err != nil {
		return pending, fmt.Errorf("read pending installation: %w", err)
	}
	if !filepath.IsAbs(pending.Stage) || filepath.Base(filepath.Dir(pending.Stage)) != ".install-tmp" ||
		!strings.HasPrefix(filepath.Base(pending.Stage), "install-") || pending.ID == (installDirectoryID{}) {
		return pending, errors.New("invalid pending installation locator")
	}
	return pending, nil
}

func writePendingInstallation(pending pendingInstallation) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, freshRecoveryKey, registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
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

func readInstallStringRaw(path, name string) (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, kind, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if kind != registry.SZ || value == "" {
		return "", fmt.Errorf("invalid registered %s", name)
	}
	return value, nil
}

func flushInstallRegistry(key registry.Key) error {
	status, _, _ := windows.NewLazySystemDLL("advapi32.dll").NewProc("RegFlushKey").Call(uintptr(key))
	if status != 0 {
		return windows.Errno(status)
	}
	return nil
}

func writeInstallSession(stage string, session installSession) error {
	content, err := json.Marshal(session)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(stage, "session-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(content)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(stage, "session.json"))
}

func installerCreationTime(pid uint32) (uint64, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	owned, err := handleOwnedByCurrentUser(handle)
	if err != nil {
		return 0, err
	}
	if !owned {
		return 0, errors.New("setup process belongs to another Windows user")
	}
	result, err := windows.WaitForSingleObject(handle, 0)
	if err != nil || result == windows.WAIT_OBJECT_0 {
		return 0, err
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), nil
}

func planFreshInstall(opts commandOptions) (*freshInstallState, error) {
	if opts.InstallerPID == 0 || opts.InstallVersion == "" || opts.ShortcutName == "" || !filepath.IsAbs(opts.Uninstaller) {
		return nil, errors.New("fresh installation requires installer identity and embedded uninstaller metadata")
	}
	start, err := installerCreationTime(opts.InstallerPID)
	if err != nil {
		return nil, err
	}
	if start == 0 {
		return nil, errors.New("fresh installation owner process is absent")
	}
	manifest, err := readPayloadManifest(opts.Manifest)
	if err != nil {
		return nil, err
	}
	for _, entry := range manifest.Files {
		if strings.EqualFold(entry.Path, freshUninstaller) {
			return nil, errors.New("the archive unexpectedly contains the embedded uninstaller")
		}
	}
	if err := noReparsePath(opts.Uninstaller); err != nil {
		return nil, err
	}
	uninstaller, err := os.Open(opts.Uninstaller)
	if err != nil {
		return nil, err
	}
	defer uninstaller.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, uninstaller)
	if err != nil {
		return nil, err
	}
	fresh := freshInstallExpectations(opts.InstallDir)
	fresh.InstallerPID, fresh.InstallerStart = opts.InstallerPID, start
	fresh.Payload = append(manifest.Files, payloadFile{Path: freshUninstaller, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))})
	fresh.Program["ShortcutName"] = opts.ShortcutName
	if opts.MenuDirectory != "" {
		fresh.Program["MenuDirectory"] = opts.MenuDirectory
	}
	if opts.Publisher != "" {
		fresh.Uninstall["Publisher"] = opts.Publisher
	}
	fresh.Uninstall["DisplayVersion"] = opts.InstallVersion
	if opts.EstimatedSize != 0 {
		fresh.UninstallNumbers["EstimatedSize"] = opts.EstimatedSize
	}
	return fresh, nil
}

func freshInstallExpectations(app string) *freshInstallState {
	command := fmt.Sprintf("\"%s\" /currentuser", filepath.Join(app, freshUninstaller))
	return &freshInstallState{
		Program: map[string]string{"InstallLocation": app, "KeepShortcuts": "true", "ShortcutName": "LazyMind"},
		Uninstall: map[string]string{
			"DisplayName": "LazyMind", "UninstallString": command, "QuietUninstallString": command + " /S",
			"DisplayIcon": filepath.Join(app, "LazyMind.exe") + ",0",
		},
		UninstallNumbers: map[string]uint64{"NoModify": 1, "NoRepair": 1},
	}
}

func createFreshInstallSession(layout installLayout, session installSession) error {
	pending, err := pendingFreshSession()
	if err != nil {
		return err
	}
	if pending.Stage != "" {
		return errors.New("another installation still has a recovery session")
	}
	fresh := layout.Fresh
	if fresh == nil {
		fresh = freshInstallExpectations(layout.App)
	}
	for name, id := range map[string]*installDirectoryID{layout.App: &fresh.AppID, layout.Data: &fresh.DataID} {
		entries, err := os.ReadDir(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return fmt.Errorf("fresh installation directory is not empty: %s", name)
		}
		if err := privateOwner(name, session.Owner); err != nil {
			return err
		}
		*id, err = directoryIdentity(name)
		if err != nil {
			return err
		}
	}
	fresh.AppExisted = fresh.AppID != (installDirectoryID{})
	fresh.DataExisted = fresh.DataID != (installDirectoryID{})
	session.Fresh = fresh
	id, err := directoryIdentity(layout.Stage)
	if err != nil {
		return err
	}
	if err := writeInstallSession(layout.Stage, session); err != nil {
		return err
	}
	return writePendingInstallation(pendingInstallation{Stage: layout.Stage, ID: id})
}

func authenticatedFreshSession(pending pendingInstallation) (installSession, error) {
	session, err := loadPrivateSession(pending.Stage, pending.app())
	if err != nil {
		return session, err
	}
	id, err := directoryIdentity(pending.Stage)
	if err != nil {
		return session, err
	}
	if id != pending.ID || session.Fresh == nil ||
		!samePath(session.Data, filepath.Join(filepath.Dir(session.App), "Data")) {
		return session, errors.New("pending installation identity changed")
	}
	for _, entry := range session.Fresh.Payload {
		if !filepath.IsLocal(filepath.FromSlash(entry.Path)) || entry.Path == "." || entry.Size < 0 ||
			entry.Size == 1<<63-1 || len(entry.SHA256) != 2*sha256.Size {
			return session, errors.New("invalid pending installation payload evidence")
		}
	}
	return session, nil
}

func prepareFreshDirectories(layout installLayout, sid string) error {
	session, err := loadPrivateSession(layout.Stage, layout.App)
	if err != nil {
		return err
	}
	for name, id := range map[string]*installDirectoryID{layout.App: &session.Fresh.AppID, layout.Data: &session.Fresh.DataID} {
		if err := noReparsePath(name); err != nil {
			return err
		}
		if *id == (installDirectoryID{}) {
			if _, err := createDirectoryWithPrivateACL(name, sid); err != nil {
				return err
			}
		}
		if err := recordInstallDirectory(name, id, sid); err != nil {
			return err
		}
		if err := writeInstallSession(layout.Stage, session); err != nil {
			return err
		}
	}
	if err := writeFreshMarker(filepath.Join(layout.App, storagepath.AppMarker), ""); err != nil {
		return err
	}
	return writeFreshMarker(filepath.Join(layout.Data, storagepath.OwnerMarker), sid+"\n")
}

func writeFreshMarker(name, contents string) error {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(contents)
	return errors.Join(writeErr, file.Close())
}

func recordInstallDirectory(name string, expected *installDirectoryID, sid string) error {
	if err := privateOwner(name, sid); err != nil {
		return err
	}
	id, err := directoryIdentity(name)
	if err != nil {
		return err
	}
	if *expected != (installDirectoryID{}) && *expected != id {
		return fmt.Errorf("installation directory was replaced: %s", name)
	}
	*expected = id
	return nil
}

func rememberVerifiedInstallPayload(opts commandOptions, manifest payloadManifest) error {
	if opts.StageDir == "" || opts.InstallDir == "" || !samePath(opts.PayloadDir, opts.InstallDir) {
		return nil
	}
	return withInstallRecoveryLock(func() error {
		pending, err := pendingFreshSession()
		if err != nil || pending.Stage == "" {
			return err
		}
		if !samePath(pending.Stage, opts.StageDir) || !samePath(pending.app(), opts.InstallDir) {
			return errors.New("verified payload does not match the pending installation")
		}
		session, err := authenticatedFreshSession(pending)
		if err != nil {
			return err
		}
		fresh := session.Fresh
		if len(fresh.Payload) == 0 {
			fresh.Payload = manifest.Files
		} else {
			// The archive is verified before NSIS writes the embedded uninstaller.
			if len(fresh.Payload) != len(manifest.Files)+1 {
				return errors.New("verified payload differs from the prepared package")
			}
			for i, entry := range manifest.Files {
				if entry != fresh.Payload[i] {
					return errors.New("verified payload differs from the prepared package")
				}
			}
		}
		if err := recordInstallDirectory(session.App, &fresh.AppID, session.Owner); err != nil {
			return err
		}
		if err := recordInstallDirectory(session.Data, &fresh.DataID, session.Owner); err != nil {
			return err
		}
		fresh.AppVerified = true
		return writeInstallSession(pending.Stage, session)
	})
}

func publishInstallPayload(opts commandOptions) error {
	return withInstallRecoveryLock(func() error {
		pending, err := pendingFreshSession()
		if err != nil {
			return err
		}
		if pending.Stage == "" || pending.Cleaning || !samePath(pending.Stage, opts.StageDir) || !samePath(pending.app(), opts.InstallDir) {
			return errors.New("payload target does not match the pending fresh installation")
		}
		session, err := authenticatedFreshSession(pending)
		if err != nil {
			return err
		}
		fresh := session.Fresh
		if len(fresh.Payload) == 0 || fresh.Payload[len(fresh.Payload)-1].Path != freshUninstaller {
			return errors.New("fresh installation payload receipt is incomplete")
		}
		id, err := directoryIdentity(session.App)
		if err != nil {
			return err
		}
		if id != fresh.AppID || id.Volume != pending.ID.Volume {
			return errors.New("fresh App is not the prepared directory on the staging volume")
		}
		if err := privateOwner(session.App, session.Owner); err != nil {
			return err
		}
		entries, err := os.ReadDir(session.App)
		if err != nil {
			return err
		}
		if len(entries) != 1 || entries[0].Name() != storagepath.AppMarker {
			return errors.New("fresh App contains files before payload publication")
		}
		extracted := filepath.Join(pending.Stage, "7z-out")
		if err := privateOwner(extracted, session.Owner); err != nil {
			return err
		}
		uninstaller := fresh.Payload[len(fresh.Payload)-1]
		stagedUninstaller, err := stageFreshUninstaller(pending.Stage, opts.Uninstaller, uninstaller)
		if err != nil {
			return err
		}
		for _, entry := range fresh.Payload {
			relative := filepath.FromSlash(entry.Path)
			source := filepath.Join(extracted, relative)
			if entry.Path == freshUninstaller {
				source = stagedUninstaller
			}
			destination := filepath.Join(session.App, relative)
			parent := filepath.Dir(destination)
			if err := noReparsePath(parent); err != nil {
				return err
			}
			if err := os.MkdirAll(parent, 0o700); err != nil {
				return err
			}
			if err := moveVerifiedInstallFile(source, destination, entry, pending.ID.Volume); err != nil {
				return err
			}
		}
		fresh.AppVerified = true
		return writeInstallSession(pending.Stage, session)
	})
}

func stageFreshUninstaller(stage, source string, expected payloadFile) (string, error) {
	input, err := openVerifiedFreshFile(source, expected, windows.GENERIC_READ)
	if err != nil {
		return "", err
	}
	defer input.Close()
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	output, err := os.CreateTemp(stage, "embedded-uninstaller-*.exe")
	if err != nil {
		return "", err
	}
	size, copyErr := io.Copy(output, io.LimitReader(input, expected.Size+1))
	if copyErr == nil {
		copyErr = output.Sync()
	}
	if err := errors.Join(copyErr, output.Close()); err != nil {
		return "", err
	}
	if size != expected.Size {
		return "", errors.New("the staged uninstaller has an unexpected size")
	}
	return output.Name(), nil
}

func moveVerifiedInstallFile(source, destination string, expected payloadFile, volume uint32) error {
	file, err := openVerifiedFreshFile(source, expected, windows.GENERIC_READ|windows.DELETE)
	if err != nil {
		return err
	}
	defer file.Close()
	return moveInstallHandle(windows.Handle(file.Fd()), destination, volume, nil)
}

func moveInstallHandle(handle windows.Handle, destination string, volume uint32, expectedParent *installDirectoryID) error {
	if err := noReparsePath(filepath.Dir(destination)); err != nil {
		return err
	}
	parent, err := windows.CreateFile(windows.StringToUTF16Ptr(filepath.Dir(destination)),
		// FILE_WRITE_DATA and FILE_APPEND_DATA add files and subdirectories.
		windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE|windows.FILE_WRITE_DATA|windows.FILE_APPEND_DATA,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(parent)
	var parentInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(parent, &parentInfo); err != nil {
		return err
	}
	if parentInfo.VolumeSerialNumber != volume || parentInfo.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		parentInfo.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return errors.New("payload destination is not a regular directory on the staging volume")
	}
	parentID := installDirectoryID{
		Volume: volume, File: uint64(parentInfo.FileIndexHigh)<<32 | uint64(parentInfo.FileIndexLow),
	}
	if expectedParent != nil && parentID != *expectedParent {
		return errors.New("the move destination directory was replaced")
	}
	// FILE_RENAME_INFO renames the verified handle on NTFS. With zero flags
	// it cannot overwrite a destination or fall back to a streamed copy.
	type fileRenameInfo struct {
		Flags          uint32
		Root           windows.Handle
		FileNameLength uint32
		FileName       uint16
	}
	name, err := windows.UTF16FromString(filepath.Base(destination))
	if err != nil {
		return err
	}
	offset := int(unsafe.Offsetof(fileRenameInfo{}.FileName))
	buffer := make([]byte, max(int(unsafe.Sizeof(fileRenameInfo{})), offset+len(name)*2))
	info := (*fileRenameInfo)(unsafe.Pointer(&buffer[0]))
	info.Root = parent
	info.FileNameLength = uint32((len(name) - 1) * 2)
	copy(unsafe.Slice(&info.FileName, len(name)), name)
	if err := windows.SetFileInformationByHandle(handle, windows.FileRenameInfo, &buffer[0], uint32(len(buffer))); err != nil {
		return fmt.Errorf("publish %s: %w", destination, err)
	}
	return nil
}

func recoverInterruptedInstallation() error {
	return withInstallRecoveryLock(func() error {
		removal, err := readPendingUninstall()
		if err != nil {
			return err
		}
		if removal.Stage != "" {
			if err := finishUninstallation(removal); err != nil {
				return fmt.Errorf("finish the pending uninstall before installing: %w", err)
			}
		}
		pending, err := pendingFreshSession()
		if err != nil || pending.Stage == "" {
			return err
		}
		if session, err := authenticatedFreshSession(pending); err == nil {
			fresh := session.Fresh
			if fresh.InstallerPID != 0 {
				start, err := installerCreationTime(fresh.InstallerPID)
				if err != nil {
					return err
				}
				if start != 0 && start == fresh.InstallerStart {
					return errors.New("the other LazyMind installer is still running")
				}
			}
		} else if !pending.Cleaning {
			return err
		}
		if err := finishFreshInstallation(pending, true); err != nil {
			return err
		}
		fmt.Println("Recovered the interrupted installation")
		return nil
	})
}

func recoverFailedPreparation(pending pendingInstallation, cause error) error {
	if err := finishFreshInstallation(pending, true); err != nil {
		return errors.Join(cause, fmt.Errorf("recover failed preparation: %w", err))
	}
	return cause
}

func finishFreshInstallation(pending pendingInstallation, rollback bool) error {
	if pending.Cleaning {
		return cleanFreshInstallationStage(pending)
	}
	session, err := authenticatedFreshSession(pending)
	if err != nil {
		return err
	}
	if err := leaveAppForMutation(session.App, pending.Stage); err != nil {
		return err
	}
	published, err := readInstallStringRaw(storagepath.DataKey, "DataRoot")
	if err != nil {
		return err
	}
	switch {
	case published != "":
		if !samePath(published, session.Data) {
			return errors.New("the published data belongs to another installation")
		}
		if _, err := trustedInstallDir("", session.App); err != nil {
			return err
		}
	case rollback:
		if err := rollbackFreshInstall(session); err != nil {
			return err
		}
	default:
		if err := publishFreshInstallation(session); err != nil {
			return err
		}
	}
	pending.Cleaning = true
	if err := writePendingInstallation(pending); err != nil {
		return err
	}
	return cleanFreshInstallationStage(pending)
}

func cleanFreshInstallationStage(pending pendingInstallation) error {
	id, err := directoryIdentity(pending.Stage)
	if !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return err
		}
		if id != pending.ID {
			return errors.New("the installer staging directory was replaced")
		}
		if _, err := authenticatedFreshSession(pending); err == nil {
			entries, err := os.ReadDir(pending.Stage)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.Name() != "session.json" {
					if err := os.RemoveAll(filepath.Join(pending.Stage, entry.Name())); err != nil {
						return err
					}
				}
			}
			if err := os.Remove(filepath.Join(pending.Stage, "session.json")); err != nil {
				return err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// After the receipt is removed, only removal of the empty directory is allowed.
		if err := os.Remove(pending.Stage); err != nil {
			return err
		}
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, freshRecoveryKey, registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.DeleteValue("Pending"); err != nil {
		return err
	}
	return flushInstallRegistry(key)
}

func publishFreshInstallation(session installSession) error {
	fresh := session.Fresh
	if !fresh.AppVerified {
		return errors.New("the installed App has not been verified")
	}
	if err := recordInstallDirectory(session.App, &fresh.AppID, session.Owner); err != nil {
		return err
	}
	if err := recordInstallDirectory(session.Data, &fresh.DataID, session.Owner); err != nil {
		return err
	}
	if err := validateOwnedData(session.Data); err != nil {
		return err
	}
	for _, entry := range fresh.Payload {
		if err := checkFreshFile(filepath.Join(session.App, filepath.FromSlash(entry.Path)), entry, false); err != nil {
			return err
		}
	}
	if err := visitFreshRegistration(session, true, false); err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, storagepath.DataKey, registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer key.Close()
	if _, _, err := key.GetValue("DataRoot", nil); !errors.Is(err, registry.ErrNotExist) {
		return errors.New("a data location was registered while setup was running")
	}
	if err := key.SetStringValue("DataRoot", session.Data); err != nil {
		return err
	}
	return flushInstallRegistry(key)
}

func visitFreshRegistration(session installSession, published, restore bool) error {
	fresh := session.Fresh
	for _, reg := range []struct {
		path               string
		expected, original map[string]any
	}{
		{storagepath.InstallKey, installRegistryValues(fresh.Program, nil), installRegistryValues(session.Program, nil)},
		{uninstallKey, installRegistryValues(fresh.Uninstall, fresh.UninstallNumbers),
			installRegistryValues(session.Uninstall, session.UninstallNumbers)},
	} {
		access := uint32(registry.QUERY_VALUE | registry.WOW64_64KEY)
		if restore {
			access |= registry.SET_VALUE
		}
		key, err := registry.OpenKey(registry.CURRENT_USER, reg.path, access)
		if errors.Is(err, registry.ErrNotExist) && !published {
			continue
		}
		if err != nil {
			return err
		}
		err = reconcileFreshRegistryKey(key, reg.expected, reg.original, published, restore)
		key.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func installRegistryValues(text map[string]string, numbers map[string]uint64) map[string]any {
	result := make(map[string]any, len(text)+len(numbers))
	for name, value := range text {
		result[name] = value
	}
	for name, value := range numbers {
		result[name] = value
	}
	return result
}

func reconcileFreshRegistryKey(key registry.Key, expected, original map[string]any, published, restore bool) error {
	for name, want := range expected {
		var current any
		var err error
		if _, numeric := want.(uint64); numeric {
			value, kind, readErr := key.GetIntegerValue(name)
			if readErr == nil && kind != registry.DWORD {
				return fmt.Errorf("unexpected registry type for %s", name)
			}
			current, err = value, readErr
		} else {
			value, kind, readErr := key.GetStringValue(name)
			if readErr == nil && kind != registry.SZ {
				return fmt.Errorf("unexpected registry type for %s", name)
			}
			current, err = value, readErr
		}
		if errors.Is(err, registry.ErrNotExist) && !published {
			continue
		}
		if err != nil {
			return err
		}
		if !published && current == original[name] {
			continue
		}
		if current != want {
			return fmt.Errorf("program registration changed during setup: %s", name)
		}
		if !restore {
			continue
		}
		switch before := original[name].(type) {
		case string:
			err = key.SetStringValue(name, before)
		case uint64:
			err = key.SetDWordValue(name, uint32(before))
		default:
			err = key.DeleteValue(name)
		}
		if err != nil {
			return err
		}
	}
	if restore {
		return flushInstallRegistry(key)
	}
	return nil
}

type freshDirectoryCleanup struct {
	root     string
	id       installDirectoryID
	existed  bool
	expected map[string]payloadFile
	dirs     []string
	files    []string
}

func rollbackFreshInstall(session installSession) error {
	if err := visitFreshRegistration(session, false, false); err != nil {
		return err
	}
	fresh := session.Fresh
	app := freshDirectoryCleanup{root: session.App, id: fresh.AppID, existed: fresh.AppExisted, expected: make(map[string]payloadFile)}
	data := freshDirectoryCleanup{root: session.Data, id: fresh.DataID, existed: fresh.DataExisted, expected: make(map[string]payloadFile)}
	for _, entry := range fresh.Payload {
		app.expected[strings.ToLower(filepath.FromSlash(entry.Path))] = entry
	}
	app.expected[storagepath.AppMarker] = installMarkerEntry(storagepath.AppMarker, "")
	data.expected[storagepath.OwnerMarker] = installMarkerEntry(storagepath.OwnerMarker, session.Owner+"\n")
	for _, dir := range []*freshDirectoryCleanup{&app, &data} {
		if err := dir.inspect(session.Owner); err != nil {
			return err
		}
	}
	for _, dir := range []*freshDirectoryCleanup{&app, &data} {
		if err := dir.remove(); err != nil {
			return err
		}
	}
	if err := visitFreshRegistration(session, false, true); err != nil {
		return err
	}
	return nil
}

func installMarkerEntry(name, contents string) payloadFile {
	hash := sha256.Sum256([]byte(contents))
	return payloadFile{Path: name, Size: int64(len(contents)), SHA256: hex.EncodeToString(hash[:])}
}

func (dir *freshDirectoryCleanup) inspect(sid string) error {
	id, err := directoryIdentity(dir.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := privateOwner(dir.root, sid); err != nil {
		return err
	}
	if dir.id == (installDirectoryID{}) {
		// A crash may leave an empty directory created before the receipt
		// captured its identity. Preserve it and continue other cleanup.
		entries, err := os.ReadDir(dir.root)
		if err == nil && len(entries) == 0 {
			return nil
		}
		return fmt.Errorf("ownership of nonempty installation directory is unconfirmed: %s", dir.root)
	}
	if id != dir.id {
		return fmt.Errorf("installation directory was replaced: %s", dir.root)
	}
	allowedDirectories := map[string]bool{".": true}
	for name := range dir.expected {
		for parent := filepath.Dir(name); parent != "."; parent = filepath.Dir(parent) {
			allowedDirectories[parent] = true
		}
	}
	return filepath.WalkDir(dir.root, func(name string, entry fs.DirEntry, visitErr error) error {
		if visitErr != nil {
			return visitErr
		}
		if err := noReparsePath(name); err != nil {
			return err
		}
		relative, err := filepath.Rel(dir.root, name)
		if err != nil {
			return err
		}
		key := strings.ToLower(relative)
		if entry.IsDir() && allowedDirectories[key] {
			dir.dirs = append(dir.dirs, name)
			return nil
		}
		if expected, ok := dir.expected[key]; ok && !entry.IsDir() {
			if err := checkFreshFile(name, expected, false); err != nil {
				return err
			}
			dir.files = append(dir.files, relative)
			return nil
		}
		return fmt.Errorf("setup will not remove an unrecognized entry: %s", name)
	})
}

func (dir *freshDirectoryCleanup) remove() error {
	if len(dir.dirs) == 0 {
		return nil
	}
	if id, err := directoryIdentity(dir.root); err != nil || id != dir.id {
		return fmt.Errorf("installation directory changed before cleanup: %s", dir.root)
	}
	for _, name := range dir.files {
		if err := checkFreshFile(filepath.Join(dir.root, name), dir.expected[strings.ToLower(name)], true); err != nil {
			return err
		}
	}
	for i := len(dir.dirs) - 1; i >= 0; i-- {
		if i == 0 && dir.existed {
			continue
		}
		if err := noReparsePath(dir.dirs[i]); err != nil {
			return err
		}
		if err := os.Remove(dir.dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

func checkFreshFile(name string, expected payloadFile, remove bool) error {
	access := uint32(windows.GENERIC_READ)
	if remove {
		access |= windows.DELETE
	}
	file, err := openVerifiedFreshFile(name, expected, access)
	if err != nil {
		return err
	}
	defer file.Close()
	if remove {
		deleteFile := byte(1)
		return windows.SetFileInformationByHandle(windows.Handle(file.Fd()), windows.FileDispositionInfo, &deleteFile, 1)
	}
	return nil
}

func openVerifiedFreshFile(name string, expected payloadFile, access uint32) (_ *os.File, err error) {
	if err := noReparsePath(name); err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(windows.StringToUTF16Ptr(name), access, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), name)
	defer func() {
		if err != nil {
			file.Close()
		}
	}()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return nil, fmt.Errorf("installer payload is not a regular file: %s", name)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, expected.Size+1))
	if err != nil {
		return nil, err
	}
	if size != expected.Size || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return nil, fmt.Errorf("installer payload identity changed: %s", name)
	}
	return file, nil
}
