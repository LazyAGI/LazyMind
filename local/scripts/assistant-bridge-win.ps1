[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('start', 'stop')]
    [string]$Action,

    [Parameter(Mandatory = $true, Position = 1)]
    [string]$SourcePath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

if (-not $env:LOCALAPPDATA) {
    throw 'LOCALAPPDATA is unavailable; cannot stage the native Windows Assistant Bridge.'
}

$installDir = Join-Path $env:LOCALAPPDATA 'LazyMind\assistant-bridge'
$receiptPath = Join-Path $installDir 'launcher-path.txt'
$installedBinary = Join-Path $installDir 'lazymind.exe'
if (Test-Path -LiteralPath $receiptPath -PathType Leaf) {
    $installedBinary = (Get-Content -LiteralPath $receiptPath -Raw).Trim()
    if (-not $installedBinary.StartsWith($installDir + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Invalid Assistant Bridge launcher receipt.' }
}
$bridgePort = if ($env:LAZYMIND_WSL_BRIDGE_PORT) { $env:LAZYMIND_WSL_BRIDGE_PORT } else { '8090' }
if ($bridgePort -notmatch '^\d{1,5}$' -or [int]$bridgePort -lt 1 -or [int]$bridgePort -gt 65535) { throw 'Invalid LazyMind frontend port.' }
$deployment = if ($env:LAZYMIND_WSL_BRIDGE_DEPLOYMENT) { $env:LAZYMIND_WSL_BRIDGE_DEPLOYMENT } else { 'compose' }
if ($deployment -notin @('local', 'compose')) { throw 'Invalid LazyMind deployment type.' }
$clientArguments = @('--client-id', "$deployment`:$SourcePath`:$bridgePort")
if ($deployment -eq 'local') {
    $env:LAZYMIND_RUNTIME_PROFILE = 'desktop'
} else {
    Remove-Item Env:LAZYMIND_RUNTIME_PROFILE -ErrorAction SilentlyContinue
    $clientArguments += @('--server-url', "http://localhost:$bridgePort")
}

function Invoke-Bridge([string]$Path, [string[]]$Arguments) {
    $output = & $Path @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Assistant Bridge command failed with exit code $LASTEXITCODE"
    }
    return ($output -join [Environment]::NewLine)
}

# WSL environment paths are not valid Windows runtime roots. The native
# process must resolve its home and data paths from the active Windows user.
Remove-Item Env:LAZYMIND_HOME -ErrorAction SilentlyContinue
Remove-Item Env:LAZYMIND_LOCAL_BUILD_ROOT -ErrorAction SilentlyContinue

if ($Action -eq 'stop') {
    $binary = if (Test-Path -LiteralPath $installedBinary -PathType Leaf) {
        $installedBinary
    } elseif (Test-Path -LiteralPath $SourcePath -PathType Leaf) {
        $SourcePath
    } else {
        $null
    }
    if ($binary) { Invoke-Bridge $binary (@('assistant', 'stop') + $clientArguments) | Out-Null }
    Write-Output 'LAZYMIND_ASSISTANT_BRIDGE_OK:stop'
    return
}

if (-not (Test-Path -LiteralPath $SourcePath -PathType Leaf)) {
    throw "Windows Assistant Bridge build was not found: $SourcePath"
}

# Never replace an in-use executable or stop another deployment's bridge.
# The CLI negotiates reuse and stages the daemon independently of this launcher.
$digest = (Get-FileHash -LiteralPath $SourcePath -Algorithm SHA256).Hash.ToLowerInvariant()
$versionDirectory = Join-Path $installDir $digest
$installedBinary = Join-Path $versionDirectory 'lazymind.exe'
New-Item -ItemType Directory -Force -Path $versionDirectory | Out-Null
$temporary = "$installedBinary.$PID.tmp"
try {
    if (-not (Test-Path -LiteralPath $installedBinary -PathType Leaf)) {
        Copy-Item -LiteralPath $SourcePath -Destination $temporary -Force
        Move-Item -LiteralPath $temporary -Destination $installedBinary -Force
    }
} finally {
    Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue
}
Set-Content -LiteralPath $receiptPath -Value $installedBinary -Encoding UTF8
Invoke-Bridge $installedBinary (@('assistant', 'start') + $clientArguments) | Out-Null
$status = Invoke-Bridge $installedBinary @('assistant', 'status') | ConvertFrom-Json
if ($status.running -isnot [bool] -or -not $status.running -or $status.platform -ne 'windows') {
    throw 'Native Windows Assistant Bridge health verification failed.'
}
Write-Output 'LAZYMIND_ASSISTANT_BRIDGE_OK:start'
