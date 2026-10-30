package desktopsetup

import (
	"context"
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
	"unsafe"
)

func processImage(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(h, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

// Native inspection avoids starting several PowerShell/CIM hosts inside the
// five-second health budget, especially during application startup.
func Running(ctx context.Context, application string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
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
		image, imageErr := processImage(entry.ProcessID)
		if imageErr == nil && strings.EqualFold(image, application) {
			return true, nil
		}
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return false, err
	}
	return false, nil
}

var extendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

func listenerTable(ctx context.Context) ([]byte, error) {
	var size uint32 = 4096
	for attempts := 0; attempts < 4; attempts++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if size < 4 || size > 16<<20 {
			return nil, fmt.Errorf("invalid TCP table size")
		}
		buffer := make([]byte, size)
		// AF_INET, TCP_TABLE_OWNER_PID_LISTENER. CDP is contacted at 127.0.0.1.
		result, _, _ := extendedTCPTable.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 3, 0)
		if result == uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
			continue
		}
		if result != 0 {
			return nil, windows.Errno(result)
		}
		return buffer, nil
	}
	return nil, fmt.Errorf("TCP listener table kept changing")
}

func ListenerMatches(ctx context.Context, application string, port int) bool {
	if port < 1 || port > 65535 {
		return false
	}
	table, err := listenerTable(ctx)
	if err != nil || ctx.Err() != nil {
		return false
	}
	count := int(binary.LittleEndian.Uint32(table[:4]))
	if count > (len(table)-4)/24 {
		return false
	}
	found := false
	for i := 0; i < count; i++ {
		row := table[4+i*24 : 4+(i+1)*24]
		if int(binary.BigEndian.Uint16(row[8:10])) != port {
			continue
		}
		// Only listeners that can serve the actual IPv4 loopback endpoint matter.
		addr := binary.LittleEndian.Uint32(row[4:8])
		if addr != 0 && addr != 0x0100007f {
			continue
		}
		image, err := processImage(binary.LittleEndian.Uint32(row[20:24]))
		if err != nil || !strings.EqualFold(image, application) || ctx.Err() != nil {
			return false
		}
		found = true
	}
	return found
}
