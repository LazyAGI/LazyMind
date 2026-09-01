import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const manifestScript = path.join(scriptsDir, "write-runtime-manifest.mjs");
const iconScript = path.join(scriptsDir, "generate-windows-icon.mjs");
const releaseVersionScript = path.join(scriptsDir, "resolve-release-version.mjs");
const icnsSource = path.join(scriptsDir, "..", "electron", "assets", "LazyMind.icns");
const electronMainScript = path.join(scriptsDir, "..", "electron", "src", "main.js");
const electronBuilderConfig = path.join(scriptsDir, "..", "electron", "electron-builder.config.cjs");
const electronPackage = path.join(scriptsDir, "..", "electron", "package.json");
const darwinBuildScript = path.join(scriptsDir, "build-darwin-arm64.sh");
const windowsBuildScript = path.join(scriptsDir, "build-windows-x64.ps1");
const installerScript = path.join(scriptsDir, "..", "installer", "installer.nsh");
const macosWorkflow = path.join(scriptsDir, "..", "..", ".github", "workflows", "macos-installer.yml");
const windowsWorkflow = path.join(
  scriptsDir,
  "..",
  "..",
  ".github",
  "workflows",
  "windows-installer.yml",
);
const desktopReleaseWorkflow = path.join(
  scriptsDir,
  "..",
  "..",
  ".github",
  "workflows",
  "desktop-release.yml",
);
const coreDockerfile = path.join(scriptsDir, "..", "..", "backend", "core", "Dockerfile");
const dockerCompose = path.join(scriptsDir, "..", "..", "docker-compose.yml");
const rootMakefile = path.join(scriptsDir, "..", "..", "Makefile");

function nsisMacro(source, name) {
  const match = source.match(new RegExp(`!macro ${name}\\b([\\s\\S]*?)!macroend`));
  assert.ok(match, `missing NSIS macro ${name}`);
  return match[1];
}

