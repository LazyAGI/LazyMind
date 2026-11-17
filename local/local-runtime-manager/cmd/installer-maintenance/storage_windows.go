package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/storagepath"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winfile"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winprocess"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const uninstallKey = "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\412e5ec7-d7ee-55e2-b3e8-1ea8840a484e"

type payloadFile struct {
	Path   string
	Size   int64
	SHA256 string
}

type payloadManifest struct {
	Version int
	Files   []payloadFile
}

func readPayloadManifest(name string) (payloadManifest, error) {
	var manifest payloadManifest
	if !filepath.IsAbs(name) {
		return manifest, errors.New("payload manifest must have an absolute path")
	}
	if err := noReparsePath(name); err != nil {
		return manifest, err
	}
	file, err := os.Open(name)
	if err != nil {
		return manifest, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return manifest, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return manifest, errors.New("invalid or oversized payload manifest")
	}
	decoder := json.NewDecoder(io.LimitReader(file, (64<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return manifest, errors.New("payload manifest has trailing content")
	}
	if manifest.Version != 1 || len(manifest.Files) == 0 || len(manifest.Files) > 250000 {
		return manifest, errors.New("unsupported payload manifest")
	}
	seen := map[string]bool{}
	for _, entry := range manifest.Files {
		name := entry.Path
		if name == "" || name == "." || name == ".." || path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:\x00\r\n\t") || entry.Size < 0 {
			return manifest, fmt.Errorf("unsafe payload entry: %q", name)
		}
		for _, component := range strings.Split(name, "/") {
			if strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
				return manifest, fmt.Errorf("ambiguous payload entry: %q", name)
			}
		}
		key := strings.ToLower(name)
		if seen[key] {
			return manifest, fmt.Errorf("duplicate payload entry: %q", name)
		}
		seen[key] = true
		hash, err := hex.DecodeString(entry.SHA256)
		if err != nil || len(hash) != sha256.Size || strings.ToLower(entry.SHA256) != entry.SHA256 {
			return manifest, fmt.Errorf("invalid payload checksum: %q", name)
		}
	}
	return manifest, nil
}

func verifyPayload(opts commandOptions) error {
	if !filepath.IsAbs(opts.PayloadDir) || !filepath.IsAbs(opts.Manifest) {
		return errors.New("payload verification requires absolute paths")
	}
	if err := noReparsePath(opts.PayloadDir); err != nil {
		return err
	}
	info, err := os.Stat(opts.PayloadDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("payload root is not a directory")
	}
	manifest, err := readPayloadManifest(opts.Manifest)
	if err != nil {
		return err
	}
	for _, entry := range manifest.Files {
		name := filepath.Join(opts.PayloadDir, filepath.FromSlash(entry.Path))
		if err := noReparsePath(name); err != nil {
			return err
		}
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != entry.Size {
			return fmt.Errorf("payload file size/type mismatch: %s", entry.Path)
		}
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		hash := sha256.New()
		length, readErr := io.Copy(hash, io.LimitReader(file, entry.Size+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if length != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return fmt.Errorf("payload checksum mismatch: %s", entry.Path)
		}
	}
	return nil
}

type installLayout struct {
	Mode, App, Data, Storage, Stage string
	Registered                      bool
}

// This receipt is temporary, private and limited to a single installer run.
// DataRoot remains the only additional persistent location setting.
type installSession struct {
	App, Data, Owner   string
	Program, Uninstall map[string]string
	UninstallNumbers   map[string]uint64
	PurgeComplete      bool
}

func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func readInstallString(keyPath, name string) (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE|registry.WOW64_64KEY)
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
	if kind != registry.SZ || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("invalid registered %s", name)
	}
	return filepath.Clean(value), nil
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func noReparsePath(name string) error {
	if !filepath.IsAbs(name) || strings.ContainsAny(name, "\x00\r\n\t") {
		return fmt.Errorf("invalid absolute path %q", name)
	}
	for _, component := range strings.Split(strings.TrimPrefix(filepath.Clean(name), filepath.VolumeName(name)), "\\") {
		if strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") || strings.Contains(component, ":") {
			return fmt.Errorf("ambiguous Windows path component: %q", component)
		}
	}
	for current := filepath.Clean(name); ; current = filepath.Dir(current) {
		attrs, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(current))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("reparse-point path is not supported: %s", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return nil
}

