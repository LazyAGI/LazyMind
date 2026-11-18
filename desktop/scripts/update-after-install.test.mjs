import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createDesktopUpdater } = require("../electron/src/update-controller.js");
const { createInstallIntentStore } = require("../electron/src/update-install-intent.js");
const next = { status: "direct", route: ["final"], next: { releaseId: "final", version: "1.2.0", package: { size: 4 } } };

function fixture(t, initial) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-post-update-"));
  const store = createInstallIntentStore(path.join(directory, "install.json"));
  store.write({ version: 1, phase: "installing", sourceVersion: "1.0.0", nextVersion: "1.1.0", releaseId: "bridge", dataProfile: "fixture", packagePath: path.join(directory, "bridge.zip") });
  const calls = [], timers = [];
  let decision = initial;
  const updater = createDesktopUpdater({ currentVersion: "1.1.0", installIntent: store,
    invokeHelper: async command => {
      calls.push(command);
      if (command === "config") return { status: "enabled", config: { version: "1.1.0", channel: "personal-test" } };
      assert.equal(command, "discover", "automatic checks must not download or install");
      return decision;
    },
    readLocalState: async () => ({ dataProfile: "fixture", dirty: false }),
    nativeInstaller: { capability: () => ({ supported: true }), dispose() {}, prepare() { assert.fail("unattended install"); } },
    runtime: { verifyStartup: async () => ({ configuration: { version: "1.1.0", releaseId: "bridge" }, local: { dataProfile: "fixture", dirty: false } }) },
    clock: { setTimeout: callback => { timers.push(callback); return callback; }, clearTimeout() {} },
  });
  t.after(() => { updater.dispose(); fs.rmSync(directory, { recursive: true, force: true }); });
  return { updater, store, calls, timers, setDecision: value => { decision = value; } };
}

test("successful bridge startup automatically discovers the next hop without installing it", async t => {
  const f = fixture(t, next);
  await f.updater.start();
  assert.equal(f.store.read(), null);
  assert.equal(f.updater.getState().phase, "available");
  assert.equal(f.updater.getState().nextVersion, "1.2.0");
  assert.deepEqual(f.calls, ["config", "discover"]);
});

test("installed success remains visible when current, while periodic checks can find a later release", async t => {
  const f = fixture(t, { status: "up_to_date", reason: "no_newer_compatible_release" });
  await f.updater.start();
  assert.equal(f.updater.getState().phase, "installed");
  assert.equal(f.calls.filter(c => c === "discover").length, 1);
  f.setDecision(next);
  await f.timers[0]();
  assert.equal(f.updater.getState().phase, "available");
  assert.equal(f.calls.filter(c => c === "discover").length, 2);
});
