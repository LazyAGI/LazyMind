[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('build', 'resume', 'doctor', 'clean', 'clean-all')]
    [string]$Action = 'build'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$targetRoot = Join-Path $repoRoot 'desktop\build\windows-x64'
$runtimeRoot = Join-Path $targetRoot 'runtime'
$distRoot = Join-Path $repoRoot 'desktop\dist'
$electronRoot = Join-Path $repoRoot 'desktop\electron'
$pythonVersion = '3.11.15'
$goBuildArgs = @('-trimpath', '-buildvcs=false', '-ldflags=-s -w')

function Assert-PathUnderRepo([string]$Path) {
    $repoPrefix = [IO.Path]::GetFullPath($repoRoot).TrimEnd('\') + '\'
    $resolved = [IO.Path]::GetFullPath($Path)
    if (-not $resolved.StartsWith($repoPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to modify path outside repository: $resolved"
    }
}

function Remove-GeneratedPath([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    Assert-PathUnderRepo $Path
    Remove-Item -LiteralPath $Path -Recurse -Force
}

function Remove-DistArtifacts([string]$Pattern) {
    if (-not (Test-Path -LiteralPath $distRoot -PathType Container)) { return }
    Get-ChildItem -LiteralPath $distRoot -File -Filter $Pattern -ErrorAction SilentlyContinue |
        ForEach-Object { Remove-GeneratedPath $_.FullName }
}

function New-WindowsZipFileName {
    $commit = (& git.exe -C $repoRoot rev-parse --short=8 HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $commit -notmatch '^[0-9a-fA-F]{7,}$') {
        throw 'Could not resolve the Git commit for the Windows Desktop artifact name.'
    }
    $timestamp = (Get-Date).ToString('yyyyMMdd-HHmmss', [Globalization.CultureInfo]::InvariantCulture)
    return "LazyMind-windows-x64-$timestamp-$commit.zip"
}

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
    $profile = $env:MIRROR_PROFILE
    if (-not $profile) { $profile = 'cn' }
    Import-EnvFile (Join-Path $repoRoot ".env.mirrors.$profile")
    Import-EnvFile (Join-Path $repoRoot '.env')
    Import-EnvFile (Join-Path $repoRoot 'local\config.env')
    Import-EnvFile (Join-Path $repoRoot 'local\config.win.env')
    $env:MIRROR_PROFILE = $profile
    $env:CGO_ENABLED = '0'
    $env:PYTHONDONTWRITEBYTECODE = '1'
    $env:UV_PYTHON_INSTALL_DIR = Join-Path $runtimeRoot 'runtimes\python'
    $env:LAZYMIND_DESKTOP_RUNTIME_STAGE = $runtimeRoot
    $env:LAZYMIND_DESKTOP_OUTPUT_DIR = $distRoot
    $env:LAZYMIND_DESKTOP_WINDOWS_ICON = Join-Path $targetRoot 'LazyMind.ico'
}

function Assert-Command([string]$Name, [string]$Hint) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Missing required command '$Name'. $Hint"
    }
}

function Invoke-Doctor {
    if ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne [Runtime.InteropServices.Architecture]::X64) {
        throw 'LazyMind Windows Desktop currently supports Windows x64 only.'
    }
    Assert-Command 'git.exe' 'Install Git for Windows.'
    Assert-Command 'go.exe' 'Install Go 1.25 or newer.'
    Assert-Command 'uv.exe' 'Install uv.'
    Assert-Command 'node.exe' 'Install Node.js 20 or newer.'
    Assert-Command 'pnpm.cmd' 'Install pnpm.'
    Write-Host 'Windows x64 Desktop build prerequisites are available (CGO is disabled).'
}

