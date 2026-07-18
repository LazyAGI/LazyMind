//go:build windows

package winprocess

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Info struct {
	ProcessID       uint32  `json:"ProcessId"`
	ParentProcessID uint32  `json:"ParentProcessId"`
	SessionID       uint32  `json:"SessionId"`
	StartID         uint64  `json:"StartId"`
	ExecutablePath  *string `json:"ExecutablePath"`
}

var ErrProcessIdentityChanged = errors.New("process identity changed")

func Snapshot(ctx context.Context) ([]Info, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
			return []Info{}, nil
		}
		return nil, err
	}
	processes := make([]Info, 0, 128)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fallback := windows.UTF16ToString(entry.ExeFile[:])
		executable, startID := queryProcessDetails(entry.ProcessID, fallback)
		sessionID := uint32(0)
		_ = windows.ProcessIdToSessionId(entry.ProcessID, &sessionID)
		processes = append(processes, Info{
			ProcessID:       entry.ProcessID,
			ParentProcessID: entry.ParentProcessID,
			SessionID:       sessionID,
			StartID:         startID,
			ExecutablePath:  &executable,
		})
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, err
		}
	}
	return processes, nil
}

func queryProcessDetails(pid uint32, fallback string) (string, uint64) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fallback, 0
	}
	defer windows.CloseHandle(handle)
	executable := fallback
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err == nil && size > 0 {
		executable = windows.UTF16ToString(buffer[:size])
	}
	return executable, startIdentityFromHandle(handle)
}

func Text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == nil {
		defer windows.CloseHandle(handle)
		var code uint32
		return windows.GetExitCodeProcess(handle, &code) == nil && code == 259
	}
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

func StartIdentity(pid int) uint64 {
	if pid <= 0 {
		return 0
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(handle)
	return startIdentityFromHandle(handle)
}

func startIdentityFromHandle(handle windows.Handle) uint64 {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return 0
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
}

func ForceKillTree(ctx context.Context, pid int, expectedStartID uint64) error {
	if pid <= 0 || !Alive(pid) {
		return nil
	}
	if expectedStartID == 0 {
		return errors.New("cannot safely stop process because its start identity is unavailable")
	}
	currentStartID := StartIdentity(pid)
	if currentStartID == 0 {
		if !Alive(pid) {
			return nil
		}
		return errors.New("cannot verify process start identity before force-stop")
	}
	if currentStartID != expectedStartID {
		return ErrProcessIdentityChanged
	}
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return fmt.Errorf("resolve Windows system directory: %w", err)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(systemDirectory, "taskkill.exe"), "/PID", strconv.Itoa(pid), "/T", "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW, HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err == nil || !matchesStartIdentity(pid, expectedStartID) {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		message = err.Error()
	}
	return errors.New(message)
}

func matchesStartIdentity(pid int, expected uint64) bool {
	return expected != 0 && Alive(pid) && StartIdentity(pid) == expected
}

func CurrentSession(processes []Info, pid int) (uint32, error) {
	var sessionID uint32
	if pid > 0 && windows.ProcessIdToSessionId(uint32(pid), &sessionID) == nil {
		return sessionID, nil
	}
	for _, process := range processes {
		if int(process.ProcessID) == pid {
			return process.SessionID, nil
		}
	}
	return 0, fmt.Errorf("process %d is missing from Windows process inventory", pid)
}

func ExcludedAncestors(processes []Info, pid int) map[int]bool {
	parents := make(map[int]int, len(processes))
	for _, process := range processes {
		parents[int(process.ProcessID)] = int(process.ParentProcessID)
	}
	excluded := map[int]bool{}
	for current := pid; current > 0 && !excluded[current]; current = parents[current] {
		excluded[current] = true
	}
	return excluded
}
