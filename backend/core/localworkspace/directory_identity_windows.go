//go:build windows

package localworkspace

import (
	"fmt"
	"os"
	"syscall"
)

func platformDirectoryIdentity(path string, _ os.FileInfo) (string, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := syscall.CreateFile(ptr, 0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(handle)
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	index := uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	return fmt.Sprintf("fsid:windows:%d:%d", info.VolumeSerialNumber, index), nil
}
