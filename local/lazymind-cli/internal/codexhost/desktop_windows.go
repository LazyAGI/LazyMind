//go:build windows

package codexhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Include hidden/tray main processes when deciding whether startup is safe.
// Never silently send new startup arguments to an existing non-CDP instance.
const windowsDesktopFunctions = `
function Close-CodexBackgroundProcess([int]$processId, [string]$expectedPath) {
    if (-not ('LazyMindCodexNormalShutdown' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text;
public static class LazyMindCodexNormalShutdown {
    [StructLayout(LayoutKind.Sequential)]
    struct UniqueProcess { public int pid; public System.Runtime.InteropServices.ComTypes.FILETIME started; }
    [DllImport("rstrtmgr.dll", CharSet=CharSet.Unicode)]
    static extern uint RmStartSession(out uint session, int flags, StringBuilder key);
    [DllImport("rstrtmgr.dll", CharSet=CharSet.Unicode)]
    static extern uint RmRegisterResources(uint session, uint files, string[] names, uint count, UniqueProcess[] applications, uint services, string[] serviceNames);
    [DllImport("rstrtmgr.dll")]
    static extern uint RmShutdown(uint session, uint flags, IntPtr callback);
    [DllImport("rstrtmgr.dll")]
    static extern uint RmEndSession(uint session);
    static void Check(uint result) { if (result != 0) throw new System.ComponentModel.Win32Exception((int)result); }
    public static void Close(int id, string expectedPath) {
        using (Process process = Process.GetProcessById(id)) {
            if (!String.Equals(process.MainModule.FileName, expectedPath, StringComparison.OrdinalIgnoreCase))
                throw new InvalidOperationException("Codex process identity changed");
            long started = process.StartTime.ToUniversalTime().ToFileTimeUtc();
            UniqueProcess target = new UniqueProcess { pid=id, started=new System.Runtime.InteropServices.ComTypes.FILETIME {
                dwLowDateTime=unchecked((int)started), dwHighDateTime=(int)(started >> 32) } };
            uint session;
            Check(RmStartSession(out session, 0, new StringBuilder(33)));
            try {
                Check(RmRegisterResources(session, 0, null, 1, new UniqueProcess[] { target }, 0, null));
                // No RmForceShutdown: respect application shutdown refusal.
                Check(RmShutdown(session, 0, IntPtr.Zero));
            } finally { RmEndSession(session); }
        }
    }
}
'@
    }
    try { [LazyMindCodexNormalShutdown]::Close($processId, $expectedPath) }
    catch { throw ('Codex could not close normally: ' + $_.Exception.Message) }
}
function Invoke-CodexPackagedApplication([string]$appId, [string]$arguments) {
    if (-not ('LazyMindCodexActivation' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
[ComImport, Guid("2e941141-7f97-4756-ba1d-9decde894a3d"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
interface ILazyMindCodexActivation {
    [PreserveSig] int ActivateApplication([MarshalAs(UnmanagedType.LPWStr)] string appId, [MarshalAs(UnmanagedType.LPWStr)] string arguments, uint options, out uint processId);
}
public static class LazyMindCodexActivation {
    public static uint Start(string appId, string arguments) {
        object instance = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("45BA127D-10A8-46EA-8AB7-56EA9078943C")));
        try {
            uint pid;
            int hr = ((ILazyMindCodexActivation)instance).ActivateApplication(appId, arguments, 0, out pid);
            Marshal.ThrowExceptionForHR(hr);
            return pid;
        } finally { Marshal.ReleaseComObject(instance); }
    }
}
'@
    }
    [LazyMindCodexActivation]::Start($appId, $arguments) | Out-Null
}
function Start-CodexDesktop {
    $exe = $env:LAZYMIND_CODEX_DESKTOP
    $arguments = @('--remote-debugging-address=127.0.0.1', ('--remote-debugging-port='+$env:LAZYMIND_CODEX_CDP_PORT))
    if ($env:LAZYMIND_CODEX_THREAD_LINK) { $arguments += $env:LAZYMIND_CODEX_THREAD_LINK }
    if ($env:LAZYMIND_CODEX_NAVIGATION_ONLY -eq '1') {
        $arguments = @($env:LAZYMIND_CODEX_THREAD_LINK)
    } else {
    $running = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -eq $exe })
    foreach ($instance in $running) {
        if (-not $instance.CommandLine) { throw 'Cannot inspect Codex; quit it from its tray menu and retry' }
        if ($instance.CommandLine -match '(?:^|\s)--type(?:=|\s)') { continue }
        $process = Get-Process -Id $instance.ProcessId -ErrorAction SilentlyContinue
        if (-not $process) { continue }
        if ($process.MainWindowHandle -eq 0) {
            Close-CodexBackgroundProcess $instance.ProcessId $exe
        } else {
            if (-not $process.CloseMainWindow()) { throw 'Codex refused to close; finish its pending dialog and retry' }
            if (-not $process.WaitForExit(10000)) {
                $process.Refresh()
                if ($process.MainWindowHandle -eq 0) { Close-CodexBackgroundProcess $instance.ProcessId $exe }
            }
        }
        if (-not $process.WaitForExit(10000)) { throw 'Codex did not exit; finish its pending dialog and retry' }
    }
    }
    # Activate packaged apps through Windows so their package identity is retained.
    # No shortcuts, taskbar entries or application files are modified.
    Write-Output '{"stage":"starting"}'
    if (Get-Command Get-AppxPackage -ErrorAction SilentlyContinue) {
        foreach ($package in @(Get-AppxPackage -Name 'OpenAI.Codex*' -ErrorAction Stop)) {
            if (-not $package.InstallLocation) { continue }
            $root = [IO.Path]::GetFullPath($package.InstallLocation).TrimEnd('\') + '\'
            $fullExe = [IO.Path]::GetFullPath($exe)
            if (-not $fullExe.StartsWith($root, [StringComparison]::OrdinalIgnoreCase)) { continue }
            $manifest = Get-AppxPackageManifest -Package $package.PackageFullName -ErrorAction Stop
            foreach ($app in @($manifest.Package.Applications.Application)) {
                if (-not $app.Executable) { continue }
                $candidate = [IO.Path]::GetFullPath((Join-Path $package.InstallLocation $app.Executable))
                if ($candidate -ne $fullExe) { continue }
                Invoke-CodexPackagedApplication ($package.PackageFamilyName + '!' + $app.Id) ($arguments -join ' ')
                return
            }
            throw 'Cannot resolve Codex application identity; reopen its installation guide and retry'
        }
    }
    if ($exe -match '[\\/]WindowsApps[\\/]') { throw 'Cannot resolve the installed Codex package; repair its application registration before retrying' }
    Start-Process -FilePath $exe -ArgumentList $arguments
}
`