function writeOfflineSkillFixtures(root) {
  const packages = path.join(root, "builtin-skills", "packages");
  mkdirSync(packages, { recursive: true });
  writeFileSync(path.join(root, "builtin-skills", "catalog.json"), '{"schema_version":1,"skills":[]}\n');
  writeFileSync(path.join(packages, "fixture.zip"), "fixture");
  const featured = path.join(root, "featured-skills");
  mkdirSync(featured, { recursive: true });
  mkdirSync(path.join(featured, "assets"), { recursive: true });
  writeFileSync(path.join(featured, "catalog.json"), '{"schema_version":1,"cases":[]}\n');
  writeFileSync(path.join(root, "history-injection.zip"), "history-injection-fixture");
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
      writeOfflineSkillFixtures(root);
      execFileSync(process.execPath, [
        manifestScript,
        root,
        "--platform", target.platform,
        "--arch", target.arch,
      ]);
      const manifest = JSON.parse(readFileSync(path.join(root, "manifest.json"), "utf8"));
      assert.equal(manifest.platform, target.platform);
      assert.equal(manifest.arch, target.arch);
      assert.deepEqual(manifest.features, {
        trustedLocalMode: false,
        offlineBuiltinSkills: true,
        offlineFeaturedSkills: true,
      });
      assert.equal(manifest.binaries.core, `bin/core${target.suffix}`);
      assert.ok(manifest.checksums[`bin/core${target.suffix}`]);
      assert.ok(manifest.checksums["builtin-skills/catalog.json"]);
      assert.ok(manifest.checksums["featured-skills/catalog.json"]);
      assert.equal(manifest.paths.historyInjectionArchive, "history-injection.zip");
      assert.ok(manifest.checksums["history-injection.zip"]);
      assert.equal(Object.keys(manifest.checksums).some((key) => key.includes("\\")), false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
}

test("writes trusted local mode into the desktop runtime manifest", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-manifest-trusted-"));
  try {
    writeOfflineSkillFixtures(root);
    execFileSync(process.execPath, [
      manifestScript,
      root,
      "--platform", "windows",
      "--arch", "amd64",
      "--trusted-local-mode", "true",
    ]);
    const manifest = JSON.parse(readFileSync(path.join(root, "manifest.json"), "utf8"));
    assert.deepEqual(manifest.features, {
      trustedLocalMode: true,
      offlineBuiltinSkills: true,
      offlineFeaturedSkills: true,
    });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("macOS and Windows builds materialize offline assets before writing the runtime manifest", () => {
  const darwin = readFileSync(darwinBuildScript, "utf8");
  const windows = readFileSync(windowsBuildScript, "utf8");
  for (const source of [darwin, windows]) {
    const bundle = source.indexOf("builtin-skill-bundle");
    const historyPackage = source.indexOf("stage-history-injection-package.mjs");
    const manifest = source.indexOf("write-runtime-manifest.mjs");
    assert.ok(bundle >= 0, "build script must invoke the shared builtin Skill bundler");
    assert.ok(historyPackage >= 0, "build script must stage the ModelScope history package");
    assert.ok(manifest > bundle, "builtin Skills must be materialized before the runtime manifest is written");
    assert.ok(manifest > historyPackage, "history samples must be downloaded before the runtime manifest is written");
    assert.match(source, /builtin-sources\.yaml/);
    assert.match(source, /builtin-skills\.lock\.json/);
    assert.match(source, /featured-sources/);
    assert.match(source, /featured-output/);
  }
  assert.match(darwin, /--exclude "skills\/\.runtime"/);
  assert.match(darwin, /remove_generated_path "\$\{app_root\}\/skills\/\.runtime"/);
  for (const category of ["research", "review", "search"]) {
    assert.match(darwin, new RegExp(`--exclude "skills/${category}"`));
    assert.match(windows, new RegExp(`skills\\\\${category}`));
  }
  for (const directory of ["docs", "tests", ".github"]) {
    assert.match(darwin, new RegExp(`--exclude "/${directory.replace(".", "\\.")}"`));
    assert.match(windows, new RegExp(`Join-Path \\$repoRoot '${directory.replace(".", "\\.")}'`));
  }
  assert.match(darwin, /--exclude "skills\/featured"/);
  assert.match(windows, /skills\\featured/);
  for (const testDirectory of ["test", "tests", "testdata", "__snapshots__"]) {
    assert.match(darwin, new RegExp(`--exclude "${testDirectory}"`));
    assert.match(windows, new RegExp(`'${testDirectory}'`));
  }
  for (const testFile of ["*_test.go", "test_*.py", "*.test.mjs", "*.test.tsx"]) {
    const escaped = testFile.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    assert.match(darwin, new RegExp(`--exclude "${escaped}"`));
    assert.match(windows, new RegExp(`'${escaped}'`));
  }
  for (const developmentFile of [".coverage", "README.md", "README.CN.md"]) {
    assert.match(darwin, new RegExp(developmentFile.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
    assert.match(windows, new RegExp(developmentFile.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
  }
  assert.doesNotMatch(darwin, /--exclude "\/Makefile"/);
  assert.doesNotMatch(windows, /robocopy\.exe[^\r\n]*'Makefile'/);
  assert.match(darwin, /desktop runtime repo marker is required/);
  assert.match(windows, /Desktop runtime repo marker Makefile is missing/);
  assert.match(windows, /skills\\\.runtime/);
  assert.match(darwin, /"\$\{ROOT\}\/" "\$\{RUNTIME_ROOT\}\/app\/"/);
  assert.match(windows, /robocopy\.exe \$repoRoot \$appRoot \/MIR/);
  assert.match(darwin, /--exclude "\/history-injection"/);
  assert.match(darwin, /--exclude "lazymind-history-injection\*\.zip"/);
  assert.match(windows, /\$excludedDirs = @\(\s*'node_modules',/);
  assert.match(windows, /Join-Path \$repoRoot 'history-injection'/);
  assert.match(windows, /lazymind-history-injection\*\.zip/);
});

test("Docker prepares history samples at runtime without coupling the Core image build to ModelScope", () => {
  const dockerfile = readFileSync(coreDockerfile, "utf8");
  const compose = readFileSync(dockerCompose, "utf8");
  const completedInitDependency = /history-injection-init:\r?\n\s+condition: service_completed_successfully/;
  assert.doesNotMatch(dockerfile, /history-injection-package/);
  assert.doesNotMatch(dockerfile, /COPY history-injection \/app\/history-injection/);
  assert.match(compose, /history-injection-init:/);
  assert.match(compose, /lazymind-algorithm:/);
  assert.match(compose, /\.\/scripts\/prepare_history_injection\.py:.*:ro/);
  assert.match(compose, /\.\/desktop\/history-injection-package\.json:.*:ro/);
  assert.match(compose, /LAZYMIND_HISTORY_INJECTION_ROOT: \/var\/lib\/lazymind\/uploads\/\.history-injection\/bundles/);
  assert.match(compose, completedInitDependency);
  assert.match(compose.replace(/\r?\n/g, "\r\n"), completedInitDependency);
});

test("Docker startup builds a native Windows Assistant Bridge", () => {
  const source = readFileSync(rootMakefile, "utf8");

  assert.match(source, /ifeq \(\$\(OS\),Windows_NT\)[\s\S]*LAZYMIND_CLI_FILENAME := lazymind\.exe/);
  assert.match(source, /HOST_GOOS := windows/);
  assert.match(source, /PROCESSOR_ARCHITEW6432[\s\S]*PROCESSOR_ARCHITECTURE/);
  assert.match(source, /_HOST_DOCKER_PREFIX := MSYS_NO_PATHCONV=1/);
  assert.match(source, /\$\(_HOST_DOCKER_USER_FLAG\)[\s\S]*GOOS="\$\(HOST_GOOS\)"/);
  assert.match(source, /local\/build\/bin\/\$\(LAZYMIND_CLI_FILENAME\)/);
});

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

test("normalizes shared desktop release tags to standard SemVer", async () => {
  const { normalizeReleaseTag } = await import(pathToFileURL(releaseVersionScript).href);
  assert.deepEqual(normalizeReleaseTag("v1.2.3a2"), {
    releaseTag: "v1.2.3a2",
    packageVersion: "1.2.3-alpha.2",
    artifactVersion: "1.2.3-alpha.2",
  });
  assert.equal(normalizeReleaseTag("1.2.3b4").packageVersion, "1.2.3-beta.4");
  assert.equal(normalizeReleaseTag("v1.2.3rc1").packageVersion, "1.2.3-rc.1");
  assert.equal(normalizeReleaseTag("v1.2.3-alpha.2").packageVersion, "1.2.3-alpha.2");
  assert.equal(normalizeReleaseTag("v1.2.3").packageVersion, "1.2.3");
  assert.throws(() => normalizeReleaseTag("release-1.2"), /Unsupported release tag/);
});

test("Windows installer accepts development and release package versions", () => {
  const source = readFileSync(path.join(scriptsDir, "build-windows-x64.ps1"), "utf8");
  const packageJson = JSON.parse(readFileSync(electronPackage, "utf8"));
  const match = source.match(/\[string\]\$package\.version -notmatch '([^']+)'/);
  assert.ok(match, "missing Windows installer package version validation");

  const versionPattern = new RegExp(match[1]);
  for (const version of [
    packageJson.version,
    "1.2.3",
    "1.2.3-alpha.2",
    "1.2.3-beta.4",
    "1.2.3-rc.1",
    "1.2.3-preview",
    "1.2.3-preview.1+build.7",
  ]) {
    assert.match(version, versionPattern);
  }
  assert.doesNotMatch("v1.2.3", versionPattern);
  assert.doesNotMatch("1.2", versionPattern);
});

test("Windows installer force-stops LazyMind before invoking an old uninstaller", () => {
  const source = readFileSync(installerScript, "utf8");
  const check = nsisMacro(source, "customCheckAppRunning");
  const install = nsisMacro(source, "customInstall");
  const uninstall = nsisMacro(source, "customUnInstall");

  assert.match(
    check,
    /InitPluginsDir[\s\S]*File \/oname=\$PLUGINSDIR\\lazymind-installer-maintenance\.exe[\s\S]*check-stopped --install-dir "\$INSTDIR"/,
    "the app-running hook must initialize its own helper before the silent-uninstall check",
  );
  assert.match(check, /\$0 == 10[\s\S]*force-stop --install-dir "\$INSTDIR"[\s\S]*Goto LMCheckStopped/);
  assert.doesNotMatch(check, /MB_RETRYCANCEL|LMCloseApp/);
  assert.match(source, /LangString LMProcessScanFailed[\s\S]*LangString LMForceStopFailed/);
  assert.match(install, /purge-local-data --install-dir "\$INSTDIR"/);
  assert.match(uninstall, /purge-local-data --install-dir "\$INSTDIR"/);
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

test("Windows installer diagnoses paths and does not roll back when warmup fails", () => {
  const source = readFileSync(installerScript, "utf8");
  const init = nsisMacro(source, "customInit");
  const install = nsisMacro(source, "customInstall");

  assert.match(
    init,
    /preflight --install-dir "\$INSTDIR" --temp-dir "\$TEMP" --minimum-free-space-mb \$\{LAZYMIND_MIN_FREE_SPACE_MB\} --maximum-relative-path-length \$\{LAZYMIND_MAX_RELATIVE_PATH_LENGTH\}/,
  );
  assert.match(
    install,
    /\$InstallTypeChoice == "full"[\s\S]*ExecWait[^\n]+--installer-warmup --timeout-seconds 1800[^\n]+\$3[\s\S]*LMWarmupCheckStopped:[\s\S]*check-stopped --install-dir "\$INSTDIR"/,
  );
  assert.match(install, /Starting Electron installer warmup \(timeout=1800s\)/);
  assert.match(install, /installer-nsis\.log[\s\S]*Starting Electron installer warmup/);
  assert.match(install, /Electron installer warmup returned exit code \$3/);
  assert.match(
    install,
    /\$0 == 10[\s\S]*force-stop --install-dir "\$INSTDIR"[\s\S]*Goto LMWarmupCheckStopped/,
  );
  assert.match(install, /\$4 == 1[\s\S]*StrCpy \$3 4[\s\S]*\$3 != 0/);
  assert.doesNotMatch(install, /MB_ABORTRETRYIGNORE|SetErrorLevel 4/);
  assert.match(install, /installation will continue/);
});

test("Windows installer offers simple and full installation modes", () => {
  const source = readFileSync(installerScript, "utf8");
  const init = nsisMacro(source, "customInit");
  const pages = nsisMacro(source, "customPageAfterChangeDir");
  const install = nsisMacro(source, "customInstall");

  assert.match(source, /LangString LMSimpleInstall[^\n]+"Simple installation \(recommended\)"/);
  assert.match(source, /LangString LMSimpleInstall[^\n]+"简易安装（推荐）"/);
  assert.match(source, /LangString LMFullInstall[^\n]+"Full installation"/);
  assert.match(source, /LangString LMFullInstall[^\n]+"完整安装"/);
  assert.match(init, /StrCpy \$InstallTypeChoice "simple"/);
  assert.match(init, /--full-install[\s\S]*StrCpy \$InstallTypeChoice "full"/);
  assert.match(init, /--simple-install[\s\S]*StrCpy \$InstallTypeChoice "simple"/);
  assert.match(pages, /PageCallbacks LMInstallTypePageCreate LMInstallTypePageLeave/);
  assert.match(install, /\$InstallTypeChoice == "full"[\s\S]*--installer-warmup/);
  assert.match(install, /Simple installation selected; bundled Python will be prepared on first launch/);
});

test("Windows CI treats branches as non-tags without leaking git probe failures", () => {
  const source = readFileSync(windowsWorkflow, "utf8");

  assert.match(source, /REQUESTED_REF\.StartsWith\('refs\/tags\/'\)/);
  assert.match(source, /EXPLICIT_REF -and -not \$env:REQUESTED_REF\.StartsWith\('refs\/'\)/);
  assert.match(source, /is_tag=\$\(\$isTag\.ToString\(\)\.ToLowerInvariant\(\)\)/);
  assert.match(source, /exit 0/);
  assert.match(source, /git submodule update --init algorithm\/lazyllm/);
  assert.doesNotMatch(source, /git submodule update --init --recursive/);
  assert.match(source, /resolve-release-version\.mjs/);
  assert.match(source, /windows-2022[\s\S]*windows-2025/);
  assert.match(source, /workflow_call:/);
  assert.doesNotMatch(source, /push:\s*\n\s*tags:/);
  assert.match(source, /permissions:\s*\n\s*contents:\s*read/);
  assert.match(source, /artifact_name:\s*lazymind-windows-x64-installer/);
  assert.match(source, /"artifact_name=\$outputName"/);
  assert.match(
    source,
    /name:\s*\$\{\{ needs\.build-windows-installer\.outputs\.artifact_name \}\}/,
  );
  assert.match(
    source,
    /test-windows-installer:[\s\S]*name: Checkout smoke test scripts[\s\S]*ref: \$\{\{ inputs\.git_ref \|\| github\.ref \}\}[\s\S]*name: Download the exact installer built above/,
  );
  assert.match(source, /Start-Process -FilePath \$env:INSTALLER_PATH -ArgumentList "\/S --full-install" -Wait/);
  assert.match(source, /DisplayVersion -ne \$env:EXPECTED_VERSION/);
  assert.match(source, /expectedProductVersion = "\$\(\$Matches\[1\]\)\.\$\(\$Matches\[2\]\)\.\$\(\$Matches\[3\]\)\.0"/);
  assert.match(source, /Start-Process -FilePath \$uninstaller -ArgumentList "\/S" -Wait/);
  assert.match(source, /RegistryView\]::Registry64[\s\S]*RegistryView\]::Registry32/);
  assert.match(source, /name: Upload installer diagnostics[\s\S]*if: always\(\)/);
  assert.doesNotMatch(source, /name: Run explicit Electron warmup/);
  assert.doesNotMatch(source, /steps\.warmup\.outcome/);
  assert.match(source, /name: Verify installer warmup result[\s\S]*startup-metrics-latest\.json/);
});

test("Windows NSIS installer uses electron-builder's default LZMA payload", () => {
  const source = readFileSync(electronBuilderConfig, "utf8");
  const packageJson = JSON.parse(readFileSync(electronPackage, "utf8"));
  const buildScript = readFileSync(path.join(scriptsDir, "build-windows-x64.ps1"), "utf8");
  const workflow = readFileSync(windowsWorkflow, "utf8");
  assert.doesNotMatch(source, /useZip\s*:/);
  assert.doesNotMatch(source, /signAndEditExecutable\s*:/);
  assert.match(source, /uninstallDisplayName:\s*"LazyMind"/);
  assert.match(packageJson.scripts["pack:win:x64"], /--publish never$/);
  assert.match(packageJson.scripts["pack:win:x64:installer"], /--publish never$/);
  assert.match(buildScript, /function Invoke-WindowsPackagingWithRetry/);
  assert.match(buildScript, /\[0-9A-Za-z-\]\+/, "development SemVer identifiers such as 0.3.0-dev must be accepted");
  assert.match(buildScript, /function Invoke-NativeWithRetry/);
  assert.match(buildScript, /maximumAttempts = 3/);
  assert.match(buildScript, /ELECTRON_CACHE/);
  assert.match(buildScript, /ELECTRON_BUILDER_CACHE/);
  assert.match(buildScript, /LAZYMIND_TRUSTED_LOCAL_MODE/);
  assert.match(buildScript, /--trusted-local-mode', \$trustedLocalMode/);
  assert.match(buildScript, /function New-DeferredPythonRuntimeStage/);
  assert.match(buildScript, /python-runtime\.zip/);
  assert.match(buildScript, /Add-Type -AssemblyName System\.IO\.Compression\s/);
  assert.match(buildScript, /robocopy\.exe \$runtimeRoot \$packagedRuntimeRoot[^\n]+\| Out-Null/);
  assert.match(buildScript, /CompressionLevel\]::NoCompression/);
  assert.match(buildScript, /LAZYMIND_DESKTOP_RUNTIME_STAGE = New-DeferredPythonRuntimeStage/);
  assert.match(buildScript, /'resume-installer' \{ Invoke-Doctor; Finalize-Desktop 'installer' \}/);
  assert.match(workflow, /Cache Electron and electron-builder downloads/);
  assert.match(workflow, /ArgumentList "\/S --full-install"/);
  assert.match(workflow, /Submodule checkout attempt \$attempt\/3 failed/);
  assert.match(workflow, /pnpm activation attempt \$attempt\/3 failed/);
});

test("macOS distribution build signs packages while CI owns notarization sequencing", () => {
  const source = readFileSync(darwinBuildScript, "utf8");
  const builderSource = readFileSync(electronBuilderConfig, "utf8");
  const packageJson = JSON.parse(readFileSync(electronPackage, "utf8"));
  const workflow = readFileSync(macosWorkflow, "utf8");
  assert.match(workflow, /on:\s*\n\s*workflow_call:/);
  assert.match(workflow, /workflow_dispatch:/);
  assert.doesNotMatch(workflow, /push:\s*\n\s*tags:/);
  assert.match(workflow, /permissions:\s*\n\s*contents:\s*read/);
  assert.match(workflow, /name: Validate tag and set desktop version[\s\S]*resolve-release-version\.mjs/);
  assert.doesNotMatch(workflow, /pythonPrerelease|prereleaseNames/);
  assert.match(source, /PACKAGE_KIND=.*zip/);
  assert.match(source, /SIGNING_MODE=.*adhoc/);
  assert.match(source, /LAZYMIND_TRUSTED_LOCAL_MODE/);
  assert.match(source, /--trusted-local-mode "\$\{TRUSTED_LOCAL_MODE\}"/);
  assert.doesNotMatch(source, /notarytool submit/);
  assert.match(source, /Authority=Developer ID Application:/);
  assert.match(source, /signature_info="\$\(codesign -dv --verbose=4/);
  assert.doesNotMatch(source, /codesign -dv[^\n]*\|\s*grep -q/);
  assert.match(source, /verify_runtime_code_signatures "\$\{APP_PATH\}\/Contents\/Resources\/runtime"/);
  assert.match(packageJson.scripts["dist:mac:arm64"], /--publish never$/);
  assert.match(builderSource, /afterPack:\s*signAndStageEmbeddedRuntime/);
  assert.match(builderSource, /afterSign:\s*restoreRuntimeAndFinalizeSignature/);
  assert.match(
    builderSource,
    /context\.electronPlatformName !== "darwin" \|\| macSigningMode === "none"/,
  );
  assert.match(
    builderSource,
    /context\.electronPlatformName !== "darwin" \|\| macSigningMode !== "developer-id"/,
  );
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
  assert.match(
    source,
    /name: Validate release signing and notarization inputs\s*\n\s*if: steps\.release\.outputs\.is_tag == 'true'/,
  );
  assert.match(
    source,
    /LAZYMIND_DESKTOP_SIGNING_MODE: \$\{\{ steps\.release\.outputs\.is_tag == 'true' && 'developer-id' \|\| 'adhoc' \}\}/,
  );
  assert.match(
    source,
    /CSC_LINK: \$\{\{ steps\.release\.outputs\.is_tag == 'true' && secrets\.MAC_CSC_LINK \|\| '' \}\}/,
  );
  assert.match(source, /git submodule update --init algorithm\/lazyllm/);
  assert.doesNotMatch(source, /git submodule update --init --recursive/);
});

test("macOS CI notarizes ZIP then DMG and fails when notarization times out", () => {
  const buildWorkflow = readFileSync(macosWorkflow, "utf8");

  assert.match(buildWorkflow, /name:\s*Submit app ZIP for notarization/);
  assert.match(buildWorkflow, /notarytool submit "\$\{zip_path\}"/);
  assert.match(buildWorkflow, /--leave-running true/);
  assert.match(buildWorkflow, /name:\s*Start asynchronous packaged runtime cleanup[\s\S]*pkill -9 -f "\$\{app_executable\}"[\s\S]*nohup env/);
  assert.ok(
    buildWorkflow.indexOf("name: Submit app ZIP for notarization") <
      buildWorkflow.indexOf("name: Start asynchronous packaged runtime cleanup"),
  );
  assert.match(buildWorkflow, /name:\s*Wait up to 30 minutes for app ZIP notarization/);
  assert.match(buildWorkflow, /ZIP notarization is still in progress[\s\S]*exit 1/);
  assert.match(buildWorkflow, /name:\s*Require accepted app ZIP notarization/);
  assert.match(buildWorkflow, /name:\s*Staple accepted app ticket/);
  assert.match(buildWorkflow, /dist:mac:arm64:prepackaged/);
  assert.match(buildWorkflow, /name:\s*Submit DMG for notarization/);
  assert.match(buildWorkflow, /notarytool submit "\$\{NOTARIZATION_DMG\}"/);
  assert.match(buildWorkflow, /\.notarization\.dmg/);
  assert.match(buildWorkflow, /\.unnotarized\.dmg/);
  assert.doesNotMatch(buildWorkflow, /LazyMind-macos-arm64-pending/);
  assert.doesNotMatch(buildWorkflow, /LazyMind-macos-notarization-submission/);
  assert.match(buildWorkflow, /git show-ref --verify --quiet "refs\/tags\/\$\{REQUESTED_REF\}"/);
  assert.match(buildWorkflow, /tag_commit=.*git rev-parse "refs\/tags\/\$\{tag_candidate\}\^\{commit\}"/);
  assert.doesNotMatch(buildWorkflow, /path:[^\n]*LazyMind-darwin-arm64\.zip/);
  assert.match(buildWorkflow, /resolve-release-version\.mjs/);
  assert.match(buildWorkflow, /name:\s*Wait up to 30 minutes for DMG notarization/);
  assert.match(buildWorkflow, /deadline="\$\(\( started_at \+ 1800 \)\)"/);
  assert.match(buildWorkflow, /sleep 30/);
  assert.match(buildWorkflow, /DMG notarization is still in progress after 30 minutes[\s\S]*exit 1/);
  assert.match(buildWorkflow, /name:\s*Require accepted DMG notarization/);
  assert.match(buildWorkflow, /name:\s*Upload rejected app ZIP notarization diagnostics/);
  assert.match(buildWorkflow, /name:\s*Upload rejected DMG notarization diagnostics/);
  assert.match(buildWorkflow, /zip-notarization-log\.json/);
  assert.match(buildWorkflow, /dmg-notarization-log\.json/);
  assert.match(buildWorkflow, /stapler staple "\$\{final_path\}"/);
  assert.match(buildWorkflow, /stapler validate "\$\{final_path\}"/);
  assert.match(buildWorkflow, /test-macos-installer:/);
  assert.match(buildWorkflow, /name:\s*Install application from DMG/);
  assert.match(buildWorkflow, /name:\s*Run installed application smoke/);
  assert.doesNotMatch(buildWorkflow, /gh release (?:create|upload)/);
  assert.match(buildWorkflow, /name:\s*Verify packaged runtime cleanup after artifact upload/);
  assert.match(buildWorkflow, /LAZYMIND_PROCESS_COMPOSE_DOWN_TIMEOUT=1s/);
  assert.ok(
    buildWorkflow.indexOf("name: Upload final notarized installer") <
      buildWorkflow.indexOf("name: Verify packaged runtime cleanup after artifact upload"),
  );
  assert.doesNotMatch(buildWorkflow, /name:\s*Report step timings/);
});

test("desktop release builds each platform once and publishes only after both pass", () => {
  const source = readFileSync(desktopReleaseWorkflow, "utf8");

  assert.match(source, /push:\s*\n\s*tags:\s*\n\s*- "v\*"/);
  assert.match(source, /macos:[\s\S]*uses: \.\/\.github\/workflows\/macos-installer\.yml/);
  assert.match(source, /windows:[\s\S]*uses: \.\/\.github\/workflows\/windows-installer\.yml/);
  assert.match(source, /release:[\s\S]*needs:\s*\n\s*- macos\s*\n\s*- windows/);
  assert.match(source, /release:[\s\S]*permissions:[\s\S]*contents: write/);
  assert.match(source, /name: Download tested macOS installer/);
  assert.match(source, /name: Download tested Windows installer/);
  assert.match(source, /gh release create/);
  assert.match(source, /gh release upload/);
  assert.match(source, /gh release delete-asset/);
  assert.match(source, /gh release view .*--json assets/);
  assert.ok(
    source.indexOf("gh release upload") < source.indexOf("gh release delete-asset"),
    "new release assets must be uploaded before stale assets are deleted",
  );
  assert.match(source, /--prerelease/);
  assert.match(source, /printf '%s  %s\\n'.*basename/);
  assert.doesNotMatch(source, /sha256sum "\$\{dmg_files\[0\]\}" "\$\{exe_files\[0\]\}" \| tee/);
});

test("installer workflows launch the packaged application before publishing", () => {
  const macosSource = readFileSync(macosWorkflow, "utf8");
  const windowsSource = readFileSync(windowsWorkflow, "utf8");
  for (const source of [macosSource, windowsSource]) {
    assert.match(source, /packaged-app-smoke\.mjs/);
    assert.match(source, /--timeout-ms 300000/);
  }
  assert.match(macosSource, /LAZYMIND_DESKTOP_RUNTIME_ROOT=/);
  assert.ok(
    windowsSource.indexOf("packaged-app-smoke.mjs") < windowsSource.indexOf("$uninstall = Start-Process"),
  );
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

test("macOS first-launch warmup shows preparation UI instead of only a Dock icon", () => {
  const source = readFileSync(electronMainScript, "utf8");
  assert.match(
    source,
    /function createInstallationWarmupWindow\(\)[\s\S]*new BrowserWindow\(browserWindowOptions\(true\)\)[\s\S]*loadURL\(`data:text\/html/,
  );
  assert.match(
    source,
    /runMacInstallationWarmupIfNeeded\(\)[\s\S]*createInstallationWarmupWindow\(\)[\s\S]*runInstallerWarmup\(\)[\s\S]*disposeInstallationWarmupWindow/,
  );
});

test("Desktop startup shows real bundled Python progress and transient Windows lock retries", () => {
  const source = readFileSync(electronMainScript, "utf8");
  assert.match(
    source,
    /event\?\.event === "phase\.progress" && event\?\.phase === "python-payload"[\s\S]*completedFiles[\s\S]*totalBytes/,
  );
  assert.match(source, /role="progressbar"[\s\S]*aria-label="Startup progress"/);
  assert.match(source, /function renderProgress\(progress\)[\s\S]*Extracting bundled Python/);
  assert.match(source, /Waiting for Windows to release Python files/);
  assert.match(source, /Reading bundled Python archive/);
  assert.match(source, /percent \+ "% · " \+ \(progress\.totalRoots/);
  assert.match(source, /const ratio = totalFiles > 0 \? completedFiles \/ totalFiles : 0/);
  assert.match(source, /const complete = totalFiles > 0 && completedFiles >= totalFiles/);
  assert.match(source, /Math\.min\(99, Math\.floor\(ratio \* 100\)\)/);
  assert.match(source, /style\.transform = "scaleX\("/);
  assert.doesNotMatch(source, /transition:\s*width/);
});

test("Desktop allows slow first-launch Chat rendering", () => {
  const source = readFileSync(electronMainScript, "utf8");
  assert.match(source, /const rendererReadyTimeoutMs = 120 \* 1000;/);
  assert.match(source, /rendererReadyTimeoutMs \/ 1000/);
  assert.doesNotMatch(source, /did not render within 30 seconds/);
});

test("Desktop recreates a stuck hidden Chat renderer once after the full runtime is ready", () => {
  const source = readFileSync(electronMainScript, "utf8");
  const start = source.indexOf("async function createWindow()");
  const end = source.indexOf('ipcMain.on("lazymind:renderer-ready"', start);
  const createWindow = source.slice(start, end);

  assert.ok(start >= 0 && end > start, "could not locate createWindow");
  assert.match(createWindow, /const runtimeReadyPromise = waitForRuntimeReady\(\)/);
  assert.match(createWindow, /waitForRendererWithRuntimeRecovery\(/);
  assert.match(createWindow, /recreating hidden frontend window once/);
  assert.match(createWindow, /shouldRecover:[\s\S]*!isQuitting[\s\S]*!windowHiddenByUser/);
  assert.doesNotMatch(createWindow, /app\.relaunch|runSidecar\("down"\)/);
});

test("Desktop warmup reports bundled history extraction before Core starts", () => {
  const source = readFileSync(electronMainScript, "utf8");
  assert.match(source, /history-injection-payload/);
  assert.match(source, /Preparing sample conversations/);
  assert.match(source, /Verifying and unpacking the bundled sample conversations/);
});

test("selected Desktop folders become dynamic allowed roots without confirmation or restart", () => {
  const source = readFileSync(electronMainScript, "utf8");
  const start = source.indexOf('ipcMain.handle("lazymind:authorizeLocalFolders"');
  const end = source.indexOf('ipcMain.handle("lazymind:selectFolder"', start);
  const handler = source.slice(start, end);
  assert.ok(start >= 0 && end > start);
  assert.match(handler, /replaceFileWatcherAllowedRoots\([\s\S]*saveAccessState\([\s\S]*allowedRoots/);
  assert.doesNotMatch(handler, /showMessageBox/);
  assert.doesNotMatch(handler, /restartRuntimeAfterFolderAccessChange/);
});

test("Desktop discovery asks for consent before choosing roots and skips protected content folders", () => {
  const source = readFileSync(electronMainScript, "utf8");
  const start = source.indexOf('ipcMain.handle("lazymind:chooseLocalDiscoveryRoots"');
  const end = source.indexOf('ipcMain.handle("lazymind:discoverLocalFolders"', start);
  const handler = source.slice(start, end);
  assert.ok(start >= 0 && end > start);
  assert.ok(
    handler.indexOf("showMessageBox") < handler.indexOf("showOpenDialog"),
    "discovery consent must be shown before the native directory picker",
  );
  assert.match(handler, /discoveryConsentGranted:\s*false/);
  assert.match(
    handler,
    /localFolderDiscoveryExcludedRoots\(\)[\s\S]*resolveExistingDirectories\(\s*collapseRoots\([\s\S]*containsPath\(excludedRoot, candidate\)/,
    "protected selections must be filtered before filesystem validation",
  );

  const excludedStart = source.indexOf("function localFolderDiscoveryExcludedRoots()");
  const excludedEnd = source.indexOf("function runtimeAllowedRoots", excludedStart);
  const excluded = source.slice(excludedStart, excludedEnd);
  for (const name of ["desktop", "documents", "downloads", "music", "pictures", "videos"]) {
    assert.match(excluded, new RegExp(`"${name}"`));
  }
});

test("Desktop does not create the Chat window after quitting or moving to background", () => {
  const source = readFileSync(electronMainScript, "utf8");
  const start = source.indexOf("async function createWindow()");
  const end = source.indexOf('ipcMain.on("lazymind:renderer-ready"', start);
  assert.ok(start >= 0 && end > start, "could not locate createWindow");
  const createWindow = source.slice(start, end);

  assert.match(
    createWindow,
    /const status = await waitForDesktopHomeReady\(\);\s*if \(isQuitting \|\| windowHiddenByUser \|\| nextStartupWindow\.isDestroyed\(\)\) \{\s*return;\s*\}[\s\S]*startAttempt:[\s\S]*createHiddenRendererAttempt/,
    "quit and background state must be rechecked before creating the hidden Chat window",
  );
  assert.match(
    createWindow,
    /shouldRecover:\s*\(\) => !isQuitting && !windowHiddenByUser && !nextStartupWindow\.isDestroyed\(\)/,
    "quit and background state must prevent the one-time renderer recovery",
  );
});

test("Desktop renderer keeps Node disabled behind an isolated preload bridge", () => {
  const source = readFileSync(electronMainScript, "utf8");
  assert.match(source, /contextIsolation:\s*true/);
  assert.match(source, /nodeIntegration:\s*false/);
  assert.match(source, /preload:\s*path\.join\(__dirname, "preload\.js"\)/);
  assert.match(
    source,
    /backgroundThrottling:\s*false/,
    "hidden Chat windows must keep running until they report renderer readiness",
  );
});

test("Desktop clears stale frontend caches before opening a renderer", () => {
  const source = readFileSync(electronMainScript, "utf8");
  assert.match(source, /await clearFrontendCaches\(session\.defaultSession/);
  assert.ok(
    source.indexOf("await clearFrontendCaches(session.defaultSession") < source.indexOf("return runMacInstallationWarmupIfNeeded()"),
    "frontend caches must be cleared before installation warmup or the main window loads",
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

test("Desktop always starts its local runtime with automatic port allocation", () => {
  const source = readFileSync(electronMainScript, "utf8");
  assert.match(
    source,
    /LAZYMIND_LOCAL_PORTS_PINNED:\s*"false"/,
    "packaged Desktop must treat configured ports as preferences",
  );
  assert.match(
    source,
    /status\.config\?\.portResolutions[\s\S]*loggedPortResolutions\.has\(key\)[\s\S]*port moved:/,
    "resolved ports must be reported once in startup diagnostics",
  );
});

test("Desktop supervises the external Agent host until the application quits", () => {
  const source = readFileSync(electronMainScript, "utf8");
  assert.match(
    source,
    /function scheduleAgentHostRestart\(\)[\s\S]*isQuitting[\s\S]*setTimeout\([\s\S]*startAgentHost\(\)/,
    "an unexpected Agent host exit must schedule a bounded restart",
  );
  assert.match(
    source,
    /child\.once\("close"[\s\S]*scheduleAgentHostRestart\(\)/,
    "the Agent host close handler must enter supervision",
  );
  assert.match(
    source,
    /function beginFastQuit[\s\S]*clearTimeout\(agentHostRestartTimer\)[\s\S]*agentHostProcess\?\.kill\(\)/,
    "application shutdown must disable supervision before stopping the Agent host",
  );
  assert.match(
    source,
    /"agent", "host", "run", "--provider", "all", "--owner-pid", String\(process\.pid\)/,
    "the Agent host must self-terminate if Desktop exits without running cleanup",
  );
});

test("Desktop Agent login returns immediately and refreshes the Host when login exits", () => {
  const source = readFileSync(electronMainScript, "utf8");
  assert.match(source, /workbuddy: new Set\(\["connect", "status", "disconnect", "login"\]\)/);
  assert.match(
    source,
    /function runAgentConnector[\s\S]*action === "login"[\s\S]*return startAgentLogin\(agent\)/,
  );
  assert.match(
    source,
    /function startAgentLogin[\s\S]*spawn\(agentConnectorPath, \["internal", "agent", agent, "login"\][\s\S]*return runConnectorJSON\([\s\S]*\["internal", "agent", agent, "status"\]/,
  );
  assert.match(
    source,
    /function startAgentLogin[\s\S]*child\.once\("close"[\s\S]*restartAgentHost\(\)/,
  );
  assert.match(
    source,
    /function beginFastQuit[\s\S]*agentLoginProcesses\.values\(\)[\s\S]*child\.kill\(\)[\s\S]*agentLoginProcesses\.clear\(\)/,
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
