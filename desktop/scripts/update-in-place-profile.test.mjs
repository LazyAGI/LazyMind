import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import vm from "node:vm";
import profileModule from "../electron/src/unsigned-test-profile.js";
import installerModule from "../electron/src/unsigned-test-installer.js";

const { initializeTestProfile } = profileModule;
const { createUnsignedTestInstaller } = installerModule;

function effectiveSidecarEnv(f) {
  const main = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  const begin = main.indexOf("function sidecarEnv()");
  const end = main.indexOf("function sidecarShutdownEnv()", begin);
  assert.ok(begin >= 0 && end > begin);
  return vm.runInNewContext(main.slice(begin, end) + "\nsidecarEnv()", {
    process, app: { getVersion: () => "0.5.230" }, explicitRuntimeRoot: process.env.LAZYMIND_DESKTOP_RUNTIME_ROOT || "",
    runtimeResourcesRoot: path.join(f.resources, "runtime"), loadAccessState: () => ({ allowedRoots: [] }),
    loadObsidianConfig: () => ({}), buildObsidianRuntimeEnv: () => ({}), deriveDesktopCredentialKey: () => "fixture",
    desktopCredentialIdentity: {}, editablePptDependencyConfig: { windowsX64: {}, darwinArm64: {} },
    localFolderAccessStatePath: "", obsidianConfigPath: "", obsidianDisabledRoot: "",
    ownerToken: "fixture", internalServiceToken: "fixture", clientInstanceId: "fixture", cloudBaseURL: "",
  });
}

function fixture(t) {
  const home = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-in-place-")));
  t.after(() => fs.rmSync(home, { recursive: true, force: true }));
  const originalEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) => key.startsWith("LAZYMIND_")));
  const clearEnv = () => {
    for (const key of Object.keys(process.env)) if (key.startsWith("LAZYMIND_")) delete process.env[key];
  };
  clearEnv();
  t.after(() => { clearEnv(); Object.assign(process.env, originalEnv); });

  const support = path.join(home, "Library", "Application Support");
  const profile = path.join(support, "lazymind-desktop");
  const runtime = path.join(support, "LazyMind");
  const application = path.join(home, "Applications", "LazyMind.app");
  const resources = path.join(application, "Contents", "Resources");
  const defaults = { home, appData: support, userData: profile, sessionData: profile, logs: path.join(home, "Library", "Logs", "lazymind-desktop") };
  let appName = "lazymind-desktop";
  const app = {
    getPath: key => defaults[key], setPath: (key, value) => { defaults[key] = value; },
    setAppLogsPath: value => { defaults.logs = value; }, getName: () => appName, setName: value => { appName = value; },
  };
  function put(file, value) {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, value);
  }
  const executable = path.join(application, "Contents", "MacOS", "LazyMindApp");
  put(executable, "fixture");
  const open = (exe = executable, systemApplications) => initializeTestProfile(app, {
    account: { homedir: home, uid: 501, username: "tester" }, executable: exe, platform: "darwin",
    ...(systemApplications ? { systemApplications } : {}),
  });
  return { home, profile, runtime, application, resources, defaults, put, open };
}

for (const customRuntime of [false, true]) {
  test(`first in-place launch reuses the 0.5 profile and ${customRuntime ? "configured" : "default"} runtime`,
    { skip: process.platform === "win32" }, t => {
      const f = fixture(t);
      const runtime = customRuntime ? path.join(f.home, "existing custom runtime") : f.runtime;
      const cliHome = path.join(f.home, customRuntime ? "existing CLI home" : ".lazymind");
      if (customRuntime) {
        process.env.LAZYMIND_DESKTOP_RUNTIME_ROOT = runtime;
        process.env.LAZYMIND_HOME = cliHome;
      }
      const credential = JSON.stringify({ version: 1, deviceId: "test-device", deviceSecret: Buffer.alloc(32, 17).toString("base64url") });
      const oldState = '{"profile":"desktop","overallStatus":"stopped"}';
      f.put(path.join(f.profile, "credential-device.json"), credential);
      f.put(path.join(runtime, "state", "runtime-state.json"), oldState);
      const defaultSQLite = path.join(runtime, "data", "stores", "sqlite");
      function seedDataset(root, label) {
        const db = path.join(root, "core", "core.db");
        fs.mkdirSync(path.dirname(db), { recursive: true });
        execFileSync("python3", ["-c", [
          "import sqlite3, sys",
          "with sqlite3.connect(sys.argv[1]) as db:",
          "    db.execute('CREATE TABLE datasets (id VARCHAR(255) PRIMARY KEY, display_name TEXT)')",
          "    db.execute('INSERT INTO datasets VALUES (?, ?)', ('kb', sys.argv[2]))",
        ].join("\n"), db, label]);
      }
      if (customRuntime) {
        process.env.LAZYMIND_LOCAL_SQLITE_DIR = path.join(f.home, "old sqlite volume");
        seedDataset(defaultSQLite, "wrong default database");
      }
      seedDataset(process.env.LAZYMIND_LOCAL_SQLITE_DIR || defaultSQLite, "preserved");

      const previous = effectiveSidecarEnv(f);
      assert.equal(previous.LAZYMIND_RUNTIME_ROOT || f.runtime, runtime);
      assert.equal(previous.LAZYMIND_LOCAL_SQLITE_DIR || defaultSQLite, customRuntime ? path.join(f.home, "old sqlite volume") : defaultSQLite);
      const selected = f.open();
      assert.equal(selected.application, f.application);
      assert.equal(f.defaults.userData, f.profile);
      assert.equal(f.defaults.sessionData, f.profile);
      const env = effectiveSidecarEnv(f);
      const sidecarRuntime = env.LAZYMIND_RUNTIME_ROOT || f.runtime;
      assert.equal(sidecarRuntime, runtime);
      assert.equal(fs.readFileSync(path.join(sidecarRuntime, "state", "runtime-state.json"), "utf8"), oldState);
      assert.equal(fs.readFileSync(path.join(f.defaults.userData, "credential-device.json"), "utf8"), credential);
      assert.equal(process.env.LAZYMIND_HOME || path.join(f.home, ".lazymind"), cliHome);
      const selectedSQLite = env.LAZYMIND_LOCAL_SQLITE_DIR || path.join(sidecarRuntime, "data", "stores", "sqlite");
      const row = execFileSync("python3", ["-c", [
        "import pathlib, sqlite3, sys",
        "uri = pathlib.Path(sys.argv[1]).as_uri() + '?mode=ro'",
        "with sqlite3.connect(uri, uri=True) as db:",
        "    print(db.execute(\"SELECT display_name FROM datasets WHERE id='kb'\").fetchone()[0])",
      ].join("\n"), path.join(selectedSQLite, "core", "core.db")], { encoding: "utf8" });
      assert.equal(row.trim(), "preserved");
    });
}

