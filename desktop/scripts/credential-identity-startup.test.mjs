import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
const startMarker = "const editablePptDependencyConfig = loadEditablePptDependencyConfig();";
const start = source.indexOf(startMarker);
const end = source.indexOf("function sidecarShutdownEnv()", start);
assert.ok(start > 0 && end > start, "Desktop identity startup and service environment must be present");
const startup = source.slice(start + startMarker.length, end);

const goIdentity = {
  version: 1,
  deviceId: "11111111-2222-4333-8444-555555555555",
  deviceSecret: "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8",
};
const goIdentityBytes = `${JSON.stringify(goIdentity, null, 2)}\n`;
// Fixed HMAC-SHA256 vectors for the fixture's 32 bytes 0 through 31.
const goModelKey = "l_JSbmfusw38fVGvRPOcm_rKOkAV4OvMSNjNgcvPK38";
const goMCPKey = "lwLoUYnc88tzFbuT_ZmGq0evtbocf5ZFeBe1fm0Z4WQ";
const installedWindows = { platform: "win32", installed: true };
const legacyWindows = { platform: "win32", installed: false };
const legacyDatabaseEnv = {
  LAZYMIND_AUTH_SERVICE_DATABASE_URL: "sqlite:///C:/Users/fixture/AppData/Local/LazyMind/alternate-stores/custom-auth.db",
  LAZYMIND_LOCAL_SQLITE_DIR: "C:\\Users\\fixture\\AppData\\Local\\LazyMind\\alternate-stores",
};

function argumentValues(args, name) {
  const flag = new RegExp(`^--?${name}(?:=(.*))?$`);
  return args.flatMap((argument, index) => {
    const match = flag.exec(argument);
    return match ? [match[1] ?? args[index + 1]] : [];
  });
}

function fixture({ platform, installed, externalDev = false, initialBytes, runGo = () => {}, env = {} }) {
  const isWindows = platform === "win32";
  const paths = isWindows ? path.win32 : path.posix;
  const runtimeRoot = isWindows
    ? (installed ? "D:\\LazyMind Test\\Data" : "C:\\Users\\fixture\\AppData\\Local\\LazyMind")
    : "/synthetic/Library/Application Support/LazyMind";
  const identityDir = paths.join(runtimeRoot, "Desktop");
  const identityPath = paths.join(identityDir, "credential-device.json");
  const runtimeResourcesRoot = isWindows ? "D:\\LazyMind Test\\App\\resources\\runtime" : "/synthetic/runtime";
  const repoRoot = paths.join(runtimeResourcesRoot, "app");
  const sidecarPath = paths.join(runtimeResourcesRoot, "bin", isWindows ? "local-runtime-manager.exe" : "local-runtime-manager");
  const files = new Map(initialBytes === undefined ? [] : [[identityPath, initialBytes]]);
  const goCalls = [];
  const fileChanges = [];
  const context = vm.createContext({
    isWindows, hasInstalledStorage: installed, isExternalRuntimeDev: externalDev,
    explicitRuntimeRoot: installed ? runtimeRoot : "",
    desktopCredentialIdentityPath: identityPath,
    windowsDesktopPaths: { runtimeRoot, profileDir: identityDir },
    repoRoot, runtimeResourcesRoot, sidecarPath, path: paths, Buffer, createHmac,
    process: { platform, env, pid: 1, execPath: "synthetic-desktop" },
    app: { isPackaged: !externalDev, getVersion: () => "1.0-fixture" },
    editablePptDependencyConfig: { windowsX64: {}, darwinArm64: {} },
    ownerToken: "fixture-owner", internalServiceToken: "fixture-internal", clientInstanceId: "fixture-client",
    cloudBaseURL: "", agentHostFrontendPort: 8091, desktopFrontendOrigin: () => "http://localhost:8091",
    localFolderAccessStatePath: "", obsidianConfigPath: "", obsidianDisabledRoot: "",
    loadAccessState: () => ({ allowedRoots: [] }), loadObsidianConfig: () => ({ root: "" }),
    buildObsidianRuntimeEnv: () => ({}),
    randomUUID: () => "synthetic-js-device",
    randomBytes: count => Buffer.alloc(count, 42),
    fs: {
      existsSync: file => files.has(file),
      readFileSync: file => {
        if (!files.has(file)) throw Object.assign(new Error("fixture file missing"), { code: "ENOENT" });
        return files.get(file);
      },
      mkdirSync: dir => { fileChanges.push({ operation: "mkdir", path: dir }); },
      writeFileSync: (file, bytes, options) => {
        fileChanges.push({ operation: "write", path: file });
        if (options?.flag === "wx" && files.has(file)) throw Object.assign(new Error("fixture file already exists"), { code: "EEXIST" });
        files.set(file, String(bytes));
      },
    },
    execFileSync: (executable, args, options) => {
      const call = { executable, args: Array.from(args), env: { ...(options?.env ?? env) }, precedingChanges: fileChanges.length };
      goCalls.push(call);
      runGo(call, bytes => files.set(identityPath, bytes));
    },
  });
  return {
    runtimeRoot, identityDir, identityPath, repoRoot, runtimeResourcesRoot, sidecarPath, goCalls, fileChanges,
    get bytes() { return files.get(identityPath); },
    start() {
      vm.runInContext(startup, context, { filename: "desktop/electron/src/main.js" });
      return vm.runInContext("sidecarEnv()", context);
    },
  };
}

