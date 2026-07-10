//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const stillActive = 259

var procOpenJobObjectW = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW")

func configureChildProcess(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func interruptProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}

func forceKillProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	cmd := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
	if output, err := cmd.CombinedOutput(); err != nil {
		if !processAlive(pid) {
			return nil
		}
		return errors.New(strings.TrimSpace(string(output)))
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}

func nativeProcessGroupID(_ int) int { return 0 }

func processStartIdentity(pid int) uint64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
}

func windowsJobName(paths RuntimePaths, service string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(paths.RuntimeRoot + "\x00" + service)))
	return `Local\LazyMind-` + hex.EncodeToString(sum[:12])
}

func attachProcessJob(paths RuntimePaths, service string, proc *os.Process) (func(), error) {
	name, err := windows.UTF16PtrFromString(windowsJobName(paths, service))
	if err != nil {
		return nil, err
	}
	job, err := windows.CreateJobObject(nil, name)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(proc.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return func() { _ = windows.CloseHandle(job) }, nil
}

func terminateProcessJob(paths RuntimePaths, service string) error {
	name, err := windows.UTF16PtrFromString(windowsJobName(paths, service))
	if err != nil {
		return err
	}
	jobHandle, _, callErr := procOpenJobObjectW.Call(uintptr(0x0008), 0, uintptr(unsafe.Pointer(name)))
	if jobHandle == 0 {
		return callErr
	}
	job := windows.Handle(jobHandle)
	defer windows.CloseHandle(job)
	return windows.TerminateJobObject(job, 1)
}