func physicalStoragePath(name string) (string, error) {
	current, suffix := filepath.Clean(name), ""
	for {
		if _, err := os.Stat(current); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("storage volume is unavailable")
		}
		suffix = filepath.Join(filepath.Base(current), suffix)
		current = parent
	}
	handle, err := windows.CreateFile(windows.StringToUTF16Ptr(current), windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", err
	}
	if length >= uint32(len(buffer)) {
		return "", errors.New("resolved storage path is too long")
	}
	return filepath.Join(strings.TrimPrefix(windows.UTF16ToString(buffer[:length]), "\\\\?\\"), suffix), nil
}

func storageVolume(name string) error {
	if err := requireReliableLocalVolume(name); err != nil {
		return err
	}
	root := filepath.VolumeName(name) + "\\"
	if windows.GetDriveType(windows.StringToUTF16Ptr(root)) != windows.DRIVE_FIXED {
		return errors.New("storage requires a local fixed disk")
	}
	var fsName [32]uint16
	if err := windows.GetVolumeInformation(windows.StringToUTF16Ptr(root), nil, 0, nil, nil, nil, &fsName[0], uint32(len(fsName))); err != nil {
		return err
	}
	if !strings.EqualFold(windows.UTF16ToString(fsName[:]), "NTFS") {
		return errors.New("storage requires NTFS")
	}
	return noReparsePath(name)
}

func privateOwner(name, sid string) error {
	if err := noReparsePath(name); err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if owner == nil || owner.String() != sid {
		return fmt.Errorf("path is not owned by the current user: %s", name)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return fmt.Errorf("unrestricted ACL: %s", name)
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return err
		}
		// Inherit-only entries grant no rights on this object.
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			if ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
				return fmt.Errorf("unsupported ACL entry: %s", name)
			}
			continue
		}
		trustee := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if ace.Mask != 0 && trustee != sid && trustee != "S-1-5-18" && trustee != "S-1-5-32-544" {
			return fmt.Errorf("another user has access to storage: %s", name)
		}
	}
	return nil
}

func createPrivateDirectory(name, sid string) error {
	if err := noReparsePath(name); err != nil {
		return err
	}
	if info, err := os.Stat(name); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("storage path is not a directory: %s", name)
		}
		if err := privateOwner(name, sid); err == nil {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(name)
	if _, err := os.Stat(parent); os.IsNotExist(err) {
		if err := createPrivateDirectory(parent, sid); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(windows.StringToUTF16Ptr(name), &attributes); err == nil {
		return privateOwner(name, sid)
	} else if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	// Browse may create an empty folder first. Hold its namespace stable while
	// verifying ownership and restricting only that same empty directory.
	handle, err := windows.CreateFile(windows.StringToUTF16Ptr(name), windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.WRITE_DAC, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return errors.New("storage folder was replaced")
	}
	actual, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := actual.Owner()
	if err != nil || owner == nil || owner.String() != sid {
		return fmt.Errorf("existing storage has another owner: %s", name)
	}
	entries, err := os.ReadDir(name)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("existing storage is not private and empty: %s", name)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return err
	}
	return privateOwner(name, sid)
}

func safeStorageAncestors(root, sid string) error {
	trusted := func(value string) bool {
		return value == sid || value == "S-1-5-18" || value == "S-1-5-32-544" || strings.HasPrefix(value, "S-1-5-80-")
	}
	for name := filepath.Dir(root); ; name = filepath.Dir(name) {
		if _, err := os.Stat(name); err == nil {
			sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				return err
			}
			owner, _, err := sd.Owner()
			if err != nil {
				return err
			}
			if owner == nil || !trusted(owner.String()) {
				return fmt.Errorf("storage ancestor has another user as owner: %s", name)
			}
			dacl, _, err := sd.DACL()
			if err != nil {
				return err
			}
			if dacl == nil {
				return fmt.Errorf("storage ancestor has an unrestricted ACL: %s", name)
			}
			dangerous := windows.ACCESS_MASK(0x40 /* FILE_DELETE_CHILD; absent in x/sys v0.30 */ | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_ALL)
			if filepath.Dir(name) != name {
				dangerous |= windows.DELETE
			}
			for index := uint32(0); index < uint32(dacl.AceCount); index++ {
				var ace *windows.ACCESS_ALLOWED_ACE
				if err := windows.GetAce(dacl, index, &ace); err != nil {
					return err
				}
				if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
					continue
				}
				if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
					return fmt.Errorf("unsupported ancestor ACL: %s", name)
				}
				trustee := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
				if !trusted(trustee) && trustee != "S-1-3-0" && trustee != "S-1-3-4" && ace.Mask&dangerous != 0 {
					return fmt.Errorf("storage ancestor ACL cannot guarantee user isolation: %s", name)
				}
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(name) == name {
			return nil
		}
	}
}