function Invoke-Native([string]$Command, [string[]]$Arguments, [string]$WorkingDirectory = $repoRoot) {
    Push-Location $WorkingDirectory
    try {
        & $Command @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$Command exited with code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
}

function Build-GoBinary([string]$Directory, [string]$Output, [string[]]$Package = @('.'), [switch]$WindowsGUI) {
    New-Item -ItemType Directory -Force -Path (Split-Path $Output) | Out-Null
    $buildArgs = $goBuildArgs
    if ($WindowsGUI) {
        $buildArgs = @('-trimpath', '-buildvcs=false', '-ldflags=-s -w -H=windowsgui')
    }
    Invoke-Native 'go.exe' (@('build') + $buildArgs + @('-o', $Output) + $Package) $Directory
}

function Assert-WindowsGUISubsystem([string]$Path) {
    $bytes = [IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -lt 256) { throw "Invalid PE binary: $Path" }
    $peOffset = [BitConverter]::ToInt32($bytes, 0x3c)
    $subsystemOffset = $peOffset + 24 + 68
    if ($peOffset -lt 0 -or $subsystemOffset + 2 -gt $bytes.Length) {
        throw "Invalid PE header: $Path"
    }
    $subsystem = [BitConverter]::ToUInt16($bytes, $subsystemOffset)
    if ($subsystem -ne 2) {
        throw "Desktop local-runtime-manager must use the Windows GUI subsystem; got PE subsystem $subsystem"
    }
}

function Install-GoTool([string]$Package) {
    $env:GOBIN = Join-Path $runtimeRoot 'bin'
    Invoke-Native 'go.exe' (@('install', '-trimpath', '-ldflags=-s -w', $Package))
}

function Prune-PythonTree([string]$Root) {
    if (-not (Test-Path -LiteralPath $Root)) { return }
    Get-ChildItem -LiteralPath $Root -Recurse -Directory -Force -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -in @('__pycache__', 'test', 'tests') } |
        Sort-Object FullName -Descending |
        ForEach-Object { Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction SilentlyContinue }
    Get-ChildItem -LiteralPath $Root -Recurse -File -Force -ErrorAction SilentlyContinue |
        Where-Object { $_.Extension -in @('.pyc', '.pyo') } |
        ForEach-Object { Remove-Item -LiteralPath $_.FullName -Force }
}

function Materialize-PythonAliases {
    $pythonRoot = Join-Path $runtimeRoot 'runtimes\python'
    $links = @(Get-ChildItem -LiteralPath $pythonRoot -Force -ErrorAction SilentlyContinue | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint })
    foreach ($link in $links) {
        $target = [string]($link.Target | Select-Object -First 1)
        if (-not [IO.Path]::IsPathRooted($target)) {
            $target = Join-Path (Split-Path $link.FullName) $target
        }
        $target = [IO.Path]::GetFullPath($target)
        $copy = $link.FullName + '.materialized'
        Remove-GeneratedPath $copy
        Copy-Item -LiteralPath $target -Destination $copy -Recurse -Force
        [IO.Directory]::Delete($link.FullName)
        Move-Item -LiteralPath $copy -Destination $link.FullName
    }
}

function Copy-RuntimeApp {
    $appRoot = Join-Path $runtimeRoot 'app'
    New-Item -ItemType Directory -Force -Path $appRoot | Out-Null
    $excludedDirs = @(
        (Join-Path $repoRoot '.git'),
        (Join-Path $repoRoot 'local\build'),
        (Join-Path $repoRoot 'local\runtime'),
        (Join-Path $repoRoot 'desktop\build'),
        (Join-Path $repoRoot 'desktop\cache'),
        (Join-Path $repoRoot 'desktop\dist'),
        (Join-Path $repoRoot 'desktop\electron\node_modules'),
        (Join-Path $repoRoot 'frontend\node_modules'),
        (Join-Path $repoRoot 'frontend\src'),
        (Join-Path $repoRoot 'frontend\public'),
        (Join-Path $repoRoot 'frontend\scripts'),
        (Join-Path $repoRoot 'algorithm\lazyllm\docs'),
        (Join-Path $repoRoot '.codex-gocache'),
        (Join-Path $repoRoot '.codex-gomodcache'),
        (Join-Path $repoRoot '.pnpm-store')
    )
    & robocopy.exe $repoRoot $appRoot /MIR /R:2 /W:1 /NFL /NDL /NJH /NJS /NP /XD @excludedDirs /XF '*.pyc' '*.pyo' '.DS_Store'
    if ($LASTEXITCODE -gt 7) { throw "robocopy runtime app staging failed with code $LASTEXITCODE" }
    $coreDevBinary = Join-Path $appRoot 'backend\core\core.exe'
    if (Test-Path -LiteralPath $coreDevBinary) { Remove-Item -LiteralPath $coreDevBinary -Force }
    if (-not (Test-Path -LiteralPath (Join-Path $appRoot 'frontend\dist\index.html') -PathType Leaf)) {
        throw 'Desktop frontend dist is missing from staged runtime app.'
    }
    if (-not (Test-Path -LiteralPath (Join-Path $appRoot 'algorithm\lazyllm\lazyllm') -PathType Container)) {
        throw 'Bundled LazyLLM source is missing from staged runtime app.'
    }
}

