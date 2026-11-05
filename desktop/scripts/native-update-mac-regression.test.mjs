import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { createHash } from "node:crypto";
import { mkdtempSync, writeFileSync, copyFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import test from "node:test";
const require = createRequire(new URL("../electron/package.json", import.meta.url));
const { MacUpdater } = require("electron-updater");
const { createNativeUpdateInstaller } = require("./src/native-update-installer.js");

for (const alreadyDownloaded of [false, true]) test(`actual MacUpdater explicitly installs and restarts (native cache=${alreadyDownloaded})`, async t => {
  const directory = mkdtempSync(path.join(tmpdir(), "lazymind-real-mac-updater-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const configuration = { trustDomain: "fixture", audience: "official", channel: "stable", mode: "official", releaseId: "source", version: "1.0.0", platform: "darwin", arch: "amd64", signingIdentity: "Fixture Team", updaterProtocol: 1 };
  writeFileSync(path.join(directory, "app-update.yml"), JSON.stringify({
    updaterCacheDirName: "LazyMind-fixture-official-stable",
    lazymind: { protocol: 1, releaseId: "source", version: "1.0.0", mode: "official", platform: "darwin", arch: "amd64", signingIdentity: "Fixture Team" },
  }));
  const bytes = Buffer.from("fixture ZIP bytes: not an application");
  const original = path.join(directory, "original.zip"), staged = path.join(directory, "staged.zip");
  writeFileSync(original, bytes);
  const calls = [], native = new EventEmitter();
  native.checkForUpdates = () => calls.push("native.checkForUpdates");
  native.quitAndInstall = () => { calls.push("native.quitAndInstall"); native.emit("before-quit-for-update"); };
  // Use the locked dependency's real handoff methods. Only native OS effects,
  // Electron transport and file staging are replaced; no Electron is launched.
  const updater = Object.create(MacUpdater.prototype);
  EventEmitter.call(updater);
  updater.nativeUpdater = native;
  updater.squirrelDownloadedUpdate = alreadyDownloaded;
  updater.app = { quit: () => calls.push("app.quit") };
  updater.server = { close: callback => { calls.push("server.close"); callback(); } };
  Object.defineProperty(updater, "netSession", { value: { setProxy: async () => {} } });
  updater.setFeedURL = () => {};
  updater.checkForUpdates = async () => ({ isUpdateAvailable: true, updateInfo: { version: "1.1.0" } });
  updater.downloadUpdate = async () => { copyFileSync(original, staged); return [staged]; };
  assert.equal(updater.quitAndInstall, MacUpdater.prototype.quitAndInstall);
  assert.equal(updater.handleUpdateDownloaded, MacUpdater.prototype.handleUpdateDownloaded);
  const installer = createNativeUpdateInstaller({ platform: "darwin", arch: "x64", currentVersion: "1.0.0", isPackaged: true,
    resourcesPath: directory, createUpdater: () => updater, nativeAutoUpdater: native, setQuitting: () => {} });
  t.after(() => installer.dispose());
  const prepared = await installer.prepare({ configuration,
    release: { releaseId: "target", version: "1.1.0", platform: "darwin", arch: "amd64", signingIdentity: "Fixture Team",
      package: { size: bytes.length, sha256: createHash("sha256").update(bytes).digest("hex") } },
    receipt: { releaseId: "target", version: "1.1.0", packagePath: original },
  });
  assert.equal(updater.autoInstallOnAppQuit, false);
  assert.deepEqual(calls, [], "staging must not start Squirrel or quit the app");
  let settled = false;
  const attempt = prepared.install().then(value => { settled = true; return { value }; }, error => { settled = true; return { error }; });
  const deadline = Date.now() + 3000;
  while (calls.length === 0) { assert.ok(Date.now() < deadline); await new Promise(resolve => setTimeout(resolve, 1)); }
  if (!alreadyDownloaded) {
    assert.equal(settled, false);
    assert.deepEqual(calls, ["native.checkForUpdates"]);
    native.emit("update-downloaded");
  }
  assert.equal(calls.filter(call => call === "native.quitAndInstall").length, 1, "the real dependency must take its install-and-restart branch");
  assert.ok(!calls.includes("app.quit"), "ordinary app.quit does not satisfy update-and-restart");
  const outcome = await attempt;
  assert.equal(outcome.error, undefined);
  assert.deepEqual(outcome.value, { handoff: true });
});