func safeStorageRoot(root string) error {
	if err := storageVolume(root); err != nil {
		return err
	}
	resolved, err := physicalStoragePath(root)
	if err != nil {
		return err
	}
	if filepath.Dir(resolved) == resolved {
		return errors.New("a drive root cannot be application storage")
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	if err := safeStorageAncestors(root, sid); err != nil {
		return err
	}
	for _, folder := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Windows, windows.FOLDERID_ProgramFiles, windows.FOLDERID_ProgramFilesX86, windows.FOLDERID_ProgramData} {
		base, err := windows.KnownFolderPath(folder, 0)
		if err == nil {
			if physical, err := physicalStoragePath(base); err == nil {
				base = physical
			}
		}
		if err == nil && (samePath(resolved, base) || strings.HasPrefix(strings.ToLower(resolved), strings.ToLower(filepath.Clean(base))+string(filepath.Separator))) {
			return fmt.Errorf("system directory cannot be application storage: %s", root)
		}
	}
	for _, folder := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Profile, windows.FOLDERID_LocalAppData, windows.FOLDERID_RoamingAppData, windows.FOLDERID_Documents, windows.FOLDERID_Desktop, windows.FOLDERID_Downloads} {
		base, err := windows.KnownFolderPath(folder, 0)
		if err == nil {
			if physical, err := physicalStoragePath(base); err == nil {
				base = physical
			}
		}
		if err == nil && samePath(resolved, base) {
			return fmt.Errorf("choose a dedicated application subdirectory: %s", root)
		}
	}
	return nil
}

func validateOwnedData(data string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Base(data), "Data") {
		return errors.New("new storage root must be the fixed Data child")
	}
	if err := safeStorageRoot(filepath.Dir(data)); err != nil {
		return err
	}
	for _, name := range []string{filepath.Dir(data), data, filepath.Join(data, storagepath.OwnerMarker)} {
		if err := privateOwner(name, sid); err != nil {
			return err
		}
	}
	marker, err := os.ReadFile(filepath.Join(data, storagepath.OwnerMarker))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(marker)) != sid {
		return errors.New("Data ownership marker does not match the current user")
	}
	return nil
}

func writeAppFlag(app, sid string) error {
	name := filepath.Join(app, storagepath.AppMarker)
	if err := noReparsePath(name); err != nil {
		return err
	}
	if info, err := os.Lstat(name); err == nil {
		if !info.Mode().IsRegular() || info.Size() != 0 {
			return errors.New("App installation flag is damaged")
		}
		return privateOwner(name, sid)
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return file.Close()
}

func meaningfulLegacyData(root string) (bool, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), "logs") {
			continue
		}
		if entry.IsDir() {
			children, err := os.ReadDir(filepath.Join(root, entry.Name()))
			if err != nil {
				return false, err
			}
			if len(children) > 0 {
				return true, nil
			}
		} else {
			return true, nil
		}
	}
	return false, nil
}

