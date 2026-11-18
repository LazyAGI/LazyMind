import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
const require = createRequire(import.meta.url);
const { sendUpdateState } = require("../electron/src/update-controller.js");

test("the actual Electron entrypoint starts the updater and binds current window/origin authority", () => {
  const source = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  assert.match(source, /require\(["']\.\/update-controller(?:\.js)?["']\)/);
  assert.match(source, /require\(["']\.\/update-helper-client(?:\.js)?["']\)/);
  assert.match(source, /const desktopUpdater = createDesktopUpdater\(/);
  assert.match(source, /registerUpdateIPC\(\{[\s\S]*?updater: desktopUpdater[\s\S]*?authorize: \(event\) => isTrustedNotificationSender\(event, mainWindow, notificationFrontendOrigin\(\)\)/);
  assert.match(source, /desktopUpdater\.start\(\)/);
  assert.match(source, /desktopUpdater\.subscribe\(\(state\) => sendUpdateState\(mainWindow, notificationFrontendOrigin\(\), state\)\)/);
  assert.match(source, /app\.on\(["']will-quit["'],[\s\S]*?desktopUpdater\.dispose\(\)/);
  const residentClose = source.slice(source.indexOf('app.on("window-all-closed"'), source.indexOf('app.on("before-quit", (event)'));
  assert.ok(residentClose.includes("window-all-closed"));
  assert.doesNotMatch(residentClose, /desktopUpdater\.(?:cancel|dispose)/);
});

test("state forwarding preserves revisions and sends only to the current trusted main frame", () => {
  const sends = []; const frame = { url: "http://127.0.0.1:3131/settings" };
  const contents = { mainFrame: frame, isDestroyed: () => false, send: (...args) => sends.push(args) };
  const window = { webContents: contents, isDestroyed: () => false };
  const state = { revision: 17, phase: "downloading", currentVersion: "1.0.0", downloadedBytes: 1, totalBytes: 2 };
  sendUpdateState(window, "http://127.0.0.1:3131", state);
  assert.deepEqual(sends, [["lazymind:update:state", state]]);
  frame.url = "https://attacker.invalid"; sendUpdateState(window, "http://127.0.0.1:3131", { ...state, revision: 18 });
  sendUpdateState(null, "http://127.0.0.1:3131", state); assert.equal(sends.length, 1);
});

test("the real settings navigation and app layout expose the update controls", () => {
  const settings = readFileSync(new URL("../../frontend/src/modules/settings/index.tsx", import.meta.url), "utf8");
  const layout = readFileSync(new URL("../../frontend/src/layouts/MainLayout.tsx", import.meta.url), "utf8");
  assert.match(settings, /import DesktopUpdateSettings from ["']\.\/DesktopUpdateSettings["']/);
  assert.match(settings, /isDesktopRuntime\(\)[\s\S]{0,160}id: "updates"/);
  assert.match(settings, /section === "updates"[\s\S]{0,180}<DesktopUpdateSettings/);
  assert.match(layout, /import \{ DesktopUpdateNotice \} from ["']@\/modules\/settings\/DesktopUpdateSettings["']/);
  assert.match(layout, /<DesktopUpdateNotice\s*\/>/);
});