function seedTestRelease(f) {
  const config = { mode: "unsigned-test", audience: "fork-test", channel: "personal-test", signingIdentity: "adhoc",
    platform: "darwin", arch: "arm64", updaterProtocol: 2, version: "0.5.230", releaseId: "darwin-arm64-0.5.230",
    trustDomain: "inplace-101-" + "a".repeat(64),
    policyURL: "https://modelscope.cn/datasets/test/desktop/resolve/master/updates/unsigned-test/in-place/round-101/policy.json",
    allowedOrigins: ["https://modelscope.cn"], publicKeys: { fixture: Buffer.alloc(32, 1).toString("base64") } };
  f.put(path.join(f.resources, "runtime", "bin", "unsigned-update-helper"), "fixture");
  f.put(path.join(f.resources, "app-update.yml"), JSON.stringify({
    lazymind: { ...config, protocol: 2 }, updaterCacheDirName: `LazyMind-${config.trustDomain}-${config.audience}-${config.channel}`,
  }));
  return config;
}

test("in-place update capability binds the active application and the embedded test channel",
  { skip: process.platform === "win32" }, t => {
    const f = fixture(t);
    const config = seedTestRelease(f);
    const installer = createUnsignedTestInstaller({ profile: { application: f.application }, currentVersion: config.version, resourcesPath: f.resources });
    assert.equal(installer.capability(config).supported, true);
    assert.equal(installer.capability({ ...config, mode: "official", signingIdentity: "Developer ID" }).supported, false);
    assert.equal(installer.capability({ ...config, trustDomain: "round-101-other" }).supported, false);
    const otherResources = path.join(f.home, "Other", "LazyMind.app", "Contents", "Resources");
    fs.cpSync(f.resources, otherResources, { recursive: true });
    const otherInstaller = createUnsignedTestInstaller({
      profile: { application: f.application }, currentVersion: config.version, resourcesPath: otherResources,
    });
    assert.equal(otherInstaller.capability(config).supported, false);
  });

test("the system installation updates itself even when the user Applications folder contains another copy",
  { skip: process.platform === "win32" }, t => {
    const f = fixture(t);
    const config = seedTestRelease(f);
    // Substitute a temporary directory for the OS Applications directory. The
    // shipped caller supplies no override; both installed copies exist here.
    const system = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-system-apps-")));
    t.after(() => fs.rmSync(system, { recursive: true, force: true }));
    const systemApp = path.join(system, "LazyMind.app");
    fs.cpSync(f.application, systemApp, { recursive: true });
    const current = f.open(path.join(systemApp, "Contents", "MacOS", "LazyMindApp"), system);
    assert.equal(current.application, systemApp);
    for (const [resourcesPath, supported] of [[path.join(systemApp, "Contents", "Resources"), true], [f.resources, false]]) {
      const candidate = createUnsignedTestInstaller({ profile: current, currentVersion: config.version, resourcesPath });
      assert.equal(candidate.capability(config).supported, supported);
    }
  });

test("old isolated or renamed applications cannot become the in-place installer",
  { skip: process.platform === "win32" }, t => {
    const f = fixture(t);
    for (const name of ["LazyMind Test.app", "LazyMind Copy.app"]) {
      const executable = path.join(f.home, "Applications", name, "Contents", "MacOS", "LazyMindApp");
      f.put(executable, "fixture");
      assert.throws(() => f.open(executable));
    }
  });