func planInstallation(opts commandOptions, final bool) (installLayout, error) {
	legacy, err := localAppDataRoot()
	if err != nil {
		return installLayout{}, err
	}
	data, modern, err := storagepath.Resolve("")
	if err != nil {
		return installLayout{}, err
	}
	registered, err := readInstallString(storagepath.InstallKey, "InstallLocation")
	if err != nil {
		return installLayout{}, err
	}
	uninstaller, err := readInstallString(uninstallKey, "UninstallString")
	if err != nil {
		return installLayout{}, err
	}
	layout := installLayout{Registered: registered != "" || uninstaller != ""}
	if modern {
		if err := validateOwnedData(data); err != nil {
			return layout, err
		}
		layout.Data, layout.App, layout.Storage = data, filepath.Join(filepath.Dir(data), "App"), filepath.Dir(data)
		layout.Mode = "restore"
		if registered != "" {
			layout.Mode = "installed"
			if _, err := trustedInstallDir(legacy, registered); err != nil {
				return layout, err
			}
		}
	} else {
		hasData, err := meaningfulLegacyData(legacy)
		if err != nil {
			return layout, err
		}
		legacyApp := filepath.Join(filepath.Dir(legacy), "Programs", appDataLeaf)
		_, appErr := os.Stat(filepath.Join(legacyApp, "LazyMind.exe"))
		if registered != "" || uninstaller != "" || hasData || appErr == nil {
			layout.Mode, layout.App, layout.Data, layout.Storage = "legacy", legacyApp, legacy, legacyApp
			if registered != "" && !samePath(registered, legacyApp) {
				return layout, errors.New("legacy installation is outside its supported registered directory")
			}
		} else {
			root := filepath.Clean(opts.InstallDir)
			if final {
				if !strings.EqualFold(filepath.Base(root), "App") {
					return layout, errors.New("installation must use the fixed App child")
				}
				root = filepath.Dir(root)
			}
			layout.Mode, layout.Storage, layout.App, layout.Data = "new", root, filepath.Join(root, "App"), filepath.Join(root, "Data")
		}
	}
	if final && !samePath(opts.InstallDir, layout.App) {
		return layout, errors.New("installation conflicts with the retained program location")
	}
	if opts.RequestedDir != "" && layout.Mode != "new" && !samePath(opts.RequestedDir, layout.App) {
		return layout, errors.New("upgrades and retained-data reinstalls cannot change installation directory")
	}
	return layout, nil
}

func writeLayoutFile(name string, layout installLayout) error {
	if !filepath.IsAbs(name) {
		return errors.New("layout output must be an absolute temporary file")
	}
	if err := noReparsePath(name); err != nil {
		return err
	}
	content := fmt.Sprintf("[layout]\r\nmode=%s\r\napp=%s\r\ndata=%s\r\nstorage=%s\r\nstage=%s\r\nregistered=%t\r\n", layout.Mode, layout.App, layout.Data, layout.Storage, layout.Stage, layout.Registered)
	encoded := []byte{0xff, 0xfe}
	for _, value := range utf16.Encode([]rune(content)) {
		encoded = binary.LittleEndian.AppendUint16(encoded, value)
	}
	return os.WriteFile(name, encoded, 0600)
}

