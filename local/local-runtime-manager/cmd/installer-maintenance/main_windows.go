package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const appDataLeaf = "LazyMind"

type processRegistry struct {
	Processes []processRecord `json:"processes"`
}

type processRecord struct {
	PID     int    `json:"pid"`
	StartID uint64 `json:"startId,omitempty"`
}

func main() {
	if len(os.Args) != 2 {
		fatalf("usage: lazymind-installer-maintenance check-stopped|purge-local-data")
	}
	root, err := localAppDataRoot()
	if err != nil {
		fatalf("resolve Local AppData: %v", err)
	}
	switch os.Args[1] {
	case "check-stopped":
		if err := checkStopped(root); err != nil {
			fatalf("%v", err)
		}
	case "purge-local-data":
		if err := purgeLocalData(root); err != nil {
			fatalf("purge %s: %v", root, err)
		}
	default:
		fatalf("unsupported command %q", os.Args[1])
	}
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func localAppDataRoot() (string, error) {
	base, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", err
	}
	return localAppDataTarget(base)
}

func localAppDataTarget(base string) (string, error) {
	base = filepath.Clean(base)
	if base == "" || base == "." || filepath.IsAbs(base) == false {
		return "", fmt.Errorf("invalid Local AppData path %q", base)
	}
	return filepath.Join(base, appDataLeaf), nil
}

func checkStopped(root string) error {
	running, err := processNamed("LazyMind.exe")
	if err != nil {
		return err
	}
	if running {
		return errors.New("LazyMind.exe is still running")
	}
	raw, err := os.ReadFile(filepath.Join(root, "run", "processes.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var registry processRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		return fmt.Errorf("read runtime process registry: %w", err)
	}
	for _, process := range registry.Processes {
		if process.PID > 0 && processMatchesStartIdentity(uint32(process.PID), process.StartID) {
			return fmt.Errorf("LazyMind runtime process %d is still running", process.PID)
		}
	}
	return nil
}

func processNamed(name string) (bool, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafeSizeofProcessEntry32())}
	err = windows.Process32First(snapshot, &entry)
	for err == nil {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), name) {
			return true, nil
		}
		err = windows.Process32Next(snapshot, &entry)
	}
	if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
		return false, nil
	}
	return false, err
}

// Kept behind a helper so the structure size is supplied exactly as required by Toolhelp32.
func unsafeSizeofProcessEntry32() uintptr {
	var entry windows.ProcessEntry32
	return unsafe.Sizeof(entry)
}

func processAlive(pid uint32) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	state, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && state == uint32(windows.WAIT_TIMEOUT)
}

func processStartIdentity(pid uint32) uint64 {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(handle)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return 0
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
}

func processMatchesStartIdentity(pid uint32, expected uint64) bool {
	return expected != 0 && processAlive(pid) && processStartIdentity(pid) == expected
}

func purgeLocalData(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	attrs, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(target))
	if err != nil {
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to purge a reparse-point data root")
	}
	parent := filepath.Dir(target)
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	tombstone := fmt.Sprintf(".%s-uninstall-%d-%d", appDataLeaf, os.Getpid(), time.Now().UnixNano())
	if err := root.Rename(appDataLeaf, tombstone); err != nil {
		return fmt.Errorf("quarantine data root: %w", err)
	}
	if err := root.RemoveAll(tombstone); err != nil {
		if restoreErr := root.Rename(tombstone, appDataLeaf); restoreErr != nil {
			return fmt.Errorf("delete quarantined data: %w; restore also failed: %v", err, restoreErr)
		}
		return fmt.Errorf("delete quarantined data: %w", err)
	}
	return nil
}
