import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8").replace(/\r\n/g, "\n");
function declaration(name, next) {
  const definition = source.indexOf(`function ${name}`);
  assert.ok(definition >= 0);
  const start = source.slice(definition - 6, definition) === "async " ? definition - 6 : definition;
  return source.slice(start, source.indexOf(`\nfunction ${next}`, start));
}

test("main supplies its Windows icon when installing popup navigation", () => {
  let options;
  const context = vm.createContext({
    installExternalNavigationHandler: (...args) => { options = args[3]; },
    windowsDesktopIconPath: () => "/runtime/LazyMind.ico",
    path, __dirname: "/runtime", shell: { openExternal() {} },
    appendStartupLog() {}, serializeError: String,
  });
  vm.runInContext(declaration("attachExternalNavigationHandler", "configuredCloudOrigin"), context);
  context.attachExternalNavigationHandler({ webContents: {} });
  assert.equal(options.icon, "/runtime/LazyMind.ico");
});

for (const completed of [false, true]) test(`macOS confirms a loaded cleanup guard before warmup (previously completed=${completed})`, async () => {
  const events = [];
  let acknowledge;
  const ready = new Promise(resolve => { acknowledge = resolve; });
  const context = vm.createContext({
    isMac: true, isPackaged: true, isExternalRuntimeDev: false,
    app: { getVersion: () => "fixture-version" },
    macInstallationWarmupMarker: "/fixture/warmup.json",
    macWarmupCompleted: () => completed,
    startGuard() { events.push("guard-start"); return ready; },
    appendStartupLog() {}, startupMetricsRecorder: { mark() {} },
    createInstallationWarmupWindow: async () => ({}),
    runInstallerWarmup: async () => { events.push("services"); },
    markMacWarmupCompleted() {}, disposeInstallationWarmupWindow() {},
  });
  vm.runInContext(declaration("runMacInstallationWarmupIfNeeded", "startGuard"), context);
  const pending = context.runMacInstallationWarmupIfNeeded();
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(events, ["guard-start"], "services must not start until the cleanup guard has parsed its configuration");
  acknowledge();
  await pending;
  assert.deepEqual(events, completed ? ["guard-start"] : ["guard-start", "services"]);
});

for (const exitsEarly of [false, true]) test(`guard startup requires configuration acknowledgement (exits early=${exitsEarly})`, { timeout: 1000 }, async () => {
  const child = new EventEmitter();
  child.stdout = new EventEmitter();
  child.unref = () => {};
  let spawnOptions;
  const context = vm.createContext({
    guardProcess: null, guardPID: 0, guardReadyPromise: undefined,
    sidecarPath: "/fixture/manager", desktopLogsDir: "/fixture/logs", desktopShutdownTimeout: "5s",
    isWindows: false, path, process: { pid: 123 },
    fs: { existsSync: () => true, openSync: () => 42, appendFileSync() {}, closeSync() {} },
    ensureDesktopLogDirs() {}, appendStartupLog() {}, serializeError: String,
    sidecarArgs: () => ["guard"], sidecarShutdownEnv: () => ({}),
    spawn(_file, _args, options) { spawnOptions = options; return child; },
  });
  vm.runInContext(declaration("startGuard", "quoteWindowsArgument"), context);
  const pending = context.startGuard();
  assert.ok(pending && typeof pending.then === "function", "the caller must wait for the guard to load its configuration");
  assert.equal(spawnOptions.stdio[1], "pipe");
  assert.equal(context.startGuard(), pending, "concurrent startup must share the acknowledgement");
  if (exitsEarly) {
    child.emit("exit", 1, null);
    await assert.rejects(pending, /guard/i);
  } else {
    let settled = false;
    pending.then(() => { settled = true; });
    child.stdout.emit("data", Buffer.from("desktop-guard-"));
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(settled, false);
    child.stdout.emit("data", Buffer.from("ready\n"));
    await pending;
    child.emit("exit", 0, null);
  }
});