func prepareInstallation(opts commandOptions) error {
	layout, err := planInstallation(opts, true)
	if err != nil {
		return err
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	if layout.Mode == "new" {
		if err := safeStorageRoot(layout.Storage); err != nil {
			return err
		}
		// Never adopt an existing nonempty App/Data without the locator.
		for _, name := range []string{layout.App, layout.Data} {
			if entries, err := os.ReadDir(name); err == nil && len(entries) != 0 {
				return fmt.Errorf("unregistered data/program files already exist: %s", name)
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := createPrivateDirectory(layout.Storage, sid); err != nil {
			return err
		}
	}
	stageParent := filepath.Join(filepath.Dir(layout.App), ".install-tmp")
	if err := createPrivateDirectory(stageParent, sid); err != nil {
		return err
	}
	layout.Stage, err = os.MkdirTemp(stageParent, "install-")
	if err != nil {
		return err
	}
	// This directory is still empty and unique to this attempt. Path rejection
	// removes only it, never any retained program or data.
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.Remove(layout.Stage)
		}
	}()
	opts.StageDir = layout.Stage
	if err := installerPreflight(opts); err != nil {
		return err
	}
	if layout.Mode != "legacy" {
		if layout.Mode == "restore" {
			if entries, err := os.ReadDir(layout.App); err == nil && len(entries) != 0 {
				if _, err := os.Stat(filepath.Join(layout.App, storagepath.AppMarker)); err != nil {
					return errors.New("retained App directory contains unmarked files; repair explicitly")
				}
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		for _, name := range []string{layout.App, layout.Data} {
			if err := createPrivateDirectory(name, sid); err != nil {
				return err
			}
			if err := ensureWritableDirectory(name); err != nil {
				return err
			}
		}
		if layout.Mode == "new" {
			if err := os.WriteFile(filepath.Join(layout.Data, storagepath.OwnerMarker), []byte(sid+"\n"), 0600); err != nil {
				return err
			}
			if err := validateOwnedData(layout.Data); err != nil {
				return err
			}
			key, _, err := registry.CreateKey(registry.CURRENT_USER, storagepath.DataKey, registry.SET_VALUE|registry.WOW64_64KEY)
			if err != nil {
				return err
			}
			err = key.SetStringValue("DataRoot", layout.Data)
			key.Close()
			if err != nil {
				return err
			}
		}
		if err := writeAppFlag(layout.App, sid); err != nil {
			return err
		}
	}
	if err := createSession(layout); err != nil {
		return err
	}
	if err := writeLayoutFile(opts.Output, layout); err != nil {
		return err
	}
	keepStage = true
	return nil
}

func createSession(layout installLayout) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	session := installSession{App: layout.App, Data: layout.Data, Owner: sid, Program: map[string]string{}, Uninstall: map[string]string{}, UninstallNumbers: map[string]uint64{}}
	for path, values := range map[string]map[string]string{storagepath.InstallKey: session.Program, uninstallKey: session.Uninstall} {
		key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		names := []string{"InstallLocation", "KeepShortcuts", "ShortcutName", "MenuDirectory"}
		if path == uninstallKey {
			names = []string{"DisplayName", "UninstallString", "QuietUninstallString", "DisplayVersion", "DisplayIcon", "Publisher"}
		}
		for _, name := range names {
			if value, _, err := key.GetStringValue(name); err == nil {
				values[name] = value
			} else if !errors.Is(err, registry.ErrNotExist) {
				key.Close()
				return err
			}
		}
		if path == uninstallKey {
			for _, name := range []string{"NoModify", "NoRepair", "EstimatedSize"} {
				if value, _, err := key.GetIntegerValue(name); err == nil {
					session.UninstallNumbers[name] = value
				}
			}
		}
		key.Close()
	}
	content, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(layout.Stage, "session.json"), content, 0600)
}

func loadPrivateSession(stage, app string) (installSession, error) {
	var session installSession
	sid, err := currentUserSID()
	if err != nil {
		return session, err
	}
	expectedParent := filepath.Join(filepath.Dir(filepath.Clean(app)), ".install-tmp")
	if !filepath.IsAbs(stage) || !samePath(filepath.Dir(stage), expectedParent) || !strings.HasPrefix(filepath.Base(stage), "install-") {
		return session, errors.New("untrusted installation staging path")
	}
	for _, name := range []string{expectedParent, stage, filepath.Join(stage, "session.json")} {
		if err := privateOwner(name, sid); err != nil {
			return session, err
		}
	}
	content, err := os.ReadFile(filepath.Join(stage, "session.json"))
	if err != nil {
		return session, err
	}
	if err := json.Unmarshal(content, &session); err != nil {
		return session, err
	}
	if session.Owner != sid || !samePath(session.App, app) {
		return session, errors.New("installation session belongs to another user or program")
	}
	return session, nil
}

func readSession(stage, app string) (installSession, error) {
	session, err := loadPrivateSession(stage, app)
	if err != nil {
		return session, err
	}
	if session.PurgeComplete {
		return session, errors.New("this session already completed data removal")
	}
	data, modern, err := storagepath.Resolve("")
	if err != nil {
		return session, err
	}
	if modern {
		if !samePath(data, session.Data) || !samePath(app, filepath.Join(filepath.Dir(data), "App")) {
			return session, errors.New("staging session conflicts with retained storage")
		}
	} else {
		legacy, err := localAppDataRoot()
		if err != nil {
			return session, err
		}
		if !samePath(session.Data, legacy) || !samePath(app, filepath.Join(filepath.Dir(legacy), "Programs", appDataLeaf)) {
			return session, errors.New("staging session is not a legacy installation")
		}
	}
	return session, nil
}

func completePurgeSession(opts commandOptions) error {
	if err := verifyUninstalled(); err != nil {
		return err
	}
	session, err := loadPrivateSession(opts.StageDir, opts.InstallDir)
	if err != nil {
		return err
	}
	if !session.PurgeComplete {
		return errors.New("late purge cannot authorize data deletion")
	}
	legacy, err := localAppDataRoot()
	if err != nil {
		return err
	}
	modern := samePath(session.App, filepath.Join(filepath.Dir(session.Data), "App")) && strings.EqualFold(filepath.Base(session.Data), "Data")
	if modern {
		if err := safeStorageRoot(filepath.Dir(session.Data)); err != nil {
			return err
		}
		if err := privateOwner(filepath.Dir(session.Data), session.Owner); err != nil {
			return err
		}
		value, err := readInstallString(storagepath.DataKey, "DataRoot")
		if err != nil || value != "" {
			return errors.New("data locator was not removed by this purge")
		}
	} else if !samePath(session.Data, legacy) || !samePath(session.App, filepath.Join(filepath.Dir(legacy), "Programs", appDataLeaf)) {
		return errors.New("completed purge session has an untrusted target")
	}
	if _, err := os.Lstat(session.Data); !os.IsNotExist(err) {
		return errors.New("late purge requires the original data target to be absent")
	}
	// No process handling and no data deletion remain at this late hook.
	return os.RemoveAll(opts.StageDir)
}

func verifyUninstalled() error {
	for key, name := range map[string]string{storagepath.InstallKey: "InstallLocation", uninstallKey: "UninstallString"} {
		value, err := readInstallString(key, name)
		if err != nil {
			return err
		}
		if value != "" {
			return errors.New("program registration was not removed; inspect the partial uninstall before retrying")
		}
	}
	return nil
}

func markInstallation(opts commandOptions) error {
	session, err := readSession(opts.StageDir, opts.InstallDir)
	if err != nil {
		return err
	}
	data, modern, err := storagepath.Resolve("")
	if err != nil {
		return err
	}
	if modern {
		if !samePath(data, session.Data) || !samePath(filepath.Join(filepath.Dir(data), "App"), opts.InstallDir) {
			return errors.New("installation session conflicts with DataRoot")
		}
		if err := validateOwnedData(data); err != nil {
			return err
		}
		if err := leaveAppForMutation(opts.InstallDir, opts.StageDir); err != nil {
			return err
		}
		registered, err := readInstallString(storagepath.InstallKey, "InstallLocation")
		if err != nil {
			return err
		}
		if registered == "" {
			entries, err := os.ReadDir(opts.InstallDir)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			hasPartialFiles := false
			for _, entry := range entries {
				if entry.Name() != storagepath.AppMarker {
					hasPartialFiles = true
				}
			}
			if hasPartialFiles {
				if err := privateOwner(opts.InstallDir, session.Owner); err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(context.Background(), processScanLimit)
				processes, scanErr := winprocess.Snapshot(ctx)
				cancel()
				if scanErr != nil {
					return scanErr
				}
				for _, process := range processes {
					if executableMatchesRoots(winprocess.Text(process.ExecutablePath), compactRoots(opts.InstallDir)) {
						return errors.New("retained unregistered App is running; close it before reinstalling")
					}
				}
				if err := os.Rename(opts.InstallDir, filepath.Join(opts.StageDir, "old-app")); err != nil {
					return err
				}
			}
		}
		if err := createPrivateDirectory(opts.InstallDir, session.Owner); err != nil {
			return err
		}
		return writeAppFlag(opts.InstallDir, session.Owner)
	}
	return nil
}

func removeProgram(opts commandOptions) error {
	legacy, err := localAppDataRoot()
	if err != nil {
		return err
	}
	if _, err := trustedInstallDir(legacy, opts.InstallDir); err != nil {
		return err
	}
	root, modern, err := storagepath.Resolve("")
	if err != nil {
		return err
	}
	if !modern {
		root = legacy
	}
	if _, err := forceStop(root, opts.InstallDir); err != nil {
		return err
	}
	stage := opts.StageDir
	if stage == "" {
		sid, err := currentUserSID()
		if err != nil {
			return err
		}
		parent := filepath.Join(filepath.Dir(opts.InstallDir), ".install-tmp")
		if err := createPrivateDirectory(parent, sid); err != nil {
			return err
		}
		stage, err = os.MkdirTemp(parent, "install-")
		if err != nil {
			return err
		}
		if err := createSession(installLayout{App: opts.InstallDir, Data: root, Stage: stage}); err != nil {
			return err
		}
	}
	if _, err := readSession(stage, opts.InstallDir); err != nil {
		return err
	}
	old := filepath.Join(stage, "old-app")
	if _, err := os.Stat(old); err == nil {
		return errors.New("old program backup already exists; inspect it before retrying")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := noReparsePath(opts.InstallDir); err != nil {
		return err
	}
	if err := leaveAppForMutation(opts.InstallDir, stage); err != nil {
		return err
	}
	if err := winfile.RetryOperation(context.Background(), func() error { return os.Rename(opts.InstallDir, old) }, winfile.RetryOptions{MaxWait: purgeRetryLimit}); err != nil {
		return err
	}
	if opts.Upgrade {
		return nil
	}
	if opts.Purge {
		if err := purgeLocalData(context.Background(), root); err != nil {
			_ = os.Rename(old, opts.InstallDir)
			return err
		}
		if modern {
			key, err := registry.OpenKey(registry.CURRENT_USER, storagepath.DataKey, registry.SET_VALUE|registry.WOW64_64KEY)
			if err != nil {
				return err
			}
			err = key.DeleteValue("DataRoot")
			key.Close()
			if err != nil {
				return err
			}
		}
		session, err := loadPrivateSession(stage, opts.InstallDir)
		if err != nil {
			return err
		}
		session.PurgeComplete = true
		content, err := json.Marshal(session)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, "session.json"), content, 0600); err != nil {
			return err
		}
		return writeLayoutFile(opts.Output, installLayout{App: opts.InstallDir, Data: root, Stage: stage})
	}
	return os.RemoveAll(stage)
}

