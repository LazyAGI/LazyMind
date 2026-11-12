package agentexec

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func platformDSHDesktopRunning(ctx context.Context, cli string) (bool, error) {
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(cli)))))
	application := filepath.Join(root, "DeepSeek Harness.exe")
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(application)) {
			continue
		}
		handle, imageErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if errors.Is(imageErr, windows.ERROR_INVALID_PARAMETER) {
			continue
		}
		if imageErr != nil {
			return false, imageErr
		}
		buffer := make([]uint16, 32768)
		size := uint32(len(buffer))
		imageErr = windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size)
		windows.CloseHandle(handle)
		if imageErr != nil {
			return false, imageErr
		}
		if strings.EqualFold(filepath.Clean(windows.UTF16ToString(buffer[:size])), application) {
			return true, nil
		}
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return false, err
	}
	return false, nil
}
