[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('doctor', 'build', 'up', 'up-lan', 'down', 'status', 'clean', 'reset')]
    [string]$Action
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$buildRoot = Join-Path $repoRoot 'local\build'
$managerDir = Join-Path $repoRoot 'local\local-runtime-manager'
$managerBin = Join-Path $buildRoot 'bin\local-runtime-manager.exe'
$configFile = Join-Path $repoRoot 'local\config.env'
$configExample = Join-Path $repoRoot 'local\config.env.example'

function Import-EnvFile([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return }
    foreach ($line in Get-Content -LiteralPath $Path) {
        $text = $line.Trim()
        if (-not $text -or $text.StartsWith('#')) { continue }
        if ($text.StartsWith('export ')) { $text = $text.Substring(7).Trim() }
        $separator = $text.IndexOf('=')
        if ($separator -le 0) { continue }
        $name = $text.Substring(0, $separator).Trim()
        if ($name -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') { continue }
        if ([Environment]::GetEnvironmentVariable($name, 'Process')) { continue }
        $value = $text.Substring($separator + 1).Trim()
        if ($value.Length -ge 2 -and (($value[0] -eq '"' -and $value[$value.Length - 1] -eq '"') -or ($value[0] -eq "'" -and $value[$value.Length - 1] -eq "'"))) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        $value = $value.Replace('$(HOME)', $HOME).Replace('$(CURDIR)', $repoRoot)
        [Environment]::SetEnvironmentVariable($name, $value, 'Process')
    }
}

function Initialize-Environment {
    # Scoop updates the user PATH, but the current terminal may predate an
    # installation. Discover its native Node/Corepack paths without requiring
    # users to restart PowerShell.
    $scoopNode = Join-Path $HOME 'scoop\apps\nodejs-lts\current'
    if (Test-Path -LiteralPath (Join-Path $scoopNode 'node.exe')) {
        $corepackShims = Join-Path $scoopNode 'node_modules\corepack\shims'
        $env:PATH = "$scoopNode;$corepackShims;$env:PATH"
    }

    if (-not (Test-Path -LiteralPath $configFile -PathType Leaf)) {
        New-Item -ItemType Directory -Force -Path (Split-Path $configFile) | Out-Null
        Copy-Item -LiteralPath $configExample -Destination $configFile
        Write-Host "Created local/config.env from local/config.env.example"
    }

    $profile = $env:MIRROR_PROFILE
    if (-not $profile) {
        $envFile = Join-Path $repoRoot '.env'
        if (Test-Path -LiteralPath $envFile) {
            $profileLine = Get-Content -LiteralPath $envFile | Where-Object { $_ -match '^\s*MIRROR_PROFILE\s*=' } | Select-Object -First 1
            if ($profileLine) { $profile = ($profileLine -split '=', 2)[1].Trim() }
        }
    }
    if (-not $profile) { $profile = 'cn' }

    Import-EnvFile (Join-Path $repoRoot ".env.mirrors.$profile")
    Import-EnvFile (Join-Path $repoRoot '.env')
    Import-EnvFile $configFile
    Import-EnvFile (Join-Path $repoRoot 'local\config.win.env')

    $env:MIRROR_PROFILE = $profile
    $env:CGO_ENABLED = '0'
    $env:LAZYMIND_LOCAL_BUILD_ROOT = $buildRoot
}

function Assert-Command([string]$Name, [string]$Hint) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Missing required command '$Name'. $Hint"
    }
}

function Invoke-Doctor {
    if (-not [Environment]::Is64BitOperatingSystem) { throw 'LazyMind local runtime requires 64-bit Windows.' }
    Assert-Command 'git.exe' 'Install Git for Windows.'
    Assert-Command 'go.exe' 'Install Go 1.25 or newer.'
    Assert-Command 'uv.exe' 'Install uv (https://docs.astral.sh/uv/).'
    Assert-Command 'node.exe' 'Install Node.js 20 or newer.'
    Assert-Command 'pnpm.cmd' 'Install pnpm.'
    Write-Host 'Windows local runtime prerequisites are available (CGO is disabled).'
}

function Build-Manager {
    Invoke-Doctor
    New-Item -ItemType Directory -Force -Path (Split-Path $managerBin) | Out-Null
    Push-Location $managerDir
    try {
        & go.exe build -buildvcs=false -o $managerBin .
        if ($LASTEXITCODE -ne 0) { throw "local-runtime-manager build failed with exit code $LASTEXITCODE" }
    } finally {
        Pop-Location
    }
}

function Invoke-Manager([string[]]$Arguments) {
    if (-not (Test-Path -LiteralPath $managerBin -PathType Leaf)) {
        throw "Local runtime manager was not built: $managerBin"
    }
    & $managerBin @Arguments
    if ($LASTEXITCODE -ne 0) { throw "local-runtime-manager exited with code $LASTEXITCODE" }
}

function Remove-BuildRoot {
    if (-not (Test-Path -LiteralPath $buildRoot)) { return }
    $resolvedRepo = [IO.Path]::GetFullPath($repoRoot).TrimEnd('\') + '\'
    $resolvedBuild = [IO.Path]::GetFullPath($buildRoot)
    if (-not $resolvedBuild.StartsWith($resolvedRepo, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove path outside repository: $resolvedBuild"
    }
    Remove-Item -LiteralPath $resolvedBuild -Recurse -Force
}

Initialize-Environment
switch ($Action) {
    'doctor' { Invoke-Doctor }
    'build' { Build-Manager }
    'up' { Build-Manager; Invoke-Manager @('up') }
    'up-lan' {
        $env:LAZYMIND_LOCAL_NETWORK_PROFILE = 'lan'
        $env:LAZYMIND_LOCAL_AUTO_LOGIN_ALLOW_LAN = 'true'
        Build-Manager
        Invoke-Manager @('up')
    }
    'down' {
        if (Test-Path -LiteralPath $managerBin -PathType Leaf) { Invoke-Manager @('down') }
        else { Write-Host 'No Windows local runtime manager found; skipping.' }
    }
    'status' {
        if (Test-Path -LiteralPath $managerBin -PathType Leaf) { Invoke-Manager @('status') }
        else { Write-Host 'No Windows local runtime manager found.' }
    }
    'clean' { Remove-BuildRoot }
    'reset' {
        Build-Manager
        try { Invoke-Manager @('reset', '--scope', 'all') } finally { Remove-BuildRoot }
    }
}
