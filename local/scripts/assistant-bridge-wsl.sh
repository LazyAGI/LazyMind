#!/bin/sh
# Keep Windows paths as data across both the POSIX and PowerShell parsers.
set -eu

case "${1-}" in
    start|stop) action=$1 ;;
    *) echo 'Usage: assistant-bridge-wsl.sh <start|stop>' >&2; exit 1 ;;
esac
[ "$#" -eq 1 ] || { echo 'Unexpected Assistant Bridge arguments.' >&2; exit 1; }
if ! command -v powershell.exe >/dev/null 2>&1 || ! command -v wslpath >/dev/null 2>&1; then
    echo 'WSL interoperability and wslpath are required to control the native Windows Assistant Bridge.' >&2
    exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repo_root=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)
# Runtime command substitution is expanded once. UNC backslashes, dollars and
# backticks returned by wslpath are never reinterpreted as shell source.
LAZYMIND_WSL_BRIDGE_SCRIPT=$(wslpath -w "$script_dir/assistant-bridge-win.ps1")
LAZYMIND_WSL_BRIDGE_SOURCE=$(wslpath -w "$repo_root/local/build/bin/lazymind.exe")
LAZYMIND_WSL_BRIDGE_ACTION=$action
if [ -z "$LAZYMIND_WSL_BRIDGE_SCRIPT" ] || [ -z "$LAZYMIND_WSL_BRIDGE_SOURCE" ]; then
    echo 'wslpath returned an empty Assistant Bridge path.' >&2
    exit 1
fi
export LAZYMIND_WSL_BRIDGE_SCRIPT LAZYMIND_WSL_BRIDGE_SOURCE LAZYMIND_WSL_BRIDGE_ACTION
# /w exports only towards Win32; no /p because paths are already converted.
WSLENV="${WSLENV:+$WSLENV:}LAZYMIND_WSL_BRIDGE_SCRIPT/w:LAZYMIND_WSL_BRIDGE_SOURCE/w:LAZYMIND_WSL_BRIDGE_ACTION/w"
export WSLENV

# -Command keeps script-load failures inside the Windows-side catch. Normalize
# native failures to 1 before WSL truncates Windows exit codes to eight bits.
if ! reply=$(powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command '
$ErrorActionPreference = "Stop"
$global:LASTEXITCODE = 0
try {
    if (-not (Test-Path -LiteralPath $env:LAZYMIND_WSL_BRIDGE_SCRIPT -PathType Leaf)) { throw "Windows Assistant Bridge launcher was not found." }
    & $env:LAZYMIND_WSL_BRIDGE_SCRIPT -Action $env:LAZYMIND_WSL_BRIDGE_ACTION -SourcePath $env:LAZYMIND_WSL_BRIDGE_SOURCE
    if ($LASTEXITCODE -ne 0) { exit 1 }
    exit 0
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}'); then
    echo 'Windows Assistant Bridge command failed.' >&2
    exit 1
fi
# A shell-visible zero is insufficient: a launcher failure such as 0xFFFD0000
# can appear as zero in WSL. Require the helper's postcondition acknowledgement.
reply=$(printf '%s' "$reply" | tr -d '\r')
if [ "$reply" != "LAZYMIND_ASSISTANT_BRIDGE_OK:$action" ]; then
    echo 'Windows Assistant Bridge did not acknowledge completion; startup/stop was not verified.' >&2
    exit 1
fi

if [ "$action" = start ]; then
    rm -f "$repo_root/local/build/bin/lazymind"
fi
