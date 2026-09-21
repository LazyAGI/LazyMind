//go:build windows

package localworkspace

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func platformDirectoryIdentity(path string, info os.FileInfo) (string, error) {
	before, ok := info.(windowsIdentityObservation)
	if !ok {
		return "", os.ErrInvalid
	}
	current, err := observeIdentityPath(path)
	if err != nil {
		return "", err
	}
	after := current.(windowsIdentityObservation)
	if after.Mode()&os.ModeSymlink != 0 || before.identity != after.identity {
		return "", os.ErrInvalid
	}
	return after.identity, nil
}

type windowsIdentityObservation struct {
	os.FileInfo
	identity string
}

// Capture all 128 bits at observation time. os.SameFile uses the legacy
// 64-bit index and may lazily reopen a path, so it cannot pin this observation.
func observeIdentityPath(path string) (os.FileInfo, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(ptr, 0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, os.ErrInvalid
	}
	identity, err := openedDirectoryIdentity(file)
	if err != nil {
		return nil, err
	}
	return windowsIdentityObservation{FileInfo: info, identity: identity}, nil
}

func openedDirectoryIdentity(file *os.File) (string, error) {
	var info struct {
		VolumeSerialNumber uint64
		FileID             [16]byte
	}
	if err := windows.GetFileInformationByHandleEx(windows.Handle(file.Fd()), windows.FileIdInfo,
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return "", fmt.Errorf("read persistent file identity: %w", err)
	}
	if info.FileID == [16]byte{} {
		return "", fmt.Errorf("persistent file identity unavailable: %w", os.ErrInvalid)
	}
	return windowsPersistentIdentity(info.VolumeSerialNumber, info.FileID), nil
}

func windowsPersistentIdentity(volume uint64, fileID [16]byte) string {
	return fmt.Sprintf("fsid:windows:v2:%016x:%x", volume, fileID)
}