test("managed Windows launches Go to check and publish the first identity", async t => {
  for (const [name, mode] of [["registered Windows data root", installedWindows], ["default legacy Windows layout", legacyWindows]]) {
    await t.test(name, () => {
      const databaseEnv = mode.installed ? {} : legacyDatabaseEnv;
      const h = fixture({
        ...mode, env: { ...databaseEnv },
        runGo: ({ args }, publish) => {
          if (args[1] === "ensure-credential-identity") publish(goIdentityBytes);
        },
      });
      const env = h.start();
      assert.equal(h.goCalls.length, 1);
      const [call] = h.goCalls;
      assert.equal(call.executable, h.sidecarPath);
      assert.deepEqual(call.args.slice(0, 2), ["internal", "ensure-credential-identity"]);
      assert.equal(call.precedingChanges, 0, "Go must run before any JavaScript file mutation");
      for (const [flag, value] of [["profile", "desktop"], ["repo-root", h.repoRoot], ["resources-root", h.runtimeResourcesRoot]]) {
        assert.deepEqual(argumentValues(call.args, flag), [value], `Go invocation must select exactly one --${flag}`);
      }
      const roots = argumentValues(call.args, "runtime-root");
      if (mode.installed || roots.length) {
        assert.deepEqual(roots, [h.runtimeRoot], "Go must select exactly the effective Windows data root");
      }
      for (const [key, value] of Object.entries(databaseEnv)) {
        assert.equal(call.env[key], value, `Go must inspect the selected ${key}`);
        assert.equal(env[key], call.env[key], `service startup must use the inspected ${key}`);
      }
      assert.deepEqual(h.fileChanges, [], "after Go publishes, the JavaScript path must only read the identity");
      assert.equal(h.bytes, goIdentityBytes);
      assert.equal(env.LAZYMIND_MODEL_PROVIDER_SECRET_KEY, goModelKey);
      assert.equal(env.LAZYMIND_MCP_SECRET_KEY, goMCPKey);
    });
  }
});

test("managed Windows stops when Go fails or has not published a valid identity", async t => {
  const shortSecret = JSON.stringify({ ...goIdentity, deviceSecret: "c2hvcnQ" });
  const malformedSecret = JSON.stringify({ ...goIdentity, deviceSecret: `${goIdentity.deviceSecret}!` });
  for (const [name, publishedBytes, fail] of [
    ["helper fails", undefined, true],
    ["helper fails after publishing", goIdentityBytes, true],
    ["helper succeeds without a file", undefined, false],
    ["helper returns malformed JSON", "{invalid", false],
    ["helper returns a well-formed identity with an invalid secret", shortSecret, false],
    ["helper returns an invalid encoding of a full-length secret", malformedSecret, false],
  ]) {
    await t.test(name, () => {
      const h = fixture({ ...installedWindows, runGo: (_call, publish) => {
        if (publishedBytes !== undefined) publish(publishedBytes);
        if (fail) throw Object.assign(new Error("synthetic helper failure"), { stderr: "synthetic store inspection failed" });
      } });
      assert.throws(() => h.start(), fail ? /synthetic store inspection failed/ : /credential identity/i);
      assert.equal(h.goCalls.length, 1);
      assert.equal(h.goCalls[0].executable, h.sidecarPath);
      assert.equal(h.goCalls[0].precedingChanges, 0);
      assert.deepEqual(h.fileChanges, [], "startup must not publish a replacement in JavaScript");
      assert.equal(h.bytes, publishedBytes);
    });
  }
});

test("managed Windows reads an existing valid identity without changing it or invoking Go", async t => {
  const existing = `${JSON.stringify({ ...goIdentity, deviceSecret: `${goIdentity.deviceSecret}=` })}\n`;
  for (const [name, mode] of [["registered Windows data root", installedWindows], ["default legacy Windows layout", legacyWindows]]) {
    await t.test(name, () => {
      const h = fixture({ ...mode, initialBytes: existing });
      const env = h.start();
      assert.deepEqual(h.goCalls, []);
      assert.deepEqual(h.fileChanges, []);
      assert.equal(h.bytes, existing);
      assert.equal(env.LAZYMIND_MODEL_PROVIDER_SECRET_KEY, goModelKey);
      assert.equal(env.LAZYMIND_MCP_SECRET_KEY, goMCPKey);
    });
  }
});

test("managed Windows rejects an existing invalid secret even with an explicit cloud key", () => {
  const existing = JSON.stringify({ ...goIdentity, deviceSecret: `${goIdentity.deviceSecret}!` });
  const h = fixture({
    ...installedWindows,
    initialBytes: existing,
    env: { LAZYMIND_AUTH_CLOUD_SECRET_KEY: "synthetic-explicit-cloud-key" },
    runGo() {
      throw Object.assign(new Error("invalid credential device identity"), { stderr: "invalid credential device identity" });
    },
  });
  assert.throws(() => h.start(), /credential (?:device )?identity/i);
  assert.deepEqual(h.fileChanges, []);
  assert.equal(h.bytes, existing);
});

test("unmanaged Windows development and non-Windows startup retain JavaScript creation", async t => {
  for (const [name, options] of [
    ["Windows external development runtime", { ...legacyWindows, externalDev: true }],
    ["macOS", { platform: "darwin", installed: false }],
    ["Linux", { platform: "linux", installed: false }],
  ]) {
    await t.test(name, () => {
      const h = fixture(options);
      const env = h.start();
      assert.deepEqual(h.goCalls, []);
      assert.deepEqual(h.fileChanges, [
        { operation: "mkdir", path: h.identityDir },
        { operation: "write", path: h.identityPath },
      ]);
      assert.equal(JSON.parse(h.bytes).deviceId, "synthetic-js-device");
      assert.ok(env.LAZYMIND_MODEL_PROVIDER_SECRET_KEY);
      assert.ok(env.LAZYMIND_MCP_SECRET_KEY);
      assert.notEqual(env.LAZYMIND_MODEL_PROVIDER_SECRET_KEY, env.LAZYMIND_MCP_SECRET_KEY);
    });
  }
});
