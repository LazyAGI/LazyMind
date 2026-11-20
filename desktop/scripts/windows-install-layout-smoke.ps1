#Requires -Version 7.0
<#
Only run in a disposable Windows account named lm-install-qa-*.
Provision .lazymind-storage-test-scope.json BEFORE running:
  {"authorization":"Disposable LazyMind storage test","userSid":"S-1-5-21-...",
   "computerName":"TEST-VM","storageRoot":"D:\\QA Tools\\中文\\case-unique"}
Its directory must belong to this SID; StorageRoot must be a new descendant.
No existing product registration/data is backed up, cleared or replaced.
PackagesFile has L/A/B objects with path, sha256, version (exact DisplayVersion),
and sourceCommit. B must have a higher real Windows ProductVersion than A/L.

The operator supplies actual directory-page evidence and creates a persisted
chat + attachment through the UI. Verification uses authenticated application
APIs. This script never seeds a DB, sets DataRoot for the app, or changes system
paths. A passed case does not cover all W1-W12; missing evidence throws.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('NewSimple', 'NewFull', 'LegacyUpgrade', 'V2Upgrade', 'KeepReinstall', 'Purge', 'FailureBoundary')][string]$Case,
    [Parameter(Mandatory)][string]$ScopeFile,
    [Parameter(Mandatory)][string]$PackagesFile,
    [ValidateSet('Manual', 'Updated')][string]$UpgradeEntry = 'Manual',
    [switch]$DisposableWindowsUser
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
function Assert-That([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}
Assert-That ($IsWindows -and $DisposableWindowsUser) 'Refusing execution: Windows and explicit -DisposableWindowsUser are required.'
Assert-That ([Environment]::Is64BitProcess -and [Environment]::Is64BitOperatingSystem) 'Use 64-bit PowerShell so registry providers address the same view as the x64 installer.'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$currentSid = $identity.User.Value
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
Assert-That (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) 'Run installation/application with a non-elevated ordinary-user token.'
Assert-That (($identity.Name -split '\\')[-1] -like 'lm-install-qa-*') 'A dedicated lm-install-qa-* disposable account is required.'
Assert-That ((Split-Path -Leaf $ScopeFile) -eq '.lazymind-storage-test-scope.json') 'Wrong scope sentinel filename.'
$scope = Get-Content -LiteralPath $ScopeFile -Raw | ConvertFrom-Json
Assert-That ($scope.authorization -ceq 'Disposable LazyMind storage test') 'Missing disposable-test authorization.'
Assert-That ($scope.userSid -ceq $currentSid -and $scope.computerName -ceq $env:COMPUTERNAME) 'Scope does not authorize this user and machine.'
$scopeDir = [IO.Path]::GetFullPath((Split-Path -Parent (Resolve-Path -LiteralPath $ScopeFile).Path))
$storageRoot = [IO.Path]::GetFullPath($scope.storageRoot).TrimEnd('\')
Assert-That ($storageRoot -match '^[A-Za-z]:\\') 'StorageRoot must be an absolute drive path.'
Assert-That ($storageRoot.StartsWith($scopeDir.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) 'StorageRoot must be a new child of the authorized scope.'
Assert-That ($storageRoot -notmatch '(?i)lazymind') 'Use a path without the product name to expose NSIS name-appending mistakes.'
Assert-That (-not (Test-Path -LiteralPath $storageRoot)) 'StorageRoot exists; use a clean disposable scope.'
$scopeOwner = ([Security.Principal.NTAccount]::new((Get-Acl -LiteralPath $scopeDir).Owner)).Translate([Security.Principal.SecurityIdentifier]).Value
Assert-That ($scopeOwner -ceq $currentSid) 'The scope directory must belong to the disposable account.'
for ($part = $scopeDir; $part; $part = Split-Path -Parent $part) {
    Assert-That (-not ((Get-Item -LiteralPath $part -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) 'Scope ancestors must not be reparse points.'
}
foreach ($name in @('ELECTRON_RUN_AS_NODE', 'LAZYMIND_EDITABLE_PPT_BUNDLE_PATH', 'LAZYMIND_DESKTOP_DATA_ROOT', 'LAZYMIND_DESKTOP_RUNTIME_ROOT', 'LAZYMIND_RUNTIME_ROOT', 'LAZYMIND_RUNTIME_RESOURCES_ROOT', 'LAZYMIND_DESKTOP_RESOURCES_ROOT', 'LAZYMIND_DESKTOP_REPO_ROOT')) {
    Assert-That (-not [Environment]::GetEnvironmentVariable($name)) "Remove inherited application override $name; installed discovery must be observed."
}
$targetDrive = $storageRoot.Substring(0, 2)
$targetVolume = @(Get-CimInstance Win32_Volume -Filter "DriveLetter='$targetDrive'")
$systemVolume = @(Get-CimInstance Win32_Volume -Filter "DriveLetter='$env:SystemDrive'")
Assert-That ($targetVolume.Count -eq 1 -and $systemVolume.Count -eq 1) 'Cannot identify both real volumes.'
Assert-That ($targetVolume[0].DriveType -eq 3 -and $targetVolume[0].FileSystem -eq 'NTFS') 'Target must be a real local fixed NTFS volume.'
Assert-That ($targetVolume[0].DeviceID -ne $systemVolume[0].DeviceID) 'Target must be a genuinely different volume; subst is insufficient.'
$productGuid = '412e5ec7-d7ee-55e2-b3e8-1ea8840a484e'
$installKey = "Software\$productGuid"
$uninstallKey = "Software\Microsoft\Windows\CurrentVersion\Uninstall\$productGuid"
$dataKey = 'Software\LazyMind\DesktopStorage'
function Read-ProductValue([string]$Key, [string]$Name, [Microsoft.Win32.RegistryView]$View = [Microsoft.Win32.RegistryView]::Registry64) {
    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::CurrentUser, $View)
    try {
        $entry = $base.OpenSubKey($Key)
        if ($null -eq $entry) { return $null }
        try { return $entry.GetValue($Name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) } finally { $entry.Dispose() }
    } finally { $base.Dispose() }
}
foreach ($view in @([Microsoft.Win32.RegistryView]::Registry64, [Microsoft.Win32.RegistryView]::Registry32)) {
    Assert-That ($null -eq (Read-ProductValue $installKey 'InstallLocation' $view)) 'Existing product registration: refusing replacement.'
    Assert-That ($null -eq (Read-ProductValue $uninstallKey 'UninstallString' $view)) 'Existing product uninstaller: refusing replacement.'
    Assert-That ($null -eq (Read-ProductValue $dataKey 'DataRoot' $view)) 'Existing DataRoot: refusing overwrite or backup.'
}
$legacyRoot = Join-Path $env:LOCALAPPDATA 'LazyMind'
$legacyApp = Join-Path $env:LOCALAPPDATA 'Programs\LazyMind'
$installerCache = Join-Path $env:LOCALAPPDATA 'lazymind-desktop-updater\installer.exe'
$roamingProfiles = @((Join-Path $env:APPDATA 'LazyMind'), (Join-Path $env:APPDATA 'lazymind-desktop'))
foreach ($oldPath in @($legacyRoot, $legacyApp, $installerCache, (Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'LazyMind')) + $roamingProfiles) {
    Assert-That (-not (Test-Path -LiteralPath $oldPath)) "Account is not clean: $oldPath exists. No automatic cleanup is performed."
}
Assert-That (@(Get-Process | Where-Object { $_.ProcessName -in @('LazyMind', 'LazyMindApp') }).Count -eq 0) 'A real Desktop process exists; refusing execution.'
$packages = Get-Content -LiteralPath $PackagesFile -Raw | ConvertFrom-Json
$packageHashes = @()
$packageVersions = @{}
foreach ($label in @('L', 'A', 'B')) {
    $package = $packages.$label
    Assert-That ($package.sourceCommit -match '^[0-9a-fA-F]{7,40}$') "Missing source commit for $label."
    Assert-That ($package.sha256 -match '^[0-9a-fA-F]{64}$') "Missing SHA-256 for $label."
    $actualHash = (Get-FileHash -LiteralPath $package.path -Algorithm SHA256).Hash
    Assert-That ($actualHash -ieq $package.sha256) "Package $label SHA-256 mismatch."
    $versionText = (Get-Item -LiteralPath $package.path).VersionInfo.ProductVersion
    Assert-That ($versionText -match '^\d+\.\d+\.\d+(?:\.\d+)?') "Package $label lacks an actual Windows ProductVersion."
    $packageVersions[$label] = [version]$Matches[0]
    $packageHashes += $actualHash
}
Assert-That (@($packageHashes | Select-Object -Unique).Count -eq 3) 'L/A/B must be distinct real installer packages.'
Assert-That ($packageVersions.B -gt $packageVersions.A -and $packageVersions.B -gt $packageVersions.L) 'B must have a higher ProductVersion than A and L.'

# Mutation starts only after the complete environment/input guard.
$runId = [guid]::NewGuid().ToString('N')
$evidence = Join-Path $scopeDir "evidence-$Case-$runId"
New-Item -ItemType Directory -Path $evidence | Out-Null
$neighbor = Join-Path $scopeDir "unrelated-$runId.txt"
[IO.File]::WriteAllText($neighbor, $runId)
$neighborHash = (Get-FileHash -LiteralPath $neighbor -Algorithm SHA256).Hash
$uiReceipts = [Collections.Generic.List[object]]::new()
$stageReceipts = [Collections.Generic.List[object]]::new()
$preparationReceipts = [Collections.Generic.List[object]]::new()
Add-Type -AssemblyName System.Windows.Forms, System.Drawing
function Same-Path([string]$Left, [string]$Right) {
    return [IO.Path]::GetFullPath($Left).TrimEnd('\').Equals([IO.Path]::GetFullPath($Right).TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)
}
function Save-DesktopImage([string]$Label) {
    $bounds = [Windows.Forms.Screen]::PrimaryScreen.Bounds
    $bitmap = [Drawing.Bitmap]::new($bounds.Width, $bounds.Height)
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    try {
        $graphics.CopyFromScreen($bounds.Location, [Drawing.Point]::Empty, $bounds.Size)
        $bitmap.Save((Join-Path $evidence "$Label.png"), [Drawing.Imaging.ImageFormat]::Png)
    } finally { $graphics.Dispose(); $bitmap.Dispose() }
}
function Installed-Layout([bool]$Legacy) {
    $app = [string](Read-ProductValue $installKey 'InstallLocation')
    Assert-That (-not [string]::IsNullOrWhiteSpace($app)) 'Installer did not write an actual InstallLocation.'
    $data = Read-ProductValue $dataKey 'DataRoot'
    if ($Legacy) {
        Assert-That ($null -eq $data -and (Same-Path $app $legacyApp)) 'Legacy upgrade changed the original layout.'
        Assert-That (-not (Test-Path -LiteralPath (Join-Path $app '.lazymind-installed'))) 'Legacy install acquired a new-layout flag.'
        $data = $legacyRoot
    } else {
        Assert-That ($data -is [string] -and (Same-Path $data (Join-Path $storageRoot 'Data'))) 'Actual DataRoot is not the selected sibling Data.'
        Assert-That (Same-Path $app (Join-Path $storageRoot 'App')) 'Actual InstallLocation is not exactly App (extra product subdirectory?).'
        $flag = Get-Item -LiteralPath (Join-Path $app '.lazymind-installed')
        Assert-That ($flag.Length -eq 0) 'New-layout App flag is absent or not empty.'
        Assert-That ((Get-Content -LiteralPath (Join-Path $data '.lazymind-data-owner') -Raw).Trim() -ceq $currentSid) 'Wrong Data owner marker.'
    }
    Assert-That (Test-Path -LiteralPath (Join-Path $app 'LazyMind.exe') -PathType Leaf) 'Public app executable is missing.'
    return @{ App = $app; Data = [string]$data; Legacy = $Legacy }
}
function Assert-NoSystemBusinessData {
    foreach ($relative in @('data', 'state', 'Desktop', 'Cache', 'deps', 'run')) {
        Assert-That (-not (Test-Path -LiteralPath (Join-Path $legacyRoot $relative))) "Unexpected system-volume business data: $relative."
    }
    Assert-That (-not (Test-Path -LiteralPath $installerCache)) 'A complete installer was cached on the system volume.'
    Assert-That (-not (Test-Path -LiteralPath (Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'LazyMind'))) 'New install created the legacy managed import root.'
    foreach ($profile in $roamingProfiles) {
        Assert-That (@(Get-ChildItem -LiteralPath $profile -File -Recurse -Force -ErrorAction SilentlyContinue).Count -eq 0) "A fallback Electron profile/identity was created in $profile."
    }
}
function Install-Package([string]$Label, [bool]$Legacy, [bool]$Upgrade, [string]$Mode = '--simple-install') {
    $arguments = @($Mode)
    if ($Upgrade -and $UpgradeEntry -eq 'Updated') { $arguments += '--updated' }
    $startedUtc = [DateTime]::UtcNow
    $process = Start-Process -FilePath $packages.$Label.path -ArgumentList $arguments -PassThru
    $expectedPage = if (-not $Legacy -and -not $Upgrade) { 'shown' } else { 'hidden' }
    Write-Host "Package $Label: expected directory page=$expectedPage. New installs must select EXACTLY $storageRoot. Leave the installer open for evidence."
    $observed = Read-Host 'Enter shown/hidden after inspecting the real installer (no silent invocation)'
    Save-DesktopImage "installer-$Label-$($uiReceipts.Count)"
    Assert-That ($observed -ceq $expectedPage) 'Directory-page lifecycle contract failed.'
    $uiReceipts.Add(@{ package = $Label; expected = $expectedPage; observed = $observed; entry = $UpgradeEntry })
    [void](Read-Host 'Press Enter BEFORE clicking Install, then finish setup with Launch LazyMind UNCHECKED; keep the selected root')
    $deadline = [DateTime]::UtcNow.AddMinutes(35)
    $sawPackage = $false
    $sawOldApp = $false
    $warmupProcesses = @{}
    $sawNormalLaunch = $false
    $expectedApp = if ($Legacy) { $legacyApp } else { Join-Path $storageRoot 'App' }
    while (-not $process.HasExited) {
        Assert-That ([DateTime]::UtcNow -lt $deadline) 'Installer timed out; inspect the disposable VM before retry.'
        foreach ($child in @(Get-CimInstance Win32_Process -Filter "Name='LazyMind.exe' OR Name='LazyMindApp.exe'")) {
            if (-not $child.ExecutablePath -or -not $child.ExecutablePath.StartsWith($expectedApp.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) { continue }
            if ($child.CommandLine -match '(?:^|\s)--installer-warmup(?:\s|$)') {
                $warmupProcesses[$child.ProcessId] = @{ pid = $child.ProcessId; executable = $child.ExecutablePath; commandLine = $child.CommandLine }
            } elseif ($child.CommandLine -notmatch '(?:^|\s)--type=') {
                $sawNormalLaunch = $true
            }
        }
        if (-not $Legacy) {
            $stage = Join-Path $storageRoot '.install-tmp'
            if (Test-Path -LiteralPath $stage) {
                foreach ($file in @(Get-ChildItem -LiteralPath $stage -File -Depth 2 -ErrorAction SilentlyContinue)) {
                    if ($file.Name -like 'app-*.7z' -or $file.Name -like 'app-*.zip') { $sawPackage = $true }
                    if ($file.Name -eq 'LazyMind.exe') { $sawOldApp = $true }
                }
            }
            $wrongStage = @(Get-ChildItem -Path "$env:TEMP\ns*.tmp\app-*.7z", "$env:TEMP\ns*.tmp\app-*.zip" -File -ErrorAction SilentlyContinue)
            Assert-That ($wrongStage.Count -eq 0) 'Large installer payload appeared in system TEMP.'
        }
        Start-Sleep -Milliseconds 200
        $process.Refresh()
    }
    Assert-That ($process.ExitCode -eq 0) "Installer $Label failed: $($process.ExitCode). No automatic cleanup/retry."
    Assert-That ((Read-ProductValue $uninstallKey 'DisplayVersion') -ceq $packages.$Label.version) 'Installed version differs from the pinned manifest.'
    Assert-That (-not $sawNormalLaunch -and @(Get-Process | Where-Object { $_.ProcessName -in @('LazyMind', 'LazyMindApp') }).Count -eq 0) 'Finish launched the normal app before installation evidence. Repeat in a fresh disposable account with Launch unchecked.'
    if (-not $Legacy) {
        Assert-That $sawPackage 'Selected-volume package staging was not observed; staging remains unverified.'
        if ($Upgrade -and $Case -eq 'V2Upgrade') { Assert-That $sawOldApp 'No old-App staging was observed on the selected volume.' }
        Assert-NoSystemBusinessData
    }
    $stageReceipts.Add(@{ package = $Label; selectedVolumePackageObserved = $sawPackage; selectedVolumeOldAppObserved = $sawOldApp })
    $layout = Installed-Layout $Legacy
    # Capture fresh-install preparation before any normal application launch.
    # python_payload.go names the archive/marker and requires both expanded roots.
    if (-not $Legacy -and -not $Upgrade) {
        $resources = Join-Path $layout.App 'resources\runtime'
        $archive = Join-Path $resources 'python-runtime.zip'
        $ready = Join-Path $resources '.python-runtime-ready'
        $runtimePython = Join-Path $resources 'runtimes\python'
        $venvPython = Join-Path $resources 'deps\python'
        $runtimeFiles = @(Get-ChildItem -LiteralPath $runtimePython -File -Recurse -Force -ErrorAction SilentlyContinue | Select-Object -First 1).Count -gt 0
        $venvFiles = @(Get-ChildItem -LiteralPath $venvPython -File -Recurse -Force -ErrorAction SilentlyContinue | Select-Object -First 1).Count -gt 0
        $prepared = @{ package = $Label; mode = $Mode; archivePresent = (Test-Path -LiteralPath $archive); markerPresent = (Test-Path -LiteralPath $ready); runtimeFilesPresent = $runtimeFiles; venvFilesPresent = $venvFiles; warmupProcesses = @($warmupProcesses.Values) }
        if ($Mode -eq '--simple-install') {
            Assert-That ((Test-Path -LiteralPath $archive -PathType Leaf) -and (Get-Item -LiteralPath $archive).Length -gt 0) 'Simple install lost its deferred Python archive before first launch.'
            Assert-That (-not (Test-Path -LiteralPath $ready) -and -not $runtimeFiles -and -not $venvFiles) 'Simple install already expanded Python before first launch.'
            Assert-That ($warmupProcesses.Count -eq 0) 'Simple install unexpectedly ran installer warmup.'
        } else {
            Assert-That ($warmupProcesses.Count -gt 0) 'Full install never exposed an installer-warmup process during setup.'
            Assert-That ((Get-Content -LiteralPath $ready -Raw).Trim() -match '^\d+:\d+$') 'Full install has no valid Python payload-ready marker.'
            Assert-That ((Test-Path -LiteralPath $runtimePython -PathType Container) -and (Test-Path -LiteralPath $venvPython -PathType Container)) 'Full install did not expand both Python roots.'
            Assert-That (Test-Path -LiteralPath (Join-Path $venvPython 'auth-service\Scripts\python.exe') -PathType Leaf) 'Full install did not prepare the real Python executable.'
            $metricsFile = Join-Path $layout.Data 'Logs\desktop\startup-metrics-latest.json'
            $metrics = Get-Content -LiteralPath $metricsFile -Raw | ConvertFrom-Json
            Assert-That ($metrics.launchKind -ceq 'installer-warmup' -and $metrics.outcome -ceq 'success') 'Full setup has no successful installer-warmup metrics before first launch.'
            Assert-That ([DateTime]::Parse($metrics.startedAt).ToUniversalTime() -ge $startedUtc -and $metrics.milestones.PSObject.Properties['installerWarmupCompletedMs']) 'Warmup metrics are stale or lack completion evidence.'
            $prepared.metricsSHA256 = (Get-FileHash -LiteralPath $metricsFile -Algorithm SHA256).Hash
            $prepared.readySHA256 = (Get-FileHash -LiteralPath $ready -Algorithm SHA256).Hash
            Copy-Item -LiteralPath $metricsFile -Destination (Join-Path $evidence "installation-warmup-$Label.json")
        }
        $preparationReceipts.Add($prepared)
        $prepared | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $evidence "before-first-launch-$Label.json") -Encoding utf8
    }
    return $layout
}
function Start-ObservedApp($Layout) {
    if (@(Get-Process | Where-Object { $_.ProcessName -in @('LazyMind', 'LazyMindApp') }).Count -ne 0) {
        Write-Host 'The Finish page launched Desktop. Exit that test app via its tray before the public-entry smoke.'
        Close-ObservedApp $Layout
    }
    Assert-That (@(Get-Process | Where-Object { $_.ProcessName -in @('LazyMind', 'LazyMindApp') }).Count -eq 0) 'Another Desktop is running; refusing a second-instance smoke.'
    # Import the existing export explicitly: its current CLI file:// comparison
    # does not match Windows drive paths. Node self-tests exercise this entry,
    # without executing PowerShell or a Windows application.
    $moduleSmoke = @'
import { pathToFileURL } from "node:url";
const [script, app, runtimeRoot] = process.argv.slice(1);
const { runPackagedAppSmoke } = await import(pathToFileURL(script).href);
const result = await runPackagedAppSmoke({ app, runtimeRoot, platform: "win32", leaveRunning: true, timeoutMs: 1800000 });
if (!result?.gateway || result.state?.profile !== "desktop") throw new Error("Packaged smoke returned no desktop verification");
console.log(JSON.stringify({ verified: true, gateway: result.gateway, profile: result.state.profile }));
'@
    & node --input-type=module -e $moduleSmoke (Join-Path $PSScriptRoot 'packaged-app-smoke.mjs') (Join-Path $Layout.App 'LazyMind.exe') $Layout.Data | Out-Host
    Assert-That ($LASTEXITCODE -eq 0) 'Actual application startup/API smoke failed.'
    $state = Get-Content -LiteralPath (Join-Path $Layout.Data 'state\runtime-state.json') -Raw | ConvertFrom-Json
    $port = [int]$state.config.localProxy.port
    Assert-That ($port -gt 0 -and $port -le 65535) 'Actual runtime state has no local gateway.'
    $origin = "http://127.0.0.1:$port"
    $session = Invoke-RestMethod -Method Post -Uri "$origin/_local/admin-session"
    if ($session.PSObject.Properties['data']) { $session = $session.data }
    Assert-That (-not [string]::IsNullOrWhiteSpace($session.token)) 'Real local-admin session returned no token.'
    return @{ Origin = $origin; Headers = @{ Authorization = "Bearer $($session.token)" }; Layout = $Layout }
}
function API($Session, [string]$Method, [string]$Path, $Body = $null) {
    Assert-That ($Path.StartsWith('/api/core/', [StringComparison]::Ordinal) -and -not $Path.Contains('..')) 'Only the observed local Core API is allowed.'
    $requestArgs = @{ Method = $Method; Uri = $Session.Origin + $Path; Headers = $Session.Headers; MaximumRedirection = 0 }
    if ($null -ne $Body) { $requestArgs.ContentType = 'application/json'; $requestArgs.Body = $Body | ConvertTo-Json -Depth 12 }
    $result = Invoke-RestMethod @requestArgs
    if ($result.PSObject.Properties['data']) { return $result.data }
    return $result
}
function Read-DownloadedComponent($Session) {
    # routes.go:363-365 / systemdeps/editable_ppt.go expose installDir and
    # chromiumPath; config.go persists editablePpt.installedDir under Data.
    $status = API $Session Get '/api/core/system-dependencies/editable-ppt'
    Assert-That ($status.installed -eq $true -and $status.runtimeLocal -eq $true) 'Downloaded editable-PPT dependency is not usable according to the actual API.'
    $expected = Join-Path $Session.Layout.Data 'deps\editable-ppt'
    Assert-That (Same-Path $status.installDir $expected) 'Downloaded dependency is outside the actual Data root.'
    $config = Get-Content -LiteralPath (Join-Path $Session.Layout.Data 'config\system-dependencies.json') -Raw | ConvertFrom-Json
    Assert-That (Same-Path $config.editablePpt.installedDir $status.installDir) 'The persisted dependency receipt points to a different installDir.'
    $manifestFile = Join-Path $status.installDir 'bundle-manifest.json'
    $manifest = Get-Content -LiteralPath $manifestFile -Raw | ConvertFrom-Json
    Assert-That ($manifest.platform -ceq 'windows' -and $manifest.arch -ceq 'x64') 'Downloaded bundle targets a different OS/architecture.'
    Assert-That ($status.chromiumPath.StartsWith($expected.TrimEnd('\') + '\browsers\', [StringComparison]::OrdinalIgnoreCase)) 'Downloaded Chromium is outside Data dependency storage.'
    $packageReceipts = @{}
    foreach ($name in @('pptxgenjs', 'playwright', 'echarts')) {
        $packageFile = Join-Path $expected "node_modules\$name\package.json"
        $package = Get-Content -LiteralPath $packageFile -Raw | ConvertFrom-Json
        Assert-That (-not [string]::IsNullOrWhiteSpace($package.version)) "Downloaded $name has no package version."
        $packageReceipts[$name] = @{ version = $package.version; sha256 = (Get-FileHash -LiteralPath $packageFile -Algorithm SHA256).Hash }
    }
    return @{
        installDir = $status.installDir; chromiumPath = $status.chromiumPath
        manifestSHA256 = (Get-FileHash -LiteralPath $manifestFile -Algorithm SHA256).Hash
        chromiumSHA256 = (Get-FileHash -LiteralPath $status.chromiumPath -Algorithm SHA256).Hash
        packages = $packageReceipts
    }
}
function Prepare-DownloadedComponent($Session) {
    $before = API $Session Get '/api/core/system-dependencies/editable-ppt'
    Assert-That ($before.installed -eq $false -and $before.installSupported -eq $true) 'A fresh real component download cannot be demonstrated; dependency was preinstalled or installation is unsupported.'
    # No local bundle injection: preflight rejects BUNDLE_PATH. Invoke the real
    # catalog/checksum-backed installer, not a fake endpoint or file-copy seed.
    Write-Host 'Downloading the real editable-PPT component through the local system-dependencies API; network access and its bundle must be available.'
    $installed = API $Session Post '/api/core/system-dependencies/editable-ppt:install'
    Assert-That ($installed.installed -eq $true) 'The real downloaded-component install request failed.'
    return Read-DownloadedComponent $Session
}
function Prepare-Witness($Session) {
    $component = Prepare-DownloadedComponent $Session
    $source = Join-Path $evidence "attachment-$runId.txt"
    [IO.File]::WriteAllText($source, "LazyMind storage witness $runId", [Text.UTF8Encoding]::new($false))
    Write-Host "In the REAL UI create a persisted chat containing $runId and attach $source. A functioning test chat configuration is required."
    $conversation = Read-Host 'Enter the resulting conversation ID'
    Assert-That ($conversation -match '^[A-Za-z0-9_-]+$') 'Invalid conversation ID.'
    $history = API $Session Get "/api/core/conversations/$($conversation):history"
    $text = $history | ConvertTo-Json -Depth 40 -Compress
    Assert-That ($history.history.Count -gt 0 -and $text.Contains($runId)) 'Real API did not return the persisted chat witness.'
    $attachment = Read-Host 'Enter the attachment download path from that history, beginning /api/core/'
    Assert-That ($text.Contains($attachment)) 'Attachment path is not referenced by the persisted history.'
    API $Session Patch '/api/core/user/chat-settings' @{ enable_tool_retrieval = $true } | Out-Null
    return @{ Conversation = $conversation; Attachment = $attachment; AttachmentSHA256 = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash; IdentitySHA256 = (Get-FileHash -LiteralPath (Join-Path $Session.Layout.Data 'Desktop\credential-device.json') -Algorithm SHA256).Hash; Component = $component }
}
function Check-Witness($Session, $Witness, [string]$Label) {
    $history = API $Session Get "/api/core/conversations/$($Witness.Conversation):history"
    $text = $history | ConvertTo-Json -Depth 40 -Compress
    Assert-That ($text.Contains($runId) -and $text.Contains($Witness.Attachment)) 'Persisted chat or attachment reference was lost.'
    $download = Join-Path $evidence "attachment-read-$Label.txt"
    Assert-That ($Witness.Attachment.StartsWith('/api/core/', [StringComparison]::Ordinal) -and -not $Witness.Attachment.Contains('..')) 'Unsafe attachment URL.'
    Invoke-WebRequest -Uri ($Session.Origin + $Witness.Attachment) -Headers $Session.Headers -MaximumRedirection 0 -OutFile $download
    Assert-That ((Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash -ceq $Witness.AttachmentSHA256) 'Downloaded attachment changed.'
    $settings = API $Session Get '/api/core/user/chat-settings'
    Assert-That ($settings.enable_tool_retrieval -eq $true) 'Persisted fake preference was lost.'
    Assert-That ((Get-FileHash -LiteralPath (Join-Path $Session.Layout.Data 'Desktop\credential-device.json') -Algorithm SHA256).Hash -ceq $Witness.IdentitySHA256) 'Device identity changed.'
    $python = Join-Path $Session.Layout.App 'resources\runtime\deps\python\auth-service\Scripts\python.exe'
    Assert-That (Test-Path -LiteralPath $python -PathType Leaf) 'Prepared bundled Python is missing.'
    & $python -I -B -c 'import sys; assert sys.prefix != sys.base_prefix; print(sys.prefix)' | Out-Host
    Assert-That ($LASTEXITCODE -eq 0) 'Actual prepared Python dependency cannot start.'
    # Only read/check after upgrades or keep/reinstall. Reinstalling here would
    # conceal a lost download or a changed dependency location.
    $component = Read-DownloadedComponent $Session
    foreach ($field in @('installDir', 'chromiumPath', 'manifestSHA256', 'chromiumSHA256')) {
        Assert-That ($component[$field] -ceq $Witness.Component[$field]) "Downloaded component receipt changed: $field."
    }
    foreach ($name in @('pptxgenjs', 'playwright', 'echarts')) {
        Assert-That ($component.packages[$name].version -ceq $Witness.Component.packages[$name].version -and $component.packages[$name].sha256 -ceq $Witness.Component.packages[$name].sha256) "Downloaded $name version/content changed."
    }
    Assert-That ((Get-FileHash -LiteralPath $neighbor -Algorithm SHA256).Hash -ceq $neighborHash) 'An unrelated file outside App/Data changed.'
    if (-not $Session.Layout.Legacy) { Assert-NoSystemBusinessData }
}
function Close-ObservedApp($Layout) {
    [void](Read-Host 'Exit this test Desktop with its tray Exit command, then press Enter')
    $deadline = [DateTime]::UtcNow.AddMinutes(2)
    do {
        $running = @(Get-Process | Where-Object {
            try { $_.Path -and ($_.Path.StartsWith($Layout.App.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase) -or $_.Path.StartsWith($Layout.Data.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) } catch { $false }
        })
        if ($running.Count -eq 0) { return }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw 'Owned app processes did not stop. No global taskkill or automatic retry is performed.'
}
function Uninstall-ObservedApp($Layout, [bool]$Purge) {
    $process = Start-Process -FilePath (Join-Path $Layout.App 'Uninstall LazyMind.exe') -PassThru
    $choice = if ($Purge) { 'purge' } else { 'keep' }
    Assert-That ((Read-Host "Select $choice in the REAL data page, leave it open, then enter $choice") -ceq $choice) 'Uninstall choice was not confirmed.'
    Save-DesktopImage "uninstall-$choice"
    [void](Read-Host 'Press Enter, then finish the uninstaller')
    Assert-That ($process.WaitForExit(600000)) 'Uninstaller timed out; inspect the VM before retry.'
    Assert-That ($process.ExitCode -eq 0) "Uninstaller failed: $($process.ExitCode)"
    Assert-That (-not (Test-Path -LiteralPath (Join-Path $Layout.App 'LazyMind.exe'))) 'Public executable remains after uninstall.'
    Assert-That ($null -eq (Read-ProductValue $installKey 'InstallLocation')) 'Standard program registration remains.'
    if ($Purge) {
        Assert-That (-not (Test-Path -LiteralPath $Layout.Data) -and $null -eq (Read-ProductValue $dataKey 'DataRoot')) 'Purge did not remove owned Data and its locator.'
    } else {
        Assert-That (Test-Path -LiteralPath $Layout.Data) 'Keep uninstall removed Data.'
        Assert-That (Same-Path ([string](Read-ProductValue $dataKey 'DataRoot')) $Layout.Data) 'Keep uninstall removed/changed DataRoot.'
    }
    Assert-That ((Get-FileHash -LiteralPath $neighbor -Algorithm SHA256).Hash -ceq $neighborHash) 'Uninstall changed a neighboring user file.'
}
function Assert-LocationFailure([string]$Executable, $Layout, $Witness, [string]$Label) {
    Assert-That (Test-Path -LiteralPath (Join-Path $Layout.App '.lazymind-installed')) 'App flag disappeared before the entry-point failure check.'
    Assert-That (-not [Environment]::GetEnvironmentVariable('LAZYMIND_DESKTOP_DATA_ROOT') -and -not [Environment]::GetEnvironmentVariable('ELECTRON_RUN_AS_NODE')) 'Failure check requires real GUI mode without an injected data root.'
    $failed = Start-Process -FilePath $Executable -PassThru
    $observed = Read-Host "For $Label enter location-error ONLY if the real error identifies missing installation/data location; generic launch failures are not sufficient"
    Save-DesktopImage $Label
    Assert-That ($observed -ceq 'location-error') 'No specific location-failure evidence was supplied.'
    [void](Read-Host 'Dismiss the location error and press Enter')
    Assert-That ($failed.WaitForExit(30000) -and $failed.ExitCode -ne 0) "$Label did not fail closed."
    Assert-NoSystemBusinessData
    Assert-That ((Get-FileHash -LiteralPath (Join-Path $Layout.Data 'Desktop\credential-device.json') -Algorithm SHA256).Hash -ceq $Witness.IdentitySHA256) "$Label reset the original identity."
}
$legacy = $Case -eq 'LegacyUpgrade'
$mode = if ($Case -eq 'NewFull') { '--full-install' } else { '--simple-install' }
$first = if ($legacy) { 'L' } else { 'A' }
$layout = Install-Package $first $legacy $false $mode
$session = Start-ObservedApp $layout
$witness = Prepare-Witness $session
Check-Witness $session $witness 'before'
Close-ObservedApp $layout
switch ($Case) {
    { $_ -in @('LegacyUpgrade', 'V2Upgrade') } {
        $layout = Install-Package 'B' $legacy $true
        $session = Start-ObservedApp $layout
        Check-Witness $session $witness 'after-upgrade'
        Close-ObservedApp $layout
    }
    'KeepReinstall' {
        Uninstall-ObservedApp $layout $false
        Assert-That ((Get-FileHash -LiteralPath (Join-Path $layout.Data 'Desktop\credential-device.json') -Algorithm SHA256).Hash -ceq $witness.IdentitySHA256) 'Keep uninstall changed identity.'
        $layout = Install-Package 'B' $false $true
        $session = Start-ObservedApp $layout
        Check-Witness $session $witness 'after-reinstall'
        Close-ObservedApp $layout
    }
    'Purge' { Uninstall-ObservedApp $layout $true }
    'FailureBoundary' {
        Assert-That (Same-Path ([string](Read-ProductValue $dataKey 'DataRoot')) $layout.Data) 'Locator is not owned by this test run.'
        Assert-That (Test-Path -LiteralPath (Join-Path $layout.App '.lazymind-installed')) 'No new-layout flag; cannot test missing-locator protection.'
        # Bypass the launcher while the real record is still valid. Internal
        # Electron must reject the absent dedicated argument, not initialize C.
        Assert-LocationFailure (Join-Path $layout.App 'LazyMindApp.exe') $layout $witness 'internal-without-root'
        # Sole deliberate registry mutation: only the value created by this run,
        # after clean-user and exact-path checks. It is intentionally not repaired.
        Remove-ItemProperty -LiteralPath 'HKCU:\Software\LazyMind\DesktopStorage' -Name DataRoot
        Assert-LocationFailure (Join-Path $layout.App 'LazyMind.exe') $layout $witness 'public-without-locator'
        Assert-LocationFailure (Join-Path $layout.App 'LazyMindApp.exe') $layout $witness 'internal-without-locator'
    }
}
$os = Get-CimInstance Win32_OperatingSystem
@{
    case = $Case; upgradeEntry = $UpgradeEntry; scope = $storageRoot; automation = 'manual-required'; caseChecks = 'completed'
    os = $os.Caption; osBuild = $os.BuildNumber; desktopTarget = $os.ProductType -eq 1
    targetVolume = $targetVolume[0].DeviceID; systemVolume = $systemVolume[0].DeviceID
    packages = $packages; ui = $uiReceipts.ToArray(); staging = $stageReceipts.ToArray(); beforeFirstLaunch = $preparationReceipts.ToArray(); witness = $witness
    excluded = @('W3 full feature matrix', 'W9 other-user/ACL failures', 'W10 interrupted replacement', 'W11 external-agent/Local', 'W12 ZIP/non-Windows')
} | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath (Join-Path $evidence 'result.json') -Encoding utf8
Write-Host "Interactive case $Case completed its explicit checks; this is not unattended acceptance. Other cases remain unverified. Evidence: $evidence"
