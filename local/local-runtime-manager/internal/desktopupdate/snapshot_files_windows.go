package desktopupdate

import (
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

func hasMultipleLinks(path string, _ os.FileInfo) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return false, err
	}
	return info.NumberOfLinks > 1, nil
}

func allocatedDiskBytes(path string) (uint64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var high uint32
	low, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW").Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&high)))
	if uint32(low) == 0xffffffff && callErr != windows.ERROR_SUCCESS {
		return 0, callErr
	}
	return uint64(high)<<32 | uint64(uint32(low)), nil
}
func freeDiskBytes(path string) (uint64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, all uint64
	err = windows.GetDiskFreeSpaceEx(name, &free, &total, &all)
	return free, err
}
