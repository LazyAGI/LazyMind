package workbuddyhost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// Include hidden/tray main processes when deciding whether startup is safe.
// Never silently send new startup arguments to an existing non-CDP instance.
const windowsDesktopFunctions = `
function Start-WorkBuddyDesktop {
    $exe = $env:LAZYMIND_WB_DESKTOP
    $running = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -eq $exe })
    foreach ($instance in $running) {
        if (-not $instance.CommandLine) { throw 'Cannot inspect WorkBuddy; quit it from its tray menu and retry' }
        if ($instance.CommandLine -match '(?:^|\s)--type(?:=|\s)') { continue }
        $process = Get-Process -Id $instance.ProcessId -ErrorAction SilentlyContinue
        if (-not $process) { continue }
        if ($process.MainWindowHandle -eq 0) { throw 'WorkBuddy is still running in the tray; quit WorkBuddy from its tray menu and retry' }
        if (-not $process.CloseMainWindow()) { throw 'Close WorkBuddy and retry' }
        if (-not $process.WaitForExit(10000)) { throw 'WorkBuddy did not exit; quit it from its tray menu or finish its pending dialog and retry' }
    }
    # LazyMind itself may predate the persistent environment update. Override
    # the inherited value as WorkBuddy reads it before Electron startup.
    $env:WORKBUDDY_REMOTE_DEBUGGING_PORT = $env:LAZYMIND_WB_CDP_PORT
    Start-Process -FilePath $exe -ArgumentList @('--remote-debugging-address=127.0.0.1', ('--remote-debugging-port='+$env:LAZYMIND_WB_CDP_PORT))
}
`

func launchDesktop(ctx context.Context, application string, port int) error {
	// Pass the installed executable via environment, never interpolate a path
	// into PowerShell source. CloseMainWindow allows ordinary application shutdown.
	script := "$ErrorActionPreference='Stop';" + windowsDesktopFunctions + "\nStart-WorkBuddyDesktop"
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "LAZYMIND_WB_DESKTOP="+application, "LAZYMIND_WB_CDP_PORT="+strconv.Itoa(port))
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("start WorkBuddy: %s: %w", output, err)
	}
	return nil
}