func finishInstallation(opts commandOptions, rollback bool) error {
	session, err := readSession(opts.StageDir, opts.InstallDir)
	if err != nil {
		return err
	}
	if err := leaveAppForMutation(opts.InstallDir, opts.StageDir); err != nil {
		return err
	}
	old := filepath.Join(opts.StageDir, "old-app")
	if !rollback {
		legacy, err := localAppDataRoot()
		if err != nil {
			return err
		}
		if _, err := trustedInstallDir(legacy, opts.InstallDir); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(opts.InstallDir, "LazyMind.exe")); err != nil {
			return err
		}
		if err := os.RemoveAll(opts.StageDir); err != nil {
			// Program replacement succeeded and no service has started yet.
			// Cleanup failure must not roll back a partially removed backup.
			fmt.Fprintf(os.Stderr, "Program installed; temporary cleanup deferred at %s: %v\n", opts.StageDir, err)
		}
		return nil
	}
	if rollback {
		if _, err := os.Stat(old); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		if err := noReparsePath(opts.InstallDir); err != nil {
			return err
		}
		if err := os.RemoveAll(opts.InstallDir); err != nil {
			return err
		}
		if err := os.Rename(old, opts.InstallDir); err != nil {
			return err
		}
		for path, values := range map[string]map[string]string{storagepath.InstallKey: session.Program, uninstallKey: session.Uninstall} {
			if len(values) == 0 && (path != uninstallKey || len(session.UninstallNumbers) == 0) {
				continue
			}
			key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE|registry.WOW64_64KEY)
			if err != nil {
				return err
			}
			for name, value := range values {
				if err := key.SetStringValue(name, value); err != nil {
					key.Close()
					return err
				}
			}
			if path == uninstallKey {
				for name, value := range session.UninstallNumbers {
					if err := key.SetDWordValue(name, uint32(value)); err != nil {
						key.Close()
						return err
					}
				}
			}
			key.Close()
		}
	}
	return os.RemoveAll(opts.StageDir)
}

