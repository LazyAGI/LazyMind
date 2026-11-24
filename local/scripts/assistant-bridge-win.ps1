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
$sidecar = Join-Path (Split-Path -Parent $SourcePath) 'workflow-connectors'
$sidecarHosts = @()
if (Test-Path -LiteralPath $sidecar -PathType Container) {
    foreach ($connectorHost in @('codex', 'workbuddy')) {
        $hostPath = Join-Path $sidecar $connectorHost
        try {
            $manifest = Join-Path $hostPath 'connector.json'
            if (-not (Test-Path -LiteralPath $manifest -PathType Leaf)) {
                throw "Missing installation record: $manifest"
            }
            # Include module contents so a damaged or repaired host gets its
            # own launcher directory even when the manifest is unchanged.
            $directory = Get-Item -LiteralPath $hostPath
            $files = Get-ChildItem -LiteralPath $directory.FullName -File -Recurse -Force | Sort-Object FullName
            $fingerprint = foreach ($file in $files) {
                $relative = $file.FullName.Substring($directory.FullName.Length + 1)
                $fileDigest = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
                "$relative=$fileDigest"
            }
            $hasher = [Security.Cryptography.SHA256]::Create()
            try {
                $hash = $hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($fingerprint -join "`n"))
                $hostDigest = [BitConverter]::ToString($hash).Replace('-', '').ToLowerInvariant()
            } finally {
                $hasher.Dispose()
            }
            $digest += "-$connectorHost-$($hostDigest.Substring(0, 16))"
            $sidecarHosts += $connectorHost
        } catch {
            [Console]::Error.WriteLine("Workflow Connector source package ($connectorHost) ${hostPath}: $($_.Exception.Message)")
        }
    }
}
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
foreach ($connectorHost in $sidecarHosts) {
    $installedSidecar = Join-Path $versionDirectory 'workflow-connectors'
    $hostPath = Join-Path $sidecar $connectorHost
    $installedHost = Join-Path $installedSidecar $connectorHost
    if (Test-Path -LiteralPath $installedHost -PathType Container) { continue }
    $temporaryHost = "$installedHost.$PID.tmp"
    try {
        New-Item -ItemType Directory -Force -Path $installedSidecar | Out-Null
        Copy-Item -LiteralPath $hostPath -Destination $temporaryHost -Recurse
        [IO.Directory]::Move($temporaryHost, $installedHost)
    } catch {
        if (-not (Test-Path -LiteralPath $installedHost -PathType Container)) {
            [Console]::Error.WriteLine("Workflow Connector staged package ($connectorHost) ${installedHost}: $($_.Exception.Message)")
        }
    } finally {
        Remove-Item -LiteralPath $temporaryHost -Recurse -Force -ErrorAction SilentlyContinue
    }
}
Set-Content -LiteralPath $receiptPath -Value $installedBinary -Encoding UTF8
Invoke-Bridge $installedBinary (@('assistant', 'start') + $clientArguments) | Out-Null
$status = Invoke-Bridge $installedBinary @('assistant', 'status') | ConvertFrom-Json
if ($status.running -isnot [bool] -or -not $status.running -or $status.platform -ne 'windows') {
    throw 'Native Windows Assistant Bridge health verification failed.'
}
Write-Output 'LAZYMIND_ASSISTANT_BRIDGE_OK:start'