function Finalize-Desktop {
    $finalZipName = New-WindowsZipFileName
    Materialize-PythonAliases
    Prune-PythonTree (Join-Path $runtimeRoot 'runtimes\python')
    Prune-PythonTree (Join-Path $runtimeRoot 'deps\python')

    Write-Host '==> Staging runtime application files'
    Copy-RuntimeApp
    Invoke-Native 'node.exe' @((Join-Path $repoRoot 'desktop\scripts\write-runtime-manifest.mjs'), $runtimeRoot, '--platform', 'windows', '--arch', 'amd64')
    $reparse = @(Get-ChildItem -LiteralPath $runtimeRoot -Recurse -Force -ErrorAction SilentlyContinue | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint })
    if ($reparse.Count -gt 0) {
        throw "Desktop runtime contains non-portable reparse points; first path: $($reparse[0].FullName)"
    }

    Write-Host '==> Packaging Electron Windows x64 application'
    Invoke-Native 'node.exe' @(
        (Join-Path $repoRoot 'desktop\scripts\generate-windows-icon.mjs'),
        (Join-Path $electronRoot 'assets\LazyMind.icns'),
        $env:LAZYMIND_DESKTOP_WINDOWS_ICON
    )
    Invoke-Native 'pnpm.cmd' @('install', '--frozen-lockfile', '--prefer-offline') $electronRoot
    Remove-GeneratedPath (Join-Path $distRoot 'win-unpacked')
    Remove-GeneratedPath (Join-Path $distRoot 'LazyMind-win-x64.zip')
    Remove-GeneratedPath (Join-Path $distRoot 'LazyMind-windows-x64.zip')
    Invoke-Native 'pnpm.cmd' @('run', 'pack:win:x64') $electronRoot
    $builderZip = Join-Path $distRoot 'LazyMind-win-x64.zip'
    $finalZip = Join-Path $distRoot $finalZipName
    if (-not (Test-Path -LiteralPath (Join-Path $distRoot 'win-unpacked\LazyMind.exe') -PathType Leaf)) {
        throw 'Electron Builder did not produce win-unpacked/LazyMind.exe'
    }
    if (-not (Test-Path -LiteralPath $builderZip -PathType Leaf)) {
        throw "Electron Builder did not produce $builderZip"
    }
    Move-Item -LiteralPath $builderZip -Destination $finalZip -Force
    Write-Host "Unpacked app: $(Join-Path $distRoot 'win-unpacked')"
    Write-Host "Portable ZIP: $finalZip"
}

