package localworkspace

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func platformDirectoryIdentity(path string, info os.FileInfo) (string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	return checkedOpenedIdentity(path, info, file)
}

func openedDirectoryIdentity(file *os.File) (string, error) {
	// Darwin attrlist: five attribute bitmaps. ATTR_VOL_INFO | ATTR_VOL_UUID
	// returns a length-prefixed 16-byte UUID, independent of mount device IDs.
	attrs := struct {
		Count, Reserved                       uint16
		Common, Volume, Directory, File, Fork uint32
	}{Count: 5, Volume: 0x80000000 | 0x00040000}
	result := struct {
		Length uint32
		UUID   [16]byte
	}{}
	_, _, errno := syscall.Syscall6(syscall.SYS_FGETATTRLIST, file.Fd(),
		uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&result)), unsafe.Sizeof(result), 0, 0)
	runtime.KeepAlive(file)
	if errno != 0 {
		return "", fmt.Errorf("read persistent volume identity: %w", errno)
	}
	if result.Length != uint32(unsafe.Sizeof(result)) || result.UUID == [16]byte{} {
		return "", fmt.Errorf("persistent volume identity unavailable: %w", os.ErrInvalid)
	}
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return "", os.ErrInvalid
	}
	return darwinPersistentIdentity(result.UUID, stat), nil
}

func darwinPersistentIdentity(volume [16]byte, stat *syscall.Stat_t) string {
	return fmt.Sprintf("fsid:darwin:v2:%x:%d", volume, stat.Ino)
}
