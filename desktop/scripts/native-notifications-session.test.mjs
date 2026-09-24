import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import test from "node:test";
import { createRequire } from "node:module";
const { createNotificationSession } = createRequire(import.meta.url)("../electron/src/notification-session.js");

const source = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
function fixture() {
  const handlers = new Map();
  const calls = [];
  let finish;
  const gate = new Promise((resolve) => { finish = resolve; });
  const context = {
    isQuitting: false, notificationSessionRevision: 0, sessionWrites: Promise.resolve(),
    notificationSession: createNotificationSession(),
    mainWindow: {}, notificationFrontendOrigin: () => "http://127.0.0.1:50124",
    isTrustedNotificationSender: (event) => event.trusted,
    agentConnectorActionTimeoutMs: 15000,
    desktopNotifications: {
      clearSession: () => calls.push("clear-notifications"),
      suspendSession: () => calls.push("suspend-notifications"),
      setSession: () => calls.push("start-notifications"),
    },
    runConnectorJSON: async (args) => { calls.push(args[2]); await gate; return { ok: true }; },
    ipcMain: { handle: (name, callback) => handlers.set(name, callback), on: (name, callback) => handlers.set(name, callback) },
  };
  const start = source.indexOf('ipcMain.handle("lazymind:assistantSessionSet"');
  const end = source.indexOf('ipcMain.handle("lazymind:restartRuntime"', start);
  assert.ok(start > 0 && end > start);
  vm.runInNewContext(source.slice(start, end), context);
  return { handlers, calls, context, finish };
}

test("actual session IPC serializes credentials and a pending login cannot restart notifications after logout", async () => {
  const h = fixture();
  const login = h.handlers.get("lazymind:assistantSessionSet")({ trusted: true }, { access_token: "synthetic" });
  await Promise.resolve();
  await Promise.resolve();
  const logout = h.handlers.get("lazymind:assistantSessionClear")({ trusted: true });
  assert.equal(h.calls.filter((item) => item === "suspend-notifications").length, 1);
  assert.equal(h.calls.filter((item) => item === "clear-notifications").length, 1);
  h.finish();
  await Promise.all([login, logout]);
  assert.deepEqual(h.calls.filter((item) => ["set", "clear"].includes(item)), ["set", "clear"]);
  assert.ok(!h.calls.includes("start-notifications"));
});

test("actual session IPC rejects untrusted calls before credential or notification effects", async () => {
  const h = fixture();
  for (const channel of ["lazymind:assistantSessionSet", "lazymind:assistantSessionClear"]) {
    await assert.rejects(h.handlers.get(channel)({ trusted: false }, {}), /DESKTOP_SESSION_UNAVAILABLE/);
  }
  assert.deepEqual(h.calls, []);
});

test("actual pending login cannot start a notification poll after the application begins quitting", async () => {
  const h = fixture();
  const login = h.handlers.get("lazymind:assistantSessionSet")({ trusted: true }, { access_token: "synthetic" });
  h.context.isQuitting = true;
  h.finish();
  await login;
  assert.ok(!h.calls.includes("start-notifications"));
});

test("preload restoration is exact-session and trusted-frame only", () => {
  const h = fixture();
  const old = { server_url: "http://127.0.0.1:50124", access_token: "synthetic-old", refresh_token: "synthetic-old-refresh" };
  const fresh = { ...old, access_token: "synthetic-new", refresh_token: "synthetic-new-refresh" };
  h.context.notificationSession.remember(old);
  assert.equal(h.context.notificationSession.rotated(old, fresh), true);
  const restore = h.handlers.get("lazymind:notificationSessionRestore");
  const trusted = { trusted: true }; restore(trusted, old);
  assert.deepEqual(trusted.returnValue, fresh);
  for (const [event, value] of [[{ trusted: false }, old], [{ trusted: true }, { ...old, refresh_token: "another-login" }], [{ trusted: true }, null]]) {
    restore(event, value); assert.equal(event.returnValue, null);
  }
  h.context.notificationSession.clear();
  restore(trusted, old); assert.equal(trusted.returnValue, null);
});

test("many background rotations retain only the renderer handoff and latest session", () => {
  const state = createNotificationSession();
  const original = { server_url: "http://127.0.0.1:50124", access_token: "synthetic-0", refresh_token: "synthetic-refresh-0" };
  state.remember(original);
  let latest = original;
  for (let n = 1; n <= 100; n += 1) {
    const next = { ...original, access_token: `synthetic-${n}`, refresh_token: `synthetic-refresh-${n}` };
    assert.ok(state.rotated(latest, next)); latest = next;
  }
  assert.deepEqual(state.restore(original), latest);
  assert.equal(state.restore({ ...original, access_token: "synthetic-50", refresh_token: "synthetic-refresh-50" }), null);
  state.clear();
  assert.equal(state.rotated(latest, original), false);
});

function renewalFixture() {
  const notificationSession = createNotificationSession();
  const old = { server_url: "http://127.0.0.1:50124", access_token: "synthetic-old", refresh_token: "synthetic-refresh" };
  notificationSession.remember(old);
  let resolve;
  const gate = new Promise((done) => { resolve = done; });
  const calls = [];
  const context = { windowHiddenByUser: true, mainWindow: undefined, isQuitting: false,
    notificationSessionRevision: 1, sessionWrites: Promise.resolve(), notificationSession, notificationRenewalCandidate: undefined,
    runConnectorJSON: async (...args) => { calls.push(args); return gate; },
  };
  const start = source.indexOf("  renewSession: (session,");
  const end = source.indexOf("  getRuntime:", start);
  vm.runInNewContext(`renew = ({ ${source.slice(start, end)} }).renewSession`, context);
  return { context, old, calls, resolve };
}

