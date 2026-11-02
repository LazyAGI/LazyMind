import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
const require = createRequire(import.meta.url);
const { createDesktopBridge } = require("../electron/src/preload.js");
const { registerUpdateIPC } = require("../electron/src/update-controller.js");

test("resume is a fixed preload action with main-process authorization", async () => {
  const calls = [], handlers = new Map();
  const bridge = createDesktopBridge({ invoke: async (...args) => calls.push(args) });
  await bridge.updateResume("https://ignored.invalid/target");
  assert.deepEqual(calls, [["lazymind:update:resume"]]);
  let resumed = 0;
  registerUpdateIPC({ ipcMain: { handle: (name, handler) => handlers.set(name, handler) },
    updater: { resumeDownload: () => { resumed++; return { phase: "checking" }; } }, authorize: event => event.trusted === true });
  const resume = handlers.get("lazymind:update:resume");
  await assert.rejects(() => resume({ trusted: false })); assert.equal(resumed, 0);
  assert.deepEqual(await resume({ trusted: true }, { url: "https://ignored.invalid/" }), { phase: "checking" });
  assert.equal(resumed, 1);
});

test("the Electron updater uses a store scoped to its existing application data directory", () => {
  const main = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  assert.match(main, /downloadIntent: createDownloadIntentStore\(path.join\(app.getPath\("userData"\), "update-download-intent.json"\)\)/);
});
