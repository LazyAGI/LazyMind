package assistantbridge

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func verifyBridgePeer(ctx context.Context, pid int, conn net.Conn, logPath string) (string, error) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	protocol := "tcp"
	if conn.RemoteAddr().(*net.TCPAddr).IP.To4() == nil {
		protocol = "tcpv6"
	}
	command := exec.CommandContext(ctx, filepath.Join(dir, "netstat.exe"), "-ano", "-p", protocol)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 5 && fields[1] == conn.RemoteAddr().String() && fields[2] == conn.LocalAddr().String() && fields[4] == strconv.Itoa(pid) {
			return verifyBridgePeerFiles(uint32(pid), logPath)
		}
	}
	return "", errors.New("previous Bridge does not own the TCP connection")
}

func verifyBridgePeerFiles(pid uint32, logPath string) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	image := make([]uint16, 32768)
	size := uint32(len(image))
	if err := windows.QueryFullProcessImageName(handle, 0, &image[0], &size); err != nil {
		return "", err
	}
	executable := strings.TrimPrefix(windows.UTF16ToString(image[:size]), `\\?\`)
	volume := filepath.VolumeName(executable)
	if !filepath.IsAbs(executable) || len(volume) != 2 || volume[1] != ':' ||
		!filepath.IsLocal(strings.TrimLeft(executable[len(volume):], `\/`)) {
		return "", errors.New("previous Bridge executable could not be verified")
	}
	// Drive-letter paths can still name mapped network shares.
	drive := windows.GetDriveType(windows.StringToUTF16Ptr(volume + `\`))
	switch drive {
	case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE, windows.DRIVE_RAMDISK, windows.DRIVE_CDROM:
	default:
		return "", errors.New("previous Bridge executable is not on a local drive")
	}
	if logPath == "" {
		return executable, nil
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	if !bridgeProcessUsesLog(pid, created, logPath) {
		return "", errors.New("previous Bridge does not use this environment's log")
	}
	return executable, nil
}

type restartManagerProcess struct {
	pid     uint32
	started windows.Filetime
	app     [256]uint16
	service [64]uint16
	appType uint32
	status  uint32
	session uint32
	restart int32
}

// Ask Restart Manager which processes have this particular log open. Only its
// read APIs are used. Shutdown happens separately on the verified HTTP socket.
func bridgeProcessUsesLog(pid uint32, started windows.Filetime, logPath string) bool {
	log, err := windows.UTF16PtrFromString(logPath)
	if err != nil {
		return false
	}
	manager := windows.NewLazySystemDLL("rstrtmgr.dll")
	start := manager.NewProc("RmStartSession")
	end := manager.NewProc("RmEndSession")
	register := manager.NewProc("RmRegisterResources")
	list := manager.NewProc("RmGetList")
	var session uint32
	var key [33]uint16
	if result, _, _ := start.Call(uintptr(unsafe.Pointer(&session)), 0, uintptr(unsafe.Pointer(&key[0]))); result != 0 {
		return false
	}
	defer end.Call(uintptr(session))
	if result, _, _ := register.Call(uintptr(session), 1, uintptr(unsafe.Pointer(&log)), 0, 0, 0, 0); result != 0 {
		return false
	}
	var needed, count, reboot uint32
	result, _, _ := list.Call(uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&reboot)))
	if result != uintptr(windows.ERROR_MORE_DATA) || needed == 0 || needed > 1024 {
		return false
	}
	count = needed
	processes := make([]restartManagerProcess, count)
	result, _, _ = list.Call(uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&processes[0])), uintptr(unsafe.Pointer(&reboot)))
	if result != 0 {
		return false
	}
	for _, process := range processes[:count] {
		if process.pid == pid && process.started == started {
			return true
		}
	}
	return false
}