test("actual main-process renewal is background-only and stores a validated handoff before reopening", async () => {
  const h = renewalFixture();
  h.context.windowHiddenByUser = false;
  assert.equal(h.context.renew(h.old, "alice", () => true), null);
  h.context.windowHiddenByUser = true;
  const result = h.context.renew(h.old, "alice", () => true);
  await Promise.resolve(); await Promise.resolve();
  assert.equal(h.calls.length, 1);
  assert.equal(h.calls[0][0].join(" "), "internal session renew");
  assert.equal(h.calls[0][2].user_id, "alice");
  const next = { ...h.old, access_token: "synthetic-next", refresh_token: "synthetic-next-refresh" };
  // Window opening waits for sessionWrites even after it turns off background mode.
  h.context.windowHiddenByUser = false;
  h.resolve({ ok: true, session: next });
  assert.deepEqual(await result, next);
  assert.deepEqual(h.context.notificationSession.restore(h.old), next);
});

test("logout during actual main-process renewal prevents credential handoff", async () => {
  const h = renewalFixture();
  const result = h.context.renew(h.old, "alice", () => true);
  await Promise.resolve(); await Promise.resolve();
  h.context.notificationSessionRevision += 1;
  h.context.notificationSession.clear();
  h.resolve({ ok: true, session: { ...h.old, access_token: "synthetic-late" } });
  assert.equal(await result, null);
  assert.equal(h.context.notificationSession.restore(h.old), null);
});

test("reopening and closing again advances the renderer handoff across renewal cycles", () => {
  const state = createNotificationSession();
  const first = { server_url: "http://127.0.0.1:50124", access_token: "synthetic-first", refresh_token: "synthetic-first-refresh" };
  const second = { ...first, access_token: "synthetic-second", refresh_token: "synthetic-second-refresh" };
  const third = { ...first, access_token: "synthetic-third", refresh_token: "synthetic-third-refresh" };
  state.remember(first); state.rotated(first, second);
  state.remember(state.restore(first)); // Recreated renderer synchronizes its restored session.
  state.rotated(second, third);
  assert.deepEqual(state.restore(second), third);
  assert.equal(state.restore(first), null);
});

test("actual window reopen disables new background refresh and awaits current credential write", async () => {
  let resolve;
  const pending = new Promise((done) => { resolve = done; });
  let created = 0;
  const context = { windowHiddenByUser: true, isMac: false, frontendOpeningAllowed: true,
    activeWindow: () => created ? {} : undefined, windowCreationPromise: undefined,
    appendStartupLog: () => {}, runtimeProcess: true, sessionWrites: pending,
    createWindow: async () => { created += 1; }, isQuitting: false, setStartupFailure: assert.fail,
  };
  const start = source.indexOf("function showActiveWindow()");
  const end = source.indexOf("function createRendererReadyWait", start);
  vm.runInNewContext(source.slice(start, end), context);
  const first = context.showActiveWindow();
  const second = context.showActiveWindow();
  assert.equal(first, second);
  assert.equal(context.windowHiddenByUser, false);
  assert.equal(created, 0);
  resolve(); await first;
  assert.equal(created, 1);
});

test("cold startup restores only the persisted original-session fingerprint", async () => {
  const { createHash } = await import("node:crypto");
  const original = { server_url: "http://127.0.0.1:50124", access_token: "synthetic-old", refresh_token: "synthetic-refresh" };
  const updated = { ...original, access_token: "synthetic-new", refresh_token: "synthetic-rotated",
    desktop_handoff: createHash("sha256").update(JSON.stringify([original.server_url, original.access_token, original.refresh_token])).digest("hex"),
  };
  const state = createNotificationSession();
  state.hydrate(updated);
  assert.deepEqual(state.restore(original), updated);
  assert.equal(state.restore({ ...original, refresh_token: "synthetic-another-login" }), null);
  assert.equal(state.restore({ ...original, server_url: "http://127.0.0.1:9999" }), null);
  state.clear();
  assert.equal(state.restore(original), null);
  state.hydrate({ ...updated, desktop_handoff: undefined });
  assert.equal(state.restore(original), null);
});

test("main-process retries identity verification with its in-memory rotated candidate", async () => {
  const h = renewalFixture();
  const candidate = { ...h.old, access_token: "synthetic-candidate", refresh_token: "synthetic-candidate-refresh" };
  const inputs = [];
  h.context.runConnectorJSON = async (_args, _timeout, input) => {
    inputs.push(input);
    return inputs.length === 1
      ? { ok: false, code: "DESKTOP_SESSION_RENEWAL_UNAVAILABLE", pending_session: candidate }
      : { ok: true, session: candidate };
  };
  await assert.rejects(h.context.renew(h.old, "alice", () => true), /DESKTOP_SESSION_RENEWAL_UNAVAILABLE/);
  assert.equal(h.context.notificationSession.restore(h.old).access_token, h.old.access_token);
  assert.deepEqual(await h.context.renew(h.old, "alice", () => true), candidate);
  assert.deepEqual(inputs[1].pending_session, candidate);
  assert.equal(h.context.notificationRenewalCandidate, undefined);
});
