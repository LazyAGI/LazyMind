//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise the actual startup parser under Windows PowerShell's legacy encoding.
func TestWindowsEnvFilePreservesUTF8(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", "scripts", "local-win.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "probe.ps1")
	content := `param([string]$source)
$ErrorActionPreference = 'Stop'
$tokens=$null; $errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($source,[ref]$tokens,[ref]$errors)
if ($errors.Count) { throw 'Parse failed' }
$fn=$ast.FindAll({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst]},$true) | Where-Object Name -eq 'Import-EnvFile'
Invoke-Expression $fn.Extent.Text
$repoRoot='C:\probe'
$temp=Join-Path ([IO.Path]::GetTempPath()) ('lazymind-encoding-'+[guid]::NewGuid().ToString('N')+'.env')
try {
 $expected=[string][char]0x4e2d+[char]0x6587
 [IO.File]::WriteAllText($temp,((@("# $expected", "LAZYMIND_ENCODING_PROBE=$expected", "LAZYMIND_AFTER_COMMENT=ok")) -join [Environment]::NewLine),(New-Object Text.UTF8Encoding($false)))
 Import-EnvFile $temp
 [pscustomobject]@{powershell=$PSVersionTable.PSVersion.ToString(); defaultCodePage=[Text.Encoding]::Default.CodePage; unicodeValuePreserved=($env:LAZYMIND_ENCODING_PROBE -ceq $expected); afterCommentPreserved=($env:LAZYMIND_AFTER_COMMENT -ceq 'ok')} | ConvertTo-Json
 if ($env:LAZYMIND_ENCODING_PROBE -cne $expected -or $env:LAZYMIND_AFTER_COMMENT -cne 'ok') { exit 1 }
} finally { Remove-Item $temp -Force; Remove-Item Env:LAZYMIND_ENCODING_PROBE,Env:LAZYMIND_AFTER_COMMENT -ErrorAction SilentlyContinue }
`
	if err := os.WriteFile(script, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-source", source).CombinedOutput()
	if err != nil {
		t.Fatalf("startup env parser: %v\n%s", err, output)
	}
	t.Log(string(output))
}
