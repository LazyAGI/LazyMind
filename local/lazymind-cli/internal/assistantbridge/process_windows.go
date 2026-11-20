//go:build windows

package assistantbridge

import (
	"bytes"
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

const connectionRefused = windows.WSAECONNREFUSED

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
	flags := uint32(syscall.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS)
	// A shared Bridge must outlive both the connector launcher and Desktop.
	if os.Getenv(foregroundLauncherJobEnv) == "1" || currentProcessInJob() {
		flags |= windows.CREATE_BREAKAWAY_FROM_JOB
	}
	return &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
}

func currentProcessInJob() bool {
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	return windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil) == nil
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
	logPath := log.Name()
	// The failed Go launch left an open writer. Release it before PowerShell
	// opens the same path using a sharing mode that disallows other writers.
	if err := log.Close(); err != nil {
		return nil, nil, fmt.Errorf("close Assistant Bridge log before WMI launch: %w", err)
	}
	// Keep inherited environment values out of observable process command lines.
	config, err := os.CreateTemp(filepath.Dir(logPath), ".bridge-launch-*.json")
	if err != nil {
		return nil, nil, err
	}
	defer os.Remove(config.Name())
	err = json.NewEncoder(config).Encode(map[string]any{
		"executable": command.Path, "arguments": command.Args[1:],
		"environment": command.Env,
	})
	closeErr := config.Close()
	if err != nil {
		return nil, nil, err
	}
	if closeErr != nil {
		return nil, nil, closeErr
	}
	script := bridgeBootstrapCommand(config.Name(), logPath)
	childCommand := "powershell.exe -NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand " + encodePowerShell(script)
	create := strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"$ProgressPreference = 'SilentlyContinue'",
		"$result = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = '" + childCommand + "' }",
		"if ($result.ReturnValue -ne 0) { throw \"Win32_Process.Create failed: $($result.ReturnValue)\" }",
		"[Console]::Out.WriteLine([uint32]$result.ProcessId)",
	}, "; ")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	launcher := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(create))
	launcher.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	var diagnostics bytes.Buffer
	launcher.Stderr = &diagnostics
	output, err := launcher.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("launch independent Assistant Bridge: %w: %s", err, strings.TrimSpace(diagnostics.String()))
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid <= 0 {
		return nil, nil, fmt.Errorf("independent Assistant Bridge returned an invalid PID (stdout %d bytes, stderr %d bytes)", len(output), diagnostics.Len())
	}
	kill := func() {
		stop := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
		stop.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		_ = stop.Run()
	}
	// WMI returns before the new PowerShell process has applied its config.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(config.Name()); errors.Is(err, os.ErrNotExist) {
			break
		}
		if ownerExited(pid) {
			return nil, nil, fmt.Errorf("independent Assistant Bridge exited during launch preparation; inspect %s", logPath)
		}
		if time.Now().After(deadline) {
			kill()
			return nil, nil, fmt.Errorf("independent Assistant Bridge launch preparation timed out; inspect %s", logPath)
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

func bridgeBootstrapCommand(configPath, logPath string) string {
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	return "$configPath = " + quote(configPath) + "; $logPath = " + quote(logPath) + "\n" + bridgeBootstrapScript
}

const bridgeBootstrapScript = `
$ErrorActionPreference = 'Stop'
$stage = 'read launch configuration'
try {
    $config = Get-Content -LiteralPath $configPath -Raw -Encoding UTF8 | ConvertFrom-Json
    $stage = 'restore environment'
    foreach ($entry in $config.environment) {
        # -split avoids binding the limit to StringSplitOptions in Windows PowerShell.
        $pair = $entry -split '=', 2
        if ($pair.Count -eq 2 -and $pair[0]) { [Environment]::SetEnvironmentVariable($pair[0], $pair[1], 'Process') }
    }
    $stage = 'prepare executable'
    $executable = [string]$config.executable
    if (-not [IO.File]::Exists($executable)) {
        throw [IO.FileNotFoundException]::new('Assistant Bridge executable unavailable')
    }
    $arguments = @($config.arguments)
    Remove-Item -LiteralPath $configPath
    $stage = 'run executable'
    $ErrorActionPreference = 'Continue'
    $global:LASTEXITCODE = 1
    & $executable @arguments 2>&1 | Out-File -LiteralPath $logPath -Append -Encoding UTF8 -ErrorAction Stop
    $status = $LASTEXITCODE
    if ($status -ne 0) {
        [IO.File]::AppendAllText($logPath, "Assistant Bridge executable returned exit code $status" + [Environment]::NewLine)
    }
    exit $status
} catch {
    [IO.File]::AppendAllText($logPath, "Assistant Bridge bootstrap failed during ${stage}: $($_.Exception.GetType().Name)" + [Environment]::NewLine)
    exit 1
}
`

func encodePowerShell(script string) string {
	chars := utf16.Encode([]rune(script))
	encoded := make([]byte, len(chars)*2)
	for i, char := range chars {
		binary.LittleEndian.PutUint16(encoded[i*2:], char)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}