test("macOS warmup failure opens scrollable startup details with the complete error", async () => {
  const error = new Error("parse server failed\n" + "startup details\n".repeat(2000));
  const nativeDialogs = [];
  const windows = [];
  let failure;
  let broadcasts = 0;
  let quitReason;
  const context = vm.createContext({
    runMacInstallationWarmupIfNeeded: async () => { throw error; },
    frontendOpeningAllowed: false, windowHiddenByUser: false,
    showActiveWindow() { assert.fail("failed warmup must not start the product window"); },
    finishStartupMetrics() {}, console: { error() {} },
    dialog: { showErrorBox: (...args) => nativeDialogs.push(args) },
    app: { exit() { assert.fail("keep error details available until the window is closed"); } },
    serializeError: value => value.stack,
    setStartupFailure(value, message) { failure = { message, detail: value.stack }; },
    browserWindowOptions: () => ({ width: 1440, height: 960 }),
    BrowserWindow: class {
      constructor(options) { this.options = options; this.events = {}; windows.push(this); }
      once(name, listener) { this.events[name] = listener; }
      async loadURL(url) { this.html = decodeURIComponent(url.split(",").slice(1).join(",")); }
    },
    startupWindow: undefined,
    beginFastQuit(reason) { quitReason = reason; },
    broadcastStartupDiagnostics() { broadcasts += 1; },
  });
  vm.runInContext(declaration("loadingHTML", "browserWindowOptions"), context);
  const start = source.indexOf("    return runMacInstallationWarmupIfNeeded().then(");
  const end = source.indexOf('\n  });\n  app.on("window-all-closed"', start);
  assert.ok(start >= 0 && end > start);
  await vm.runInContext(`(async () => { ${source.slice(start, end)} })()`, context);
  assert.equal(nativeDialogs.length, 0);
  assert.equal(windows.length, 1);
  assert.equal(failure.detail, error.stack);
  assert.ok(failure.message.length < 200);
  assert.match(windows[0].html, /\.log\s*\{[^}]*overflow:\s*auto/s);
  assert.match(windows[0].html, /<pre[^>]*id="log"[^>]*tabindex="0"/);
  assert.ok(broadcasts > 0);
  windows[0].events.closed();
  assert.equal(quitReason, "warmup-failed");
});

test("moving the installed macOS executable triggers owned runtime shutdown", async (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-app-removal-"));
  const executable = path.join(root, "LazyMindApp");
  fs.writeFileSync(executable, "fixture executable");
  let watchCount = 0;
  let complete;
  const stopped = new Promise(resolve => { complete = resolve; });
  const app = new EventEmitter();
  app.requestSingleInstanceLock = () => true;
  app.whenReady = () => ({ then() {} });
  app.getPath = name => { assert.equal(name, "exe"); return executable; };
  t.after(() => { fs.unwatchFile(executable); fs.rmSync(root, { recursive: true, force: true }); });
  const context = vm.createContext({
    app, isMac: true, isWindows: false, isPackaged: true,
    isInstallerWarmup: false, isExternalRuntimeDev: false, isQuitting: false,
    fs: { ...fs, watchFile(file, options, listener) {
      assert.equal(file, executable);
      watchCount += 1;
      return fs.watchFile(file, { ...options, interval: 10 }, listener);
    } },
    beginFastQuit: reason => complete(reason),
  });
  vm.runInContext(source.slice(source.indexOf("const hasSingleInstanceLock =")), context);
  assert.equal(watchCount, 1, "installed macOS app must detect removal while it remains resident");
  fs.renameSync(executable, executable + ".removed");
  let timer;
  t.after(() => clearTimeout(timer));
  const reason = await Promise.race([stopped, new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error("app removal did not stop the runtime")), 2000);
  })]);
  assert.equal(reason, "application-removed");
  app.emit("will-quit");
});
