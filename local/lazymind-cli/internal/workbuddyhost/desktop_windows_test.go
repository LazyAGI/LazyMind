package workbuddyhost

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestWindowsDesktopLaunchRefreshesInheritedPort(t *testing.T) {
	runDesktopPowerShellTest(t, `
$env:LAZYMIND_WB_DESKTOP = 'C:\Program Files\WorkBuddy\WorkBuddy.exe'
$env:LAZYMIND_WB_CDP_PORT = '9333'
$env:WORKBUDDY_REMOTE_DEBUGGING_PORT = '9444'
function Get-CimInstance { @() }
function Start-Process {
    param($FilePath, $ArgumentList)
    if ($FilePath -ne $env:LAZYMIND_WB_DESKTOP) { throw 'wrong executable' }
    if ($env:WORKBUDDY_REMOTE_DEBUGGING_PORT -ne '9333') { throw 'stale child environment' }
    if (($ArgumentList -join ' ') -ne '--remote-debugging-address=127.0.0.1 --remote-debugging-port=9333') { throw 'wrong arguments' }
    $script:started = $true
}
Start-WorkBuddyDesktop
if (-not $script:started) { throw 'not started' }
`)
}

func TestWindowsDesktopDoesNotLaunchOverHiddenOrUnclosedInstance(t *testing.T) {
	for _, scenario := range []string{"hidden", "refused", "still-running", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			runDesktopPowerShellTest(t, `$scenario = '`+scenario+`'
$env:LAZYMIND_WB_DESKTOP = 'C:\Program Files\WorkBuddy\WorkBuddy.exe'
$env:LAZYMIND_WB_CDP_PORT = '9333'
function Get-CimInstance {
    [pscustomobject]@{ExecutablePath=$env:LAZYMIND_WB_DESKTOP; CommandLine='"C:\Program Files\WorkBuddy\WorkBuddy.exe" --type=renderer'; ProcessId=2}
    [pscustomobject]@{ExecutablePath=$env:LAZYMIND_WB_DESKTOP; CommandLine='"C:\Program Files\WorkBuddy\WorkBuddy.exe"'; ProcessId=1}
    [pscustomobject]@{ExecutablePath='C:\Other.exe'; CommandLine='other'; ProcessId=3}
}
function Get-Process {
    param($Id, $ErrorAction)
    if ($Id -ne 1) { throw 'touched a child or unrelated app' }
    $p = [pscustomobject]@{MainWindowHandle=$(if ($scenario -eq 'hidden') { 0 } else { 1 })}
    $p | Add-Member ScriptMethod CloseMainWindow { return $scenario -ne 'refused' }
    $p | Add-Member ScriptMethod WaitForExit { param($timeout); return $scenario -eq 'closed' }
    return $p
}
function Start-Process { $script:started = $true }
$script:started = $false
$caught = ''
try { Start-WorkBuddyDesktop } catch { $caught = $_.Exception.Message }
if ($scenario -eq 'closed') {
    if ($caught -or -not $script:started) { throw "clean exit failed: $caught" }
} else {
    if ($script:started) { throw 'launched over a running instance' }
    if ($caught -notmatch 'WorkBuddy') { throw "unexpected error: $caught" }
    if ($scenario -eq 'hidden' -and $caught -notmatch 'tray') { throw 'missing actionable tray error' }
}
`)
		})
	}
}

func runDesktopPowerShellTest(t *testing.T, body string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"$ErrorActionPreference='Stop';"+windowsDesktopFunctions+body)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PowerShell: %v\n%s", err, output)
	}
}
