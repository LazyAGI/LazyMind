package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Keep the two Authenticode inputs read-only for the verifier. Installers may
// still rename either file. A directory notification stays signalled even if
// the original filename and bytes are subsequently restored.
func watchStartupIdentityInputs(paths RuntimePaths) (unchanged func() bool, release func(), err error) {
	bin := filepath.Join(paths.ResourcesRoot, "bin")
	install := filepath.Dir(filepath.Dir(paths.ResourcesRoot))
	if !filepath.IsAbs(bin) || len(filepath.VolumeName(bin)) != 2 {
		return nil, nil, fs.ErrInvalid
	}
	volume, err := windows.UTF16PtrFromString(filepath.VolumeName(bin) + `\`)
	if err != nil || windows.GetDriveType(volume) != windows.DRIVE_FIXED {
		return nil, nil, fs.ErrInvalid
	}
	var handles, changes []windows.Handle
	release = func() {
		for _, h := range changes {
			windows.FindCloseChangeNotification(h)
		}
		for _, h := range handles {
			windows.CloseHandle(h)
		}
	}
	cleanup := release
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	// Pin the real directory chain so the checked files cannot be redirected
	// through a moved directory or an existing junction. Other directory
	// operations (including writes) are shared; the volume root has no parent.
	var parents []string
	for dir := bin; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		parents = append(parents, dir)
	}
	for i := len(parents) - 1; i >= 0; i-- {
		h, e := openStartupIdentityHandle(parents[i], true)
		if e != nil {
			return nil, nil, e
		}
		handles = append(handles, h)
	}
	for _, dir := range []string{install, bin} {
		h, e := windows.FindFirstChangeNotification(dir, false, windows.FILE_NOTIFY_CHANGE_FILE_NAME|windows.FILE_NOTIFY_CHANGE_DIR_NAME)
		if e != nil {
			return nil, nil, e
		}
		changes = append(changes, h)
	}
	for _, path := range []string{filepath.Join(install, "LazyMind.exe"), filepath.Join(bin, "lazymind-update-helper.exe")} {
		h, e := openStartupIdentityHandle(path, false)
		if e != nil {
			return nil, nil, e
		}
		handles = append(handles, h)
	}
	unchanged = func() bool {
		for _, h := range changes {
			state, e := windows.WaitForSingleObject(h, 0)
			if e != nil || state != uint32(windows.WAIT_TIMEOUT) {
				return false
			}
		}
		return true
	}
	return unchanged, release, nil
}

func openStartupIdentityHandle(path string, directory bool) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	access := uint32(windows.GENERIC_READ)
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_DELETE)
	if directory {
		access = windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES
		share = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE
	}
	h, err := windows.CreateFile(name, access, share, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return 0, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		(info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		windows.CloseHandle(h)
		return 0, fs.ErrInvalid
	}
	return h, nil
}

// Match the two files inspected by verifyInstalledIdentity's Authenticode
// check. Matching bytes alone would miss a replacement by a different file.
func snapshotStartupIdentityInputs(ctx context.Context, paths RuntimePaths) ([32]byte, error) {
	var sum [32]byte
	resources := filepath.Dir(paths.ResourcesRoot)
	root := filepath.Dir(resources)
	if !filepath.IsAbs(root) || !strings.EqualFold(filepath.Base(resources), "resources") {
		return sum, fs.ErrInvalid
	}
	digest := sha256.New()
	for _, path := range []string{root, filepath.Join(root, "LazyMind.exe"), filepath.Join(paths.ResourcesRoot, "bin", "lazymind-update-helper.exe")} {
		if err := hashStartupIdentityObject(ctx, digest, path, path == root); err != nil {
			return sum, err
		}
	}
	digest.Sum(sum[:0])
	return sum, nil
}

func hashStartupIdentityObject(ctx context.Context, digest hash.Hash, path string, directory bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	handle, err := openStartupIdentityHandle(path, directory)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	io.WriteString(digest, path+"\x00")
	if err := binary.Write(digest, binary.LittleEndian, [...]uint32{
		info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow, info.FileSizeHigh, info.FileSizeLow,
	}); err != nil {
		return err
	}
	if directory {
		return nil
	}
	var buf [64 * 1024]byte
	return hashStartupIdentityBytes(ctx, digest, file, buf[:])
}
