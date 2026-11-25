package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func identityPeerPID(fd uintptr) (int, error) {
	// Windows SDK afunix.h: SIO_AF_UNIX_GETPEERPID = _WSAIOR(IOC_VENDOR, 256).
	const getPeerPID = windows.IOC_OUT | windows.IOC_VENDOR | 256
	var pid, size uint32
	err := windows.WSAIoctl(windows.Handle(fd), getPeerPID, nil, 0,
		(*byte)(unsafe.Pointer(&pid)), uint32(unsafe.Sizeof(pid)), &size, nil, 0)
	if err != nil {
		return 0, err
	}
	if size != uint32(unsafe.Sizeof(pid)) {
		return 0, os.ErrInvalid
	}
	return int(pid), nil
}

func identityPeerParent(pid int) (int, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	var info windows.PROCESS_BASIC_INFORMATION
	size := uint32(unsafe.Sizeof(info))
	err = windows.NtQueryInformationProcess(handle, windows.ProcessBasicInformation, unsafe.Pointer(&info), size, nil)
	return int(info.InheritedFromUniqueProcessId), err
}

func identityPeerImagePath(pid int) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	path := make([]uint16, 32768)
	size := uint32(len(path))
	if err := windows.QueryFullProcessImageName(handle, 0, &path[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(path[:size]), nil
}
