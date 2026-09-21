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
$installedBinary = Join-Path $installDir 'lazymind.exe'

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
    if ($binary) { Invoke-Bridge $binary @('assistant', 'stop') | Out-Null }
    Write-Output 'LAZYMIND_ASSISTANT_BRIDGE_OK:stop'
    return
}

if (-not (Test-Path -LiteralPath $SourcePath -PathType Leaf)) {
    throw "Windows Assistant Bridge build was not found: $SourcePath"
}

# Stop either an existing native Bridge or a stale WSL-forwarded Bridge before
# replacing the executable. The CLI waits until the loopback listener closes.
Invoke-Bridge $SourcePath @('assistant', 'stop') | Out-Null
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$temporary = "$installedBinary.$PID.tmp"
try {
    Copy-Item -LiteralPath $SourcePath -Destination $temporary -Force
    Move-Item -LiteralPath $temporary -Destination $installedBinary -Force
} finally {
    Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue
}
Invoke-Bridge $installedBinary @('assistant', 'start') | Out-Null
$status = Invoke-Bridge $installedBinary @('assistant', 'status') | ConvertFrom-Json
if ($status.running -isnot [bool] -or -not $status.running -or $status.platform -ne 'windows') {
    throw 'Native Windows Assistant Bridge health verification failed.'
}
Write-Output 'LAZYMIND_ASSISTANT_BRIDGE_OK:start'