func launchDesktop(ctx context.Context, application string, port int) error {
	link := ""
	if thread, ok := ctx.Value(recoveryThreadKey{}).(string); ok {
		var err error
		link, err = threadLink(thread)
		if err != nil {
			return err
		}
	}
	return runDesktopCommand(ctx, application, port, link, false)
}

func openDesktopThread(ctx context.Context, application, thread string) error {
	link, err := threadLink(thread)
	if err != nil {
		return err
	}
	return runDesktopCommand(ctx, application, 0, link, true)
}

func runDesktopCommand(ctx context.Context, application string, port int, link string, navigateOnly bool) error {
	// Pass the installed executable via environment, never interpolate a path
	// into PowerShell source. CloseMainWindow allows ordinary application shutdown.
	script := "$ErrorActionPreference='Stop';" + windowsDesktopFunctions + "\nStart-CodexDesktop"
	command := exec.CommandContext(ctx, "powershell.exe", "-STA", "-NoProfile", "-NonInteractive", "-Command", script)
	navigate := "0"
	if navigateOnly {
		navigate = "1"
	}
	command.Env = append(os.Environ(), "LAZYMIND_CODEX_DESKTOP="+application, "LAZYMIND_CODEX_CDP_PORT="+strconv.Itoa(port), "LAZYMIND_CODEX_THREAD_LINK="+link, "LAZYMIND_CODEX_NAVIGATION_ONLY="+navigate)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	command.WaitDelay = time.Second
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var event struct {
			Stage string `json:"stage"`
		}
		if !navigateOnly && json.Unmarshal(scanner.Bytes(), &event) == nil && event.Stage == "starting" {
			reportRecovery(ctx, "starting")
		}
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("start Codex: %s: %w", stderr.String(), err)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}
