import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

const source = readFileSync(new URL("./build-darwin-arm64.sh", import.meta.url), "utf8");
const installer = source.slice(source.indexOf("install_feishu_cli() {"), source.indexOf("\nmake_internal_symlinks_relative()"));
const digest = (data) => createHash("sha256").update(data).digest("hex");

for (const badChecksum of [null, "archive", "license"]) {
  test(`macOS installer reads the manifest and enforces checksums (${badChecksum || "valid"})`, { skip: process.platform !== "darwin" }, () => {
    const root = mkdtempSync(path.join(tmpdir(), "feishu-release-"));
    try {
      const tools = path.join(root, "tools"), payload = path.join(root, "payload"), runtime = path.join(root, "runtime");
      for (const dir of [tools, payload, path.join(runtime, "bin")]) mkdirSync(dir, { recursive: true });
      const binary = "#!/bin/sh\nprintf 'fixture CLI\\n'\n";
      writeFileSync(path.join(payload, "lark-cli"), binary, { mode: 0o755 });
      const archive = path.join(root, "fixture.tar.gz"), license = path.join(root, "LICENSE");
      const tar = spawnSync("tar", ["-czf", archive, "-C", payload, "lark-cli"], { encoding: "utf8" });
      assert.equal(tar.status, 0, String(tar.error || "") + tar.stdout + tar.stderr);
      writeFileSync(license, "fixture license\n");
      const manifest = path.join(root, "release.json");
      writeFileSync(manifest, JSON.stringify({ version: "fixture-version", archive_sha256: { "darwin-arm64": badChecksum === "archive" ? "0".repeat(64) : digest(readFileSync(archive)) }, license_sha256: badChecksum === "license" ? "0".repeat(64) : digest(readFileSync(license)) }));
      writeFileSync(path.join(tools, "curl"), `#!/bin/sh
while [ "$#" -gt 0 ]; do
  case "$1" in --output) output="$2"; shift 2 ;; https:*) url="$1"; shift ;; *) shift ;; esac
done
case "$url" in */LICENSE) cp "$FIXTURE_LICENSE" "$output" ;; *.tar.gz) cp "$FIXTURE_ARCHIVE" "$output" ;; *) exit 1 ;; esac
`, { mode: 0o755 });
      const run = spawnSync("bash", ["-c", `set -euo pipefail\n${installer}\ninstall_feishu_cli`], {
        encoding: "utf8", env: { ...process.env, PATH: `${tools}:${process.env.PATH}`, BUILD_ROOT: root, RUNTIME_ROOT: runtime, FEISHU_CLI_RELEASE: manifest, FIXTURE_LICENSE: license, FIXTURE_ARCHIVE: archive },
      });
      if (badChecksum) {
        assert.notEqual(run.status, 0, run.stdout + run.stderr);
        assert.match(run.stdout + run.stderr, /checksum|FAILED/i);
        if (badChecksum === "archive") assert.equal(existsSync(path.join(runtime, "bin/lark-cli")), false);
      } else {
        assert.equal(run.status, 0, run.stdout + run.stderr);
        assert.match(run.stdout, /fixture-version/);
        assert.equal(readFileSync(path.join(runtime, "bin/lark-cli"), "utf8"), binary);
        assert.equal(readFileSync(path.join(runtime, "bin/lark-cli.sha256"), "utf8").trim(), digest(binary));
      }
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
}

const windowsSource = readFileSync(new URL("./build-windows-x64.ps1", import.meta.url), "utf8");
const windowsInstaller = windowsSource.slice(windowsSource.indexOf("function Install-FeishuCLI {"), windowsSource.indexOf("\nfunction Invoke-Doctor"));
const windowsPathHelpers = windowsSource.slice(windowsSource.indexOf("function Assert-PathUnderRepo"), windowsSource.indexOf("\nfunction Remove-DistArtifacts"));

for (const badChecksum of [null, "archive", "license"]) {
  test(`Windows installer reads the manifest and enforces checksums (${badChecksum || "valid"})`, { skip: process.platform !== "win32" }, () => {
    const root = mkdtempSync(path.join(tmpdir(), "feishu-release-"));
    try {
      const payload = path.join(root, "payload"), runtime = path.join(root, "runtime");
      const manifestDir = path.join(root, "backend", "core", "providerconnection");
      for (const dir of [payload, path.join(runtime, "bin"), manifestDir]) mkdirSync(dir, { recursive: true });
      const binary = "fixture Windows CLI\n";
      writeFileSync(path.join(payload, "lark-cli.exe"), binary);
      const archive = path.join(root, "fixture.zip"), license = path.join(root, "LICENSE");
      writeFileSync(license, "fixture license\n");
      const environment = { ...process.env, FIXTURE_ROOT: root, FIXTURE_ARCHIVE: archive, FIXTURE_LICENSE: license };
      const zip = spawnSync("pwsh", ["-NoProfile", "-NonInteractive", "-Command",
        '$ErrorActionPreference = "Stop"; Compress-Archive -LiteralPath (Join-Path $env:FIXTURE_ROOT "payload/lark-cli.exe") -DestinationPath $env:FIXTURE_ARCHIVE',
      ], { encoding: "utf8", env: environment });
      assert.equal(zip.status, 0, String(zip.error || "") + zip.stdout + zip.stderr);
      writeFileSync(path.join(manifestDir, "feishu-cli-release.json"), JSON.stringify({
        version: "fixture-version",
        archive_sha256: { "windows-amd64": badChecksum === "archive" ? "0".repeat(64) : digest(readFileSync(archive)) },
        license_sha256: badChecksum === "license" ? "0".repeat(64) : digest(readFileSync(license)),
      }));
      const script = path.join(root, "test-installer.ps1");
      writeFileSync(script, `
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0
$repoRoot = $env:FIXTURE_ROOT
$targetRoot = $repoRoot
$runtimeRoot = Join-Path $repoRoot 'runtime'
function Invoke-WebRequest {
  param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
  if ($Uri.EndsWith('/LICENSE')) { Copy-Item -LiteralPath $env:FIXTURE_LICENSE -Destination $OutFile }
  elseif ($Uri.EndsWith('.zip')) { Copy-Item -LiteralPath $env:FIXTURE_ARCHIVE -Destination $OutFile }
  else { throw 'Unexpected download URL' }
}
${windowsPathHelpers}
${windowsInstaller}
Install-FeishuCLI
`);
      const run = spawnSync("pwsh", ["-NoProfile", "-NonInteractive", "-File", script], { encoding: "utf8", env: environment });
      const output = String(run.error || "") + run.stdout + run.stderr;
      if (badChecksum) {
        assert.notEqual(run.status, 0, output);
        assert.match(output, new RegExp(`Feishu CLI ${badChecksum} integrity mismatch`));
        if (badChecksum === "archive") assert.equal(existsSync(path.join(runtime, "bin", "lark-cli.exe")), false);
      } else {
        assert.equal(run.status, 0, output);
        assert.match(run.stdout, /fixture-version/);
        assert.equal(readFileSync(path.join(runtime, "bin", "lark-cli.exe"), "utf8"), binary);
        assert.equal(readFileSync(path.join(runtime, "bin", "lark-cli.sha256"), "utf8").trim(), digest(binary));
        assert.equal(readFileSync(path.join(runtime, "licenses", "lark-cli", "LICENSE"), "utf8"), "fixture license\n");
      }
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
}
