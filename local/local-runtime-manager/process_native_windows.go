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

const (
	stillActive        = 259
	jobObjectTerminate = 0x0008
)

var (
	procOpenJobObjectW       = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW")
	assignProcessToJobObject = windows.AssignProcessToJobObject
)

func configureChildProcess(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}

func interruptProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	// CTRL_C/CTRL_BREAK are console-wide mechanisms, not Unix-style targeted
	// signals. They can interrupt process-compose and its calling PowerShell
	// host even when a process group is supplied. Use Windows tree termination;
	// managed service descendants are additionally contained by Job Objects.
	return forceKillProcessTree(pid)
}

func forceKillProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	cmd := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
	configureChildProcess(cmd, false)
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
	if err == nil {
		defer windows.CloseHandle(h)
		var code uint32
		return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
	}

	// A watchdog created through Win32_Process.Create can run outside the
	// Electron Job Object but under a provider token that cannot open the
	// Electron process. Toolhelp32 enumerates the native process table without
	// requiring a handle to the target process.
	snapshot, snapshotErr := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if snapshotErr != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if snapshotErr = windows.Process32First(snapshot, &entry); snapshotErr != nil {
		return false
	}
	for {
		if entry.ProcessID == uint32(pid) {
			return true
		}
		if snapshotErr = windows.Process32Next(snapshot, &entry); snapshotErr != nil {
			return false
		}
	}
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
	if err := assignProcessToJobObject(job, process); err != nil {
		// Nested Job Objects can reject assignment when the parent job does not
		// permit breakaway. Keep startup available and rely on the registered
		// process inventory/orphan scanner when strict containment is unavailable.
		windows.CloseHandle(job)
		return func() {}, nil
	}
	return func() { _ = windows.CloseHandle(job) }, nil
}

func terminateProcessJob(paths RuntimePaths, service string) error {
	name, err := windows.UTF16PtrFromString(windowsJobName(paths, service))
	if err != nil {
		return err
	}
	// x/sys/windows v0.30.0 does not expose OpenJobObject, so keep the raw
	// kernel32 call isolated here and immediately wrap its result as a Handle.
	jobHandle, _, callErr := procOpenJobObjectW.Call(uintptr(jobObjectTerminate), 0, uintptr(unsafe.Pointer(name)))
	if jobHandle == 0 {
		return callErr
	}
	job := windows.Handle(jobHandle)
	defer windows.CloseHandle(job)
	return windows.TerminateJobObject(job, 1)
}

// process-compose has already run its ordered shutdown command by this point.
// Sending CTRL_BREAK to its console group can also interrupt the calling
// PowerShell host, so terminate only the remaining supervisor process tree.
func stopSupervisorProcess(pid int) error { return forceKillProcessTree(pid) }
