//go:build windows

package assistantbridge

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ownerExited(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return true
		}
		// A WMI-launched process may not be permitted to open its owner.
		// Only a complete process-table scan can then prove that it exited.
		snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
		if err != nil {
			return false
		}
		defer windows.CloseHandle(snapshot)
		entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
		for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
			if entry.ProcessID == uint32(pid) {
				return false
			}
		}
		return errors.Is(err, windows.ERROR_NO_MORE_FILES)
	}
	defer windows.CloseHandle(handle)
	state, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && state == windows.WAIT_OBJECT_0
}

func detachedProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008 | windows.CREATE_BREAKAWAY_FROM_JOB,
		HideWindow:    true,
	}
}

func startBridgeProcess(command *exec.Cmd) (<-chan error, func(), error) {
	if err := command.Start(); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			// Electron or its launcher may be in a Job that forbids breakaway.
			// WMI starts outside that Job, as the Desktop runtime guard does.
			return startBridgeOutsideJob(command)
		}
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	return done, func() { _ = command.Process.Kill() }, nil
}

func startBridgeOutsideJob(command *exec.Cmd) (<-chan error, func(), error) {
	log, ok := command.Stdout.(*os.File)
	if !ok {
		return nil, nil, errors.New("Assistant Bridge requires a log file")
	}
	// Keep inherited environment values out of observable process command lines.
	config, err := os.CreateTemp(filepath.Dir(log.Name()), ".bridge-launch-*.json")
	if err != nil {
		return nil, nil, err
	}
	defer os.Remove(config.Name())
	err = json.NewEncoder(config).Encode(map[string]any{
		"executable": command.Path, "arguments": command.Args[1:],
		"environment": command.Env, "log": log.Name(),
	})
	closeErr := config.Close()
	if err != nil {
		return nil, nil, err
	}
	if closeErr != nil {
		return nil, nil, closeErr
	}
	path := "'" + strings.ReplaceAll(config.Name(), "'", "''") + "'"
	script := strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"$config = Get-Content -LiteralPath " + path + " -Raw | ConvertFrom-Json",
		"Remove-Item -LiteralPath " + path,
		"foreach ($entry in $config.environment) { $pair = $entry.Split(@('='), 2); if ($pair[0]) { [Environment]::SetEnvironmentVariable($pair[0], $pair[1], 'Process') } }",
		"$arguments = @($config.arguments)",
		"$ErrorActionPreference = 'Continue'",
		"& $config.executable @arguments >> $config.log 2>&1",
		"exit $LASTEXITCODE",
	}, "; ")
	childCommand := "powershell.exe -NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand " + encodePowerShell(script)
	create := "$result = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = '" + childCommand + "' }; if ($result.ReturnValue -ne 0) { throw \"Win32_Process.Create failed: $($result.ReturnValue)\" }; $result.ProcessId"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	launcher := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(create))
	launcher.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	output, err := launcher.CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("launch independent Assistant Bridge: %w: %s", err, strings.TrimSpace(string(output)))
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid <= 0 {
		return nil, nil, errors.New("independent Assistant Bridge returned an invalid PID")
	}
	kill := func() {
		stop := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
		stop.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		_ = stop.Run()
	}
	// WMI returns before the new PowerShell process has consumed its config.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(config.Name()); errors.Is(err, os.ErrNotExist) {
			break
		}
		if ownerExited(pid) || time.Now().After(deadline) {
			kill()
			return nil, nil, errors.New("independent Assistant Bridge could not read its launch configuration")
		}
		time.Sleep(25 * time.Millisecond)
	}
	done := make(chan error, 1)
	go func() {
		for !ownerExited(pid) {
			time.Sleep(100 * time.Millisecond)
		}
		done <- errors.New("independent Assistant Bridge exited")
	}()
	return done, kill, nil
}

func encodePowerShell(script string) string {
	chars := utf16.Encode([]rune(script))
	encoded := make([]byte, len(chars)*2)
	for i, char := range chars {
		binary.LittleEndian.PutUint16(encoded[i*2:], char)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}
