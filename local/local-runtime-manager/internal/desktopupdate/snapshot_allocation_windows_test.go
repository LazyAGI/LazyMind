//go:build windows

package desktopupdate

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func fixtureAllocatedBytes(t *testing.T, path string) int64 {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var high uint32
	low, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW").Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&high)))
	if uint32(low) == 0xffffffff {
		t.Fatalf("allocation metadata unavailable: %v", callErr)
	}
	return int64(uint64(high)<<32 | uint64(uint32(low)))
}