function Build-Desktop {
    Invoke-Doctor
    Remove-GeneratedPath $targetRoot
    New-Item -ItemType Directory -Force -Path (Join-Path $runtimeRoot 'bin') | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $runtimeRoot 'runtimes\python') | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $runtimeRoot 'deps\python') | Out-Null

    Write-Host '==> Building Go desktop runtime binaries'
    $desktopManager = Join-Path $runtimeRoot 'bin\local-runtime-manager.exe'
    Build-GoBinary (Join-Path $repoRoot 'local\local-runtime-manager') $desktopManager -WindowsGUI
    Assert-WindowsGUISubsystem $desktopManager
    Build-GoBinary (Join-Path $repoRoot 'local\local-proxy') (Join-Path $runtimeRoot 'bin\local-proxy.exe') @('.\cmd\local-proxy')
    Build-GoBinary (Join-Path $repoRoot 'backend\core') (Join-Path $runtimeRoot 'bin\core.exe')
    Build-GoBinary (Join-Path $repoRoot 'backend\scan-control-plane') (Join-Path $runtimeRoot 'bin\scan-control-plane.exe') @('.\cmd\scan-control-plane')
    Build-GoBinary (Join-Path $repoRoot 'backend\file-watcher') (Join-Path $runtimeRoot 'bin\file-watcher.exe') @('.\cmd\main.go')
    Install-GoTool 'github.com/f1bonacc1/process-compose@v1.116.0'
    Install-GoTool 'github.com/caddyserver/caddy/v2/cmd/caddy@v2.10.2'

    Write-Host '==> Building frontend desktop dist'
    $env:VITE_LAZYMIND_MODE = 'desktop'
    Invoke-Native 'pnpm.cmd' @('install', '--frozen-lockfile', '--prefer-offline') (Join-Path $repoRoot 'frontend')
    Invoke-Native 'pnpm.cmd' @('build') (Join-Path $repoRoot 'frontend')

    Write-Host '==> Ensuring LazyLLM submodule source'
    if (-not (Test-Path -LiteralPath (Join-Path $repoRoot 'algorithm\lazyllm\lazyllm') -PathType Container)) {
        Invoke-Native 'git.exe' @('submodule', 'update', '--init', 'algorithm/lazyllm')
    }

    Write-Host '==> Preparing bundled Python runtime and venvs'
    Invoke-Native 'uv.exe' @('python', 'install', '--install-dir', (Join-Path $runtimeRoot 'runtimes\python'), $pythonVersion)
    $python = (& uv.exe python find --managed-python --no-python-downloads --resolve-links $pythonVersion).Trim()
    if ($LASTEXITCODE -ne 0) { throw "uv python find exited with code $LASTEXITCODE" }
    if (-not $python) { throw 'uv python find returned an empty path' }
    $authVenv = Join-Path $runtimeRoot 'deps\python\auth-service'
    $algorithmVenv = Join-Path $runtimeRoot 'deps\python\algorithm'
    Invoke-Native 'uv.exe' @('venv', '--managed-python', '--no-python-downloads', '--relocatable', '--seed', '--link-mode', 'copy', '--python', $python, $authVenv)
    $authPython = Join-Path $authVenv 'Scripts\python.exe'
    Invoke-Native 'uv.exe' @('pip', 'install', '--python', $authPython, '--link-mode', 'copy', '--strict', '-r', (Join-Path $repoRoot 'backend\auth-service\requirements.txt'))
    Invoke-Native 'uv.exe' @('venv', '--managed-python', '--no-python-downloads', '--relocatable', '--seed', '--link-mode', 'copy', '--python', $python, $algorithmVenv)
    $algorithmPython = Join-Path $algorithmVenv 'Scripts\python.exe'
    Invoke-Native 'uv.exe' @('pip', 'install', '--python', $algorithmPython, '--link-mode', 'copy', '--strict', 'setuptools<81', 'lazyllm')
    Invoke-Native (Join-Path $algorithmVenv 'Scripts\lazyllm.exe') @('install', 'rag')
    Invoke-Native 'uv.exe' @('pip', 'install', '--python', $algorithmPython, '--link-mode', 'copy', '--strict', '-r', (Join-Path $repoRoot 'algorithm\requirements.txt'))
    Invoke-Native 'uv.exe' @('pip', 'install', '--python', $algorithmPython, '--link-mode', 'copy', '--strict', '-r', (Join-Path $repoRoot 'algorithm\requirements-local.txt'))
    Finalize-Desktop
}

Initialize-Environment
switch ($Action) {
    'doctor' { Invoke-Doctor }
    'build' { Build-Desktop }
    'resume' { Invoke-Doctor; Finalize-Desktop }
    'clean' {
        Remove-GeneratedPath $targetRoot
        Remove-GeneratedPath (Join-Path $distRoot 'win-unpacked')
        Remove-GeneratedPath (Join-Path $distRoot 'LazyMind-win-x64.zip')
        Remove-GeneratedPath (Join-Path $distRoot 'LazyMind-windows-x64.zip')
        Remove-DistArtifacts 'LazyMind-windows-x64-????????-??????-*.zip'
    }
    'clean-all' {
        Remove-GeneratedPath (Join-Path $repoRoot 'desktop\build')
        Remove-GeneratedPath $distRoot
        Remove-GeneratedPath (Join-Path $electronRoot 'node_modules')
    }
}