func leaveAppForMutation(app, stage string) error {
	if !filepath.IsAbs(app) || filepath.Dir(app) == app {
		return errors.New("invalid App mutation target")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = physicalStoragePath(executable)
	if err != nil {
		return err
	}
	for _, target := range []string{app, stage} {
		if target != "" {
			target, err = physicalStoragePath(target)
			if err != nil {
				return err
			}
		}
		if target != "" && (samePath(executable, target) || strings.HasPrefix(strings.ToLower(filepath.Clean(executable)), strings.ToLower(filepath.Clean(target))+string(filepath.Separator))) {
			return errors.New("maintenance executable must run outside App and its staging directory")
		}
	}
	parent := filepath.Dir(app)
	if err := noReparsePath(parent); err != nil {
		return err
	}
	// This releases a helper cwd inherited from a command-line caller as well
	// as NSIS. NSIS itself must independently release its cwd before launching us.
	return os.Chdir(parent)
}

func processOwnedByCurrentUser(pid uint32) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(handle)
	return handleOwnedByCurrentUser(handle)
}

func handleOwnedByCurrentUser(handle windows.Handle) (bool, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return false, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	sid, sidErr := currentUserSID()
	if err != nil {
		return false, err
	}
	if sidErr != nil {
		return false, sidErr
	}
	return user.User.Sid.String() == sid, nil
}

