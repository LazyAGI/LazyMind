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
const electronPackage = path.join(scriptsDir, "..", "electron", "package.json");
const darwinBuildScript = path.join(scriptsDir, "build-darwin-arm64.sh");
const installerScript = path.join(scriptsDir, "..", "installer", "installer.nsh");
const macosWorkflow = path.join(scriptsDir, "..", "..", ".github", "workflows", "macos-installer.yml");
const macosFinalizeWorkflow = path.join(
  scriptsDir,
  "..",
  "..",
  ".github",
  "workflows",
  "macos-notarization-finalize.yml",
);

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

test("macOS distribution build signs the final DMG and submits one asynchronous notarization", () => {
  const source = readFileSync(darwinBuildScript, "utf8");
  const builderSource = readFileSync(electronBuilderConfig, "utf8");
  const packageJson = JSON.parse(readFileSync(electronPackage, "utf8"));
  assert.match(source, /PACKAGE_KIND=.*zip/);
  assert.match(source, /SIGNING_MODE=.*adhoc/);
  assert.match(
    source,
    /notarytool submit "\$\{DMG_PATH\}"[\s\S]*--team-id "\$\{APPLE_TEAM_ID\}"[\s\S]*--output-format json/,
  );
  assert.match(source, /Authority=Developer ID Application:/);
  assert.match(source, /signature_info="\$\(codesign -dv --verbose=4/);
  assert.doesNotMatch(source, /codesign -dv[^\n]*\|\s*grep -q/);
  assert.match(source, /verify_runtime_code_signatures "\$\{APP_PATH\}\/Contents\/Resources\/runtime"/);
  assert.match(packageJson.scripts["dist:mac:arm64"], /--publish never$/);
  assert.match(builderSource, /afterPack:\s*signAndStageEmbeddedRuntime/);
  assert.match(builderSource, /afterSign:\s*restoreRuntimeAndFinalizeSignature/);
  assert.match(builderSource, /macSigningMode === "developer-id" \? undefined : null/);
  assert.match(builderSource, /fs\.renameSync\(runtimeRoot, stagedRuntime\)/);
  assert.match(builderSource, /fs\.renameSync\(staged\.stagedRuntime, staged\.runtimeRoot\)/);
  assert.doesNotMatch(builderSource, /notarytool[\s\S]*submit/);
  assert.match(builderSource, /notarize:\s*false/);
  assert.match(builderSource, /sign:\s*macSigningMode === "developer-id"/);
  assert.doesNotMatch(builderSource, /signIgnore:/);
  assert.match(
    source,
    /SIGNING_MODE}" == "adhoc"[\s\S]*codesign --force --deep --sign - "\$\{APP_PATH\}"/,
    "local macOS builds must apply an explicit ad-hoc bundle signature",
  );
  assert.doesNotMatch(source, /notarytool submit[\s\S]*--wait/);
  assert.doesNotMatch(source, /stapler staple/);
  for (const privatePath of ["/.env", "/.lazymind-local", "/data", "/volumes", "/local/config.env"]) {
    assert.match(source, new RegExp(`--exclude "${privatePath.replace(/[.*+?^${}()|[\\]\\\\]/g, "\\\\$&")}"`));
  }
});

test("macOS CI fails fast on missing credentials and raises the open-file limit", () => {
  const source = readFileSync(macosWorkflow, "utf8");

  for (const secret of [
    "MAC_CSC_LINK",
    "MAC_CSC_KEY_PASSWORD",
    "APPLE_ID",
    "APPLE_APP_SPECIFIC_PASSWORD",
    "APPLE_TEAM_ID",
  ]) {
    assert.match(source, new RegExp(`secrets\\.${secret}`));
  }
  assert.match(source, /ulimit -n "\$\{target_open_files\}"/);
  assert.match(source, /actual_open_files < 8192/);
});

