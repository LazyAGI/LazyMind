package codexhost

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestWindowsDesktopLaunchSelectsExactPackagedIdentityOrOrdinaryExecutable(t *testing.T) {
	for _, scenario := range []string{"packaged", "ordinary", "unresolved", "wrong-manifest"} {
		t.Run(scenario, func(t *testing.T) {
			runDesktopPowerShellTest(t, `$scenario = '`+scenario+`'
$env:LAZYMIND_CODEX_DESKTOP = 'C:\Program Files\WindowsApps\OpenAI.Codex_测试\app\Codex.exe'
$env:LAZYMIND_CODEX_CDP_PORT = '9334'
if ($scenario -eq 'ordinary') { $env:LAZYMIND_CODEX_DESKTOP = 'C:\用户\Codex\Codex.exe' }
function Get-CimInstance { @() }
function Get-AppxPackage {
    if ($scenario -eq 'unresolved') { return @() }
    [pscustomobject]@{InstallLocation='C:\Other'; PackageFullName='unrelated'; PackageFamilyName='unrelated'}
    [pscustomobject]@{InstallLocation='C:\Program Files\WindowsApps\OpenAI.Codex_测试'; PackageFullName='codex-full'; PackageFamilyName='OpenAI.Codex_family'}
}
function Get-AppxPackageManifest {
    param($Package, $ErrorAction)
    if ($Package -ne 'codex-full') { throw 'read unrelated package' }
    $application = [pscustomobject]@{Executable=$(if ($scenario -eq 'wrong-manifest') { 'other.exe' } else { 'app\Codex.exe' }); Id='App'}
    [pscustomobject]@{Package=[pscustomobject]@{Applications=[pscustomobject]@{Application=@($application)}}}
}
function Invoke-CodexPackagedApplication {
    param($appId, $arguments)
    if ($scenario -ne 'packaged' -or $appId -ne 'OpenAI.Codex_family!App') { throw 'wrong activation identity' }
    if ($arguments -ne '--remote-debugging-address=127.0.0.1 --remote-debugging-port=9334') { throw 'lost activation arguments' }
    $script:started = $true
}
function Start-Process {
    param($FilePath, $ArgumentList)
    if ($scenario -ne 'ordinary' -or $FilePath -ne $env:LAZYMIND_CODEX_DESKTOP) { throw 'bypassed package identity' }
    if (($ArgumentList -join ' ') -ne '--remote-debugging-address=127.0.0.1 --remote-debugging-port=9334') { throw 'lost arguments' }
    $script:started = $true
}
$script:started = $false
$caught = ''
try { Start-CodexDesktop } catch { $caught = $_.Exception.Message }
if ($scenario -eq 'packaged' -or $scenario -eq 'ordinary') {
    if ($caught -or -not $script:started) { throw "launch failed: $caught" }
} else {
    if ($script:started -or $caught -notmatch 'Cannot resolve') { throw "unsafe fallback: $caught" }
}
`)
		})
	}
}

func TestWindowsDesktopDoesNotLaunchOverHiddenOrUnclosedInstance(t *testing.T) {
	for _, scenario := range []string{"hidden", "tray-closed", "close-to-tray", "refused", "still-running", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			runDesktopPowerShellTest(t, `$scenario = '`+scenario+`'
$env:LAZYMIND_CODEX_DESKTOP = 'C:\Program Files\Codex\Codex.exe'
$env:LAZYMIND_CODEX_CDP_PORT = '9333'
function Get-AppxPackage { @() }
function Get-CimInstance {
    [pscustomobject]@{ExecutablePath=$env:LAZYMIND_CODEX_DESKTOP; CommandLine='"C:\Program Files\Codex\Codex.exe" --type=renderer'; ProcessId=2}
    [pscustomobject]@{ExecutablePath=$env:LAZYMIND_CODEX_DESKTOP; CommandLine='"C:\Program Files\Codex\Codex.exe"'; ProcessId=1}
    [pscustomobject]@{ExecutablePath='C:\Other.exe'; CommandLine='other'; ProcessId=3}
}
function Get-Process {
    param($Id, $ErrorAction)
    if ($Id -ne 1) { throw 'touched a child or unrelated app' }
    $p = [pscustomobject]@{MainWindowHandle=$(if ($scenario -in @('hidden','tray-closed')) { 0 } else { 1 })}
    $p | Add-Member ScriptMethod CloseMainWindow { return $scenario -ne 'refused' }
    $p | Add-Member ScriptMethod Refresh { if ($scenario -eq 'close-to-tray') { $this.MainWindowHandle = 0 } }
    $p | Add-Member ScriptMethod WaitForExit { param($timeout); return $scenario -in @('closed','tray-closed') -or $script:backgroundClosed }
    return $p
}
function Start-Process { $script:started = $true }
function Close-CodexBackgroundProcess {
    param($processId, $expectedPath)
    if ($processId -ne 1 -or $expectedPath -ne $env:LAZYMIND_CODEX_DESKTOP) { throw 'wrong background process' }
    if ($scenario -notin @('tray-closed','close-to-tray')) { throw 'Codex tray shutdown refused' }
    $script:backgroundClosed = $true
}
$script:backgroundClosed = $false
$script:started = $false
$caught = ''
try { Start-CodexDesktop } catch { $caught = $_.Exception.Message }
if ($scenario -in @('closed','tray-closed','close-to-tray')) {
    if ($caught -or -not $script:started) { throw "clean exit failed: $caught" }
} else {
    if ($script:started) { throw 'launched over a running instance' }
    if ($caught -notmatch 'Codex') { throw "unexpected error: $caught" }
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

func TestWindowsNavigationDoesNotCloseAnExistingDesktop(t *testing.T) {
	for _, packaged := range []bool{false, true} {
		mode := "ordinary"
		if packaged {
			mode = "packaged"
		}
		t.Run(mode, func(t *testing.T) {
			runDesktopPowerShellTest(t, `
$env:LAZYMIND_CODEX_DESKTOP = 'C:\Codex\Codex.exe'
$env:LAZYMIND_CODEX_THREAD_LINK = 'codex://threads/11111111-2222-3333-4444-555555555555'
$env:LAZYMIND_CODEX_NAVIGATION_ONLY = '1'
function Get-CimInstance { throw 'navigation must not close or restart Codex' }
function Get-AppxPackage { `+map[bool]string{false: "@()", true: "[pscustomobject]@{InstallLocation='C:\\Codex';PackageFullName='codex';PackageFamilyName='family'}"}[packaged]+` }
function Get-AppxPackageManifest {
 [pscustomobject]@{Package=[pscustomobject]@{Applications=[pscustomobject]@{Application=@([pscustomobject]@{Executable='Codex.exe';Id='App'})}}}
}
function Invoke-CodexPackagedApplication {param($appId,$arguments)
 if($appId -ne 'family!App' -or $arguments -ne $env:LAZYMIND_CODEX_THREAD_LINK){throw 'lost packaged navigation'}
 $script:started=$true
}
function Start-Process {param($FilePath,$ArgumentList)
 if($FilePath -ne $env:LAZYMIND_CODEX_DESKTOP -or $ArgumentList.Count -ne 1 -or $ArgumentList[0] -ne $env:LAZYMIND_CODEX_THREAD_LINK){throw 'lost ordinary navigation'}
 $script:started=$true
}
$script:started=$false
Start-CodexDesktop
if(-not $script:started){throw 'navigation not delivered'}
`)
		})
	}
}
