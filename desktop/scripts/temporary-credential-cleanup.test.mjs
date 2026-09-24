import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import test from "node:test";

const { clearTemporaryCredentials } = createRequire(import.meta.url)("../electron/src/temporary-credential-cleanup.js");

test("local-only Desktop performs no temporary credential request", async () => {
  await clearTemporaryCredentials({ cloudEnabled: false, corePort: 18000, internalToken: "fixture", fetch: () => assert.fail("unexpected request") });
});

test("temporary cleanup uses owner authentication and survives renderer destruction", async () => {
  let options;
  await clearTemporaryCredentials({
    cloudEnabled: true, corePort: 18000, internalToken: "fixture",
    fetch: async (url, init) => { assert.equal(url, "http://127.0.0.1:18000/internal/credential-vault/restores:clear-temporary"); options = init; return { ok: true }; },
    reportError: () => assert.fail("unexpected error"),
  });
  assert.equal(options.headers["X-LazyMind-Internal-Token"], "fixture");
  assert.equal(options.redirect, "error");
  assert.ok(options.signal instanceof AbortSignal);
  assert.equal(options.method, "POST");
});

test("temporary cleanup reports HTTP and transport failures without rejecting", async () => {
  for (const fetch of [async () => ({ ok: false }), async () => { throw new Error("offline"); }]) {
    let reports = 0;
    await clearTemporaryCredentials({ cloudEnabled: true, corePort: 18000, internalToken: "fixture", fetch, reportError: () => reports++ });
    assert.equal(reports, 1);
  }
});

test("closing Desktop destroys the renderer immediately while owner cleanup is pending", async () => {
  const source = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  // Extract the lifecycle functions without evaluating the Electron entry point.
  const start = source.indexOf("function clearTemporaryCredentials(reason)");
  const end = source.indexOf("function sameRuntimePath(", start);
  const events = [];
  let finish;
  const pending = new Promise((resolve) => { finish = resolve; });
  const context = {
    cloudBaseURL: "https://cloud.example.com", internalServiceToken: "owner-fixture",
    currentStatus: { config: { localProxy: { CoreHostPort: 18000 } } }, fetch: () => {},
    clearRuntimeTemporaryCredentials: (options) => { assert.equal(options.corePort, 18000); assert.equal(options.internalToken, "owner-fixture"); events.push("cleanup"); return pending; },
    appendStartupLog: () => {}, isInstallerWarmup: false, isQuitting: false,
    windowHiddenByUser: false, mainWindow: { isDestroyed: () => false, removeAllListeners: () => {}, destroy: () => events.push("destroy") }, startupWindow: undefined,
    finishStartupMetrics: () => {}, rendererReadyWait: undefined,
    ensureWindowsTray: () => {}, destroyWindowsTray: () => {}, isMac: false,
  };
  vm.createContext(context);
  vm.runInContext(source.slice(start, end), context);
  assert.equal(context.enterBackgroundMode("window close", { discoverable: true }), undefined);
  assert.deepEqual(events, ["cleanup", "destroy"]);
  finish();
  await pending;
});