// Keep termination tied to the same verified handle and creation identity.
// taskkill /T would also select children belonging to a different Windows user.
func stopCurrentUserTree(ctx context.Context, pid int, expected uint64) error {
	inventory, err := winprocess.Snapshot(ctx)
	if err != nil {
		return err
	}
	var root winprocess.Info
	children := map[uint32][]winprocess.Info{}
	for _, info := range inventory {
		children[info.ParentProcessID] = append(children[info.ParentProcessID], info)
		if info.ProcessID == uint32(pid) {
			root = info
		}
	}
	if root.ProcessID == 0 {
		return nil
	}
	if expected == 0 || root.StartID != expected {
		return winprocess.ErrProcessIdentityChanged
	}
	selected := []winprocess.Info{root}
	seen := map[uint32]bool{root.ProcessID: true}
	for index := 0; index < len(selected); index++ {
		parent := selected[index]
		for _, child := range children[parent.ProcessID] {
			if !seen[child.ProcessID] && child.StartID >= parent.StartID {
				selected = append(selected, child)
				seen[child.ProcessID] = true
			}
		}
	}
	handles := make([]windows.Handle, 0, len(selected))
	defer func() {
		for _, handle := range handles {
			windows.CloseHandle(handle)
		}
	}()
	for _, info := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, info.ProcessID)
		if err != nil {
			if !winprocess.Alive(int(info.ProcessID)) {
				continue
			}
			return err
		}
		handles = append(handles, handle)
		var created, exited, kernel, user windows.Filetime
		if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
			return err
		}
		if info.StartID == 0 || uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != info.StartID {
			return winprocess.ErrProcessIdentityChanged
		}
		owned, err := handleOwnedByCurrentUser(handle)
		if err != nil {
			return err
		}
		if !owned {
			return errors.New("process tree includes another Windows user; close it before setup")
		}
	}
	// Stop the parent first so it cannot continuously respawn captured children.
	for _, handle := range handles {
		if err := ctx.Err(); err != nil {
			return err
		}
		var code uint32
		if err := windows.GetExitCodeProcess(handle, &code); err != nil {
			return err
		}
		if code == 259 {
			if err := windows.TerminateProcess(handle, 1); err != nil {
				return err
			}
			if result, err := windows.WaitForSingleObject(handle, 1000); err != nil || result == uint32(windows.WAIT_TIMEOUT) {
				return errors.New("owned process did not terminate promptly")
			}
		}
	}
	return nil
}

// Keep the old location policy for legacy callers, while requiring the actual
// fixed product registration before a new App can authorize process handling.
func authenticateInstallDir(legacyRoot, candidate string) (string, error) {
	if strings.TrimSpace(candidate) == "" {
		return "", nil
	}
	data, modern, err := storagepath.Resolve("")
	if err != nil {
		return "", err
	}
	if modern {
		if err := validateOwnedData(data); err != nil {
			return "", err
		}
		expected := filepath.Join(filepath.Dir(data), "App")
		registered, err := readInstallString(storagepath.InstallKey, "InstallLocation")
		if err != nil {
			return "", err
		}
		if registered == "" || !samePath(candidate, expected) || !samePath(registered, expected) {
			return "", errors.New("candidate is not the registered App")
		}
		sid, err := currentUserSID()
		if err != nil {
			return "", err
		}
		for _, name := range []string{expected, filepath.Join(expected, storagepath.AppMarker)} {
			if err := privateOwner(name, sid); err != nil {
				return "", err
			}
		}
		flag, err := os.ReadFile(filepath.Join(expected, storagepath.AppMarker))
		if err != nil || len(flag) != 0 {
			return "", errors.New("registered App has no valid installation flag")
		}
		return expected, nil
	}
	expected := filepath.Join(filepath.Dir(legacyRoot), "Programs", appDataLeaf)
	if !filepath.IsAbs(legacyRoot) || !strings.EqualFold(filepath.Base(legacyRoot), appDataLeaf) || !samePath(candidate, expected) {
		return "", fmt.Errorf("untrusted legacy installation directory %q", candidate)
	}
	if err := noReparsePath(expected); err != nil {
		return "", err
	}
	if _, err := os.Stat(expected); err == nil {
		sid, err := currentUserSID()
		if err != nil {
			return "", err
		}
		if err := privateOwner(expected, sid); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return expected, nil
}
