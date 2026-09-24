import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

const read = (file) => readFileSync(new URL(file, import.meta.url), "utf8");
const sha = (data) => createHash("sha256").update(data).digest("hex");

test("credential helper pins the bundled CLI library", () => {
  const release = JSON.parse(read("../../backend/core/providerconnection/feishu-cli-release.json"));
  assert.match(read("../../backend/feishu-credential-helper/go.mod"), new RegExp(`github.com/larksuite/cli v${release.version.replaceAll(".", "\\.")}(?:\\s|$)`));
});

test("macOS build emits the helper with its actual checksum", { skip: process.platform === "win32" }, () => {
  const source = read("./build-darwin-arm64.sh");
  const lines = source.split("\n").filter((line) => line.includes("feishu-credential-helper"));
  assert.ok(lines.some((line) => line.includes("backend/feishu-credential-helper")), "desktop build omits helper module");
  assert.ok(lines.some((line) => line.includes("feishu-credential-helper.sha256")), "desktop build omits helper checksum");
  const root = mkdtempSync(path.join(tmpdir(), "feishu-helper-build-"));
  try {
    const runtime = path.join(root, "runtime"), go = path.join(root, "fixture-go");
    mkdirSync(path.join(root, "backend", "feishu-credential-helper"), { recursive: true });
    mkdirSync(path.join(runtime, "bin"), { recursive: true });
    writeFileSync(go, `#!/bin/sh
test "$PWD" = "$ROOT/backend/feishu-credential-helper" || exit 21
test "$1" = build || exit 22
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; printf 'fixture compiled helper' > "$1"; chmod 755 "$1"; exit 0; fi
  shift
done
exit 23
`, { mode: 0o755 });
    const run = spawnSync("bash", ["-c", `set -euo pipefail\nGO_BUILD_FLAGS=()\n${lines.join("\n")}`], {
      encoding: "utf8", env: { ...process.env, ROOT: root, RUNTIME_ROOT: runtime, GO_BIN: go },
    });
    assert.equal(run.status, 0, String(run.error || "") + run.stdout + run.stderr);
    const binary = readFileSync(path.join(runtime, "bin", "feishu-credential-helper"));
    assert.equal(binary.toString(), "fixture compiled helper");
    assert.equal(readFileSync(path.join(runtime, "bin", "feishu-credential-helper.sha256"), "utf8").trim(), sha(binary));
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test("Windows build emits the helper with its actual checksum", { skip: process.platform !== "win32" }, () => {
  const lines = read("./build-windows-x64.ps1").split("\n").filter((line) => line.includes("feishu-credential-helper"));
  assert.ok(lines.some((line) => line.includes("backend\\feishu-credential-helper")), "Windows build omits helper module");
  assert.ok(lines.some((line) => line.includes("feishu-credential-helper.sha256")), "Windows build omits checksum");
  const root = mkdtempSync(path.join(tmpdir(), "feishu-helper-build-"));
  try {
    const runtime = path.join(root, "runtime"), script = path.join(root, "fixture.ps1");
    mkdirSync(path.join(runtime, "bin"), { recursive: true });
    writeFileSync(script, `$ErrorActionPreference = 'Stop'
$repoRoot = $env:FIXTURE_ROOT
$runtimeRoot = Join-Path $repoRoot 'runtime'
function Build-GoBinary([string]$source, [string]$target) {
  if ($source -ne (Join-Path $repoRoot 'backend\\feishu-credential-helper')) { throw 'Unexpected module' }
  [System.IO.File]::WriteAllText($target, 'fixture compiled helper')
}
${lines.join("\n")}
`);
    const run = spawnSync("pwsh", ["-NoProfile", "-NonInteractive", "-File", script], { encoding: "utf8", env: { ...process.env, FIXTURE_ROOT: root } });
    assert.equal(run.status, 0, String(run.error || "") + run.stdout + run.stderr);
    const binary = readFileSync(path.join(runtime, "bin", "feishu-credential-helper.exe"));
    assert.equal(readFileSync(path.join(runtime, "bin", "feishu-credential-helper.sha256"), "utf8").trim(), sha(binary));
  } finally { rmSync(root, { recursive: true, force: true }); }
});

for (const [endingName, ending] of [["LF", "\n"], ["CRLF", "\r\n"]]) {
  test(`Sidecar image ships the credential helper and checksum configuration (${endingName})`, () => {
    const raw = read("../../backend/core/Dockerfile").replace(/\r?\n/g, ending);
    const docker = raw.replace(/\r\n/g, "\n");
    assert.match(docker, /COPY\s+backend\/feishu-credential-helper\//);
    const start = docker.indexOf(" AS feishu-cli-sidecar\n");
    assert.notEqual(start, -1, "Sidecar image stage is missing");
    const end = docker.indexOf("\nFROM ", start);
    const stage = docker.slice(start, end === -1 ? undefined : end);
    assert.match(stage, /COPY[^\n]*\/feishu-credential-helper\s+\/usr\/local\/bin\/feishu-credential-helper/);
    assert.match(stage, /LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_PATH=/);
    assert.match(stage, /LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_SHA256_FILE=/);
    assert.match(stage, /sha256sum[^\n]*feishu-credential-helper/);
  });
}
