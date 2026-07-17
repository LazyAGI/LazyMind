import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const manifestScript = path.join(scriptsDir, "write-runtime-manifest.mjs");
const iconScript = path.join(scriptsDir, "generate-windows-icon.mjs");
const icnsSource = path.join(scriptsDir, "..", "electron", "assets", "LazyMind.icns");
const electronMainScript = path.join(scriptsDir, "..", "electron", "src", "main.js");
const electronBuilderConfig = path.join(scriptsDir, "..", "electron", "electron-builder.config.cjs");
const installerScript = path.join(scriptsDir, "..", "installer", "installer.nsh");

function nsisMacro(source, name) {
  const match = source.match(new RegExp(`!macro ${name}\\b([\\s\\S]*?)!macroend`));
  assert.ok(match, `missing NSIS macro ${name}`);
  return match[1];
}

for (const target of [
  { platform: "darwin", arch: "arm64", suffix: "" },
  { platform: "windows", arch: "amd64", suffix: ".exe" },
]) {
  test(`writes ${target.platform}/${target.arch} desktop runtime manifest`, () => {
    const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-manifest-"));
    try {
      const bin = path.join(root, "bin");
      mkdirSync(bin, { recursive: true });
      for (const name of ["process-compose", "local-proxy", "core", "scan-control-plane", "file-watcher", "caddy"]) {
        writeFileSync(path.join(bin, `${name}${target.suffix}`), name);
      }
      execFileSync(process.execPath, [
        manifestScript,
        root,
        "--platform", target.platform,
        "--arch", target.arch,
      ]);
      const manifest = JSON.parse(readFileSync(path.join(root, "manifest.json"), "utf8"));
      assert.equal(manifest.platform, target.platform);
      assert.equal(manifest.arch, target.arch);
      assert.equal(manifest.binaries.core, `bin/core${target.suffix}`);
      assert.ok(manifest.checksums[`bin/core${target.suffix}`]);
      assert.equal(Object.keys(manifest.checksums).some((key) => key.includes("\\")), false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
}

test("generates a multi-resolution Windows ICO from the macOS icon", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-icon-"));
  try {
    const output = path.join(root, "LazyMind.ico");
    execFileSync(process.execPath, [iconScript, icnsSource, output]);
    const ico = readFileSync(output);
    assert.equal(ico.readUInt16LE(0), 0);
    assert.equal(ico.readUInt16LE(2), 1);
    assert.equal(ico.readUInt16LE(4), 4);
    assert.deepEqual(
      [0, 1, 2, 3].map((index) => ico.readUInt8(6 + index * 16) || 256),
      [32, 64, 128, 256],
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("Windows installer force-stops LazyMind before invoking an old uninstaller", () => {
  const source = readFileSync(installerScript, "utf8");
  const check = nsisMacro(source, "customCheckAppRunning");

  assert.match(
    check,
    /InitPluginsDir[\s\S]*File \/oname=\$PLUGINSDIR\\lazymind-installer-maintenance\.exe[\s\S]*check-stopped --install-dir "\$INSTDIR"/,
    "the app-running hook must initialize its own helper before the silent-uninstall check",
  );
  assert.match(check, /\$0 == 10[\s\S]*force-stop --install-dir "\$INSTDIR"[\s\S]*Goto LMCheckStopped/);
  assert.doesNotMatch(check, /MB_RETRYCANCEL|LMCloseApp/);
  assert.match(source, /LangString LMProcessScanFailed[\s\S]*LangString LMForceStopFailed/);
});

test("Windows installer replaces legacy uninstallers with the fixed embedded uninstaller", () => {
  const source = readFileSync(installerScript, "utf8");
  const init = nsisMacro(source, "customInit");
  const check = nsisMacro(source, "customCheckAppRunning");

  assert.match(
    init,
    /File \/oname=\$PLUGINSDIR\\lazymind-upgrade-uninstaller\.exe "\$\{UNINSTALLER_OUT_FILE\}"/,
  );
  assert.match(init, /ReadRegStr \$LegacyUninstallString HKCU "\$\{UNINSTALL_REGISTRY_KEY\}" "UninstallString"/);
  assert.match(
    check,
    /LMProcessCheckDone:[\s\S]*!ifndef BUILD_UNINSTALLER[\s\S]*\$LegacyUninstallString != ""[\s\S]*\$InstalledVersion != ""[\s\S]*CopyFiles \/SILENT "\$UpgradeUninstaller" "\$INSTDIR\\\$\{UNINSTALL_FILENAME\}"/,
    "the compatibility replacement must run only in the installer after process cleanup",
  );
  assert.match(
    check,
    /WriteRegStr HKCU "\$\{UNINSTALL_REGISTRY_KEY\}" "UninstallString" '\"\$INSTDIR\\\$\{UNINSTALL_FILENAME\}\"'/,
    "stale uninstall registrations must be redirected to the repaired uninstaller",
  );
  assert.match(check, /ReadRegStr \$0 HKCU "\$\{UNINSTALL_REGISTRY_KEY\}" "UninstallString"[\s\S]*LMUpgradeRepairFailed/);
  assert.match(check, /LMUpgradeRepairFailed[\s\S]*SetErrorLevel 8/);
});

test("Windows installer verifies and force-cleans processes left by warmup", () => {
  const source = readFileSync(installerScript, "utf8");
  const install = nsisMacro(source, "customInstall");

  assert.match(
    install,
    /ExecWait[^\n]+--installer-warmup[^\n]+\$3[\s\S]*LMWarmupCheckStopped:[\s\S]*check-stopped --install-dir "\$INSTDIR"/,
  );
  assert.match(
    install,
    /\$0 == 10[\s\S]*force-stop --install-dir "\$INSTDIR"[\s\S]*Goto LMWarmupCheckStopped/,
  );
  assert.match(install, /\$4 == 1[\s\S]*StrCpy \$3 4[\s\S]*\$3 != 0/);
});

test("Desktop does not create the Chat window after shutdown begins", () => {
  const source = readFileSync(electronMainScript, "utf8");
  const start = source.indexOf("async function createWindow()");
  const end = source.indexOf('ipcMain.on("lazymind:renderer-ready"', start);
  assert.ok(start >= 0 && end > start, "could not locate createWindow");
  const createWindow = source.slice(start, end);

  assert.match(
    createWindow,
    /const status = await waitForRuntimeReady\(\);\s*if \(isQuitting\) \{\s*return;\s*\}\s*mainWindow = new BrowserWindow/,
    "shutdown must be rechecked before creating the hidden Chat window",
  );
});

test("Windows installer path policy matches the maintenance helper trust boundary", () => {
  const source = readFileSync(electronBuilderConfig, "utf8");
  assert.match(
    source,
    /allowToChangeInstallationDirectory:\s*false/,
    "custom install directories require an authenticated path policy in installer-maintenance",
  );
});
