import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import vm from "node:vm";
const require = createRequire(import.meta.url);
const { createDesktopUpdater, sendUpdateState } = require("../electron/src/update-controller.js");

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

test("focusing a ready main window discovers a new release after 25 minutes without a click", async t => {
  const app = new EventEmitter(), mainWindow = {};
  const source = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  let now = 0, discoveries = 0;
  t.mock.method(Date, "now", () => now);
  const updater = createDesktopUpdater({ currentVersion: "0.5.222", readLocalState: async () => ({}),
    invokeHelper: async command => {
      if (command === "config") return { status: "enabled", config: { channel: "stable" } };
      assert.equal(command, "discover");
      if (++discoveries === 1) return { status: "up_to_date" };
      return { status: "direct", route: ["r224"], next: { releaseId: "r224", version: "0.5.224", package: { size: 100 } } };
    },
  });
  t.after(() => updater.dispose());
  const context = { app, mainWindow, desktopUpdater: updater, rendererReadyWait: undefined, isQuitting: false };
  vm.runInNewContext(source.slice(source.indexOf('  app.on("second-instance"'), source.indexOf('  app.whenReady()')), context);
  const focus = async window => { app.emit("browser-window-focus", {}, window); await new Promise(setImmediate); };
  await focus(mainWindow);
  assert.equal(discoveries, 0, "preserve the delayed first startup check");
  await updater.start();
  now = 25 * 60_000 - 1;
  await focus(mainWindow);
  assert.equal(discoveries, 1);
  now++;
  await focus({});
  context.rendererReadyWait = {};
  await focus(mainWindow);
  assert.equal(discoveries, 1, "ignore auxiliary or still-loading windows");
  context.rendererReadyWait = undefined;
  const states = [];
  updater.subscribe(state => states.push(state));
  await focus(mainWindow);
  assert.equal(discoveries, 2, "the focused main window must discover the new release");
  assert.equal(states.at(-1).phase, "available");
  assert.equal(states.at(-1).nextVersion, "0.5.224");
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