test("macOS CI normally finalizes notarization and preserves a manual fallback", () => {
  const buildWorkflow = readFileSync(macosWorkflow, "utf8");
  const finalizeWorkflow = readFileSync(macosFinalizeWorkflow, "utf8");

  assert.match(buildWorkflow, /artifact_name="LazyMind-macos-arm64-pending"/);
  assert.match(buildWorkflow, /name="LazyMind-macos-arm64-development"/);
  assert.match(buildWorkflow, /name:\s*LazyMind-macos-notarization-submission/);
  assert.match(buildWorkflow, /\.pending\.dmg/);
  assert.match(buildWorkflow, /\.unnotarized\.dmg/);
  assert.match(buildWorkflow, /git show-ref --verify --quiet "refs\/tags\/\$\{REQUESTED_REF\}"/);
  assert.match(buildWorkflow, /tag_commit=.*git rev-parse "refs\/tags\/\$\{tag_candidate\}\^\{commit\}"/);
  assert.match(
    buildWorkflow,
    /LAZYMIND_DESKTOP_NOTARIZE:\s*\$\{\{\s*steps\.release\.outputs\.is_tag\s*\}\}/,
  );
  assert.match(
    buildWorkflow,
    /if:\s*steps\.release\.outputs\.is_tag == 'true'[\s\S]*name:\s*LazyMind-macos-notarization-submission/,
  );
  assert.match(buildWorkflow, /replace\(\/\^v\//);
  assert.match(buildWorkflow, /prereleaseNames = \{ a: "alpha", b: "beta", rc: "rc" \}/);
  assert.match(buildWorkflow, /name:\s*Wait up to 30 minutes for Apple notarization/);
  assert.match(buildWorkflow, /deadline="\$\(\( started_at \+ 1800 \)\)"/);
  assert.match(buildWorkflow, /sleep 30/);
  assert.match(buildWorkflow, /::warning::Apple notarization is still in progress/);
  assert.match(buildWorkflow, /stapler staple "\$\{final_path\}"/);
  assert.match(buildWorkflow, /stapler validate "\$\{final_path\}"/);
  assert.match(buildWorkflow, /\| Wait for Apple \|/);
  assert.match(buildWorkflow, /name:\s*Report step timings/);
  assert.match(buildWorkflow, /actions\/runs\/\$\{process\.env\.RUN_ID\}\/jobs\?filter=latest/);

  assert.match(finalizeWorkflow, /source_run_id:/);
  assert.match(finalizeWorkflow, /run-id:\s*\$\{\{\s*inputs\.source_run_id\s*\}\}/);
  assert.match(finalizeWorkflow, /pattern:\s*"\*\.pending\.dmg"/);
  assert.match(finalizeWorkflow, /pattern:\s*"\*\.notarization\.json"/);
  assert.match(finalizeWorkflow, /merge-multiple:\s*true/);
  assert.match(finalizeWorkflow, /notarytool info "\$\{submission_id\}"/);
  assert.match(finalizeWorkflow, /notarytool log "\$\{SUBMISSION_ID\}"/);
  assert.match(finalizeWorkflow, /stapler staple "\$\{final_path\}"/);
  assert.match(finalizeWorkflow, /name:\s*LazyMind-macos-arm64-notarized/);
});

test("packaged macOS app runs installation warmup once before its normal window", () => {
  const source = readFileSync(electronMainScript, "utf8");
  assert.match(
    source,
    /runMacInstallationWarmupIfNeeded\(\)\.then\(\s*\(\) => \{\s*frontendOpeningAllowed = true;\s*if \(windowHiddenByUser\) \{\s*return undefined;\s*\}\s*return showActiveWindow\(\)/,
  );
  assert.match(
    source,
    /await runInstallerWarmup\(\);\s*markMacWarmupCompleted/,
    "warmup must only be marked complete after the shared lifecycle succeeds",
  );
});

test("Desktop does not create the Chat window after quitting or moving to background", () => {
  const source = readFileSync(electronMainScript, "utf8");
  const start = source.indexOf("async function createWindow()");
  const end = source.indexOf('ipcMain.on("lazymind:renderer-ready"', start);
  assert.ok(start >= 0 && end > start, "could not locate createWindow");
  const createWindow = source.slice(start, end);

  assert.match(
    createWindow,
    /const status = await waitForDesktopHomeReady\(\);\s*if \(isQuitting \|\| windowHiddenByUser \|\| nextStartupWindow\.isDestroyed\(\)\) \{\s*return;\s*\}\s*nextMainWindow = new BrowserWindow/,
    "quit and background state must be rechecked before creating the hidden Chat window",
  );
});

test("Desktop opens the home page from the sidecar readiness event with status polling as fallback", () => {
  const source = readFileSync(electronMainScript, "utf8");

  assert.match(
    source,
    /event\?\.event === "capability\.ready" && event\?\.capability === "home"[\s\S]*publishHomeReady\(Number\(event\.frontendPort\)\)/,
  );
  assert.match(
    source,
    /function waitForDesktopHomeReady\(\) \{[\s\S]*Promise\.race\(\[[\s\S]*waitForHomeReadySignal\(\),[\s\S]*waitForRuntimeReady\(\{ capability: "home" \}\)/,
  );
});

test("Desktop close and quit destroy renderers while keeping the runtime resident", () => {
  const source = readFileSync(electronMainScript, "utf8");
  const backgroundStart = source.indexOf("function enterBackgroundMode");
  const backgroundEnd = source.indexOf("function sameRuntimePath", backgroundStart);
  const backgroundMode = source.slice(backgroundStart, backgroundEnd);
  const windowsClosedStart = source.indexOf('app.on("window-all-closed"');
  const windowsClosedEnd = source.indexOf('app.on("before-quit"', windowsClosedStart);
  const windowsClosedHandler = source.slice(windowsClosedStart, windowsClosedEnd);

  assert.match(
    source,
    /function attachManagedClose\(window\)[\s\S]*event\.preventDefault\(\);\s*enterBackgroundMode\("window close", \{ discoverable: true \}\)/,
    "window close must preserve a visible background entry on macOS and Windows",
  );
  assert.match(
    backgroundMode,
    /rendererReadyWait\?\.cancel\(\);[\s\S]*window\.removeAllListeners\("close"\);[\s\S]*window\.destroy\(\)/,
    "both background modes must destroy renderer windows",
  );
  assert.match(backgroundMode, /if \(discoverable\) \{\s*ensureWindowsTray\(\)/);
  assert.match(backgroundMode, /app\.hide\(\);[\s\S]*app\.dock\.hide\(\);[\s\S]*destroyWindowsTray\(\)/);
  assert.doesNotMatch(backgroundMode, /beginFastQuit|detachRuntimeMonitor|runSidecar\("down"/);
  assert.match(
    source,
    /function showActiveWindow\(\)[\s\S]*app\.show\(\);[\s\S]*app\.dock\.show\(\)[\s\S]*const creation = createWindow\(\)/,
    "opening the resident app must restore the Dock icon and recreate its frontend",
  );
  assert.match(source, /app\.on\("second-instance"[\s\S]*showActiveWindow\(\)/);
  assert.match(source, /app\.on\("activate"[\s\S]*showActiveWindow\(\)/);
  assert.match(
    source,
    /app\.on\("before-quit",[\s\S]*event\.preventDefault\(\);\s*enterBackgroundMode\("app quit", \{ discoverable: false \}\)/,
    "Dock, menu, and keyboard quit actions must enter hidden background mode",
  );
  assert.doesNotMatch(windowsClosedHandler, /app\.quit\(\)/);
});

test("Windows tray reopens the frontend and Exit removes the visible background entry", () => {
  const source = readFileSync(electronMainScript, "utf8");
  const trayStart = source.indexOf("function ensureWindowsTray()");
  const trayEnd = source.indexOf("function attachManagedClose", trayStart);
  const traySource = source.slice(trayStart, trayEnd);

  assert.match(traySource, /tray = new Tray\(iconPath\)/);
  assert.match(traySource, /tray\.on\("click",[\s\S]*showActiveWindow\(\)/);
  assert.match(traySource, /label: "Open LazyMind"[\s\S]*showActiveWindow\(\)/);
  assert.match(
    traySource,
    /label: "Exit"[\s\S]*enterBackgroundMode\("tray exit", \{ discoverable: false \}\)/,
  );
  assert.match(source, /function destroyWindowsTray\(\)[\s\S]*tray\.destroy\(\);\s*tray = undefined/);
});

test("Windows installer path policy matches the maintenance helper trust boundary", () => {
  const source = readFileSync(electronBuilderConfig, "utf8");
  assert.match(
    source,
    /allowToChangeInstallationDirectory:\s*false/,
    "custom install directories require an authenticated path policy in installer-maintenance",
  );
});
