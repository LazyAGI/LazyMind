import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";
const require = createRequire(import.meta.url);
const { installNotificationSessionSync } = require("../electron/src/preload.js");

function fixture() {
  const window = new EventTarget();
  window.location = { origin: "http://127.0.0.1:50124" };
  window.document = { readyState: "loading" };
  let stored = null;
  window.localStorage = { getItem: (key) => key === "lazymind:user" ? stored : null };
  const calls = [];
  const bridge = {
    assistantSessionSet: async (value) => { calls.push(["set", value]); },
    assistantSessionClear: async (request) => { calls.push(["clear", request]); },
  };
  const dispose = installNotificationSessionSync(window, bridge);
  return { window, bridge, calls, dispose, store: (value) => { stored = value; } };
}
const user = { username: "alice", token: "synthetic-access", refreshToken: "synthetic-refresh" };

test("existing stored login synchronizes on page readiness without visiting integrations", () => {
  const h = fixture();
  h.store(JSON.stringify(user));
  h.window.dispatchEvent(new Event("DOMContentLoaded"));
  assert.equal(h.calls.length, 1);
  assert.equal(h.calls[0][0], "set");
  assert.equal(h.calls[0][1].access_token, user.token);
  assert.equal(h.calls[0][1].server_url, h.window.location.origin);
});

test("existing login and refresh events synchronize once per changed session", () => {
  const h = fixture();
  h.store(JSON.stringify(user));
  h.window.dispatchEvent(new Event("lazymind:user-change"));
  h.window.dispatchEvent(new Event("lazymind:user-change"));
  assert.equal(h.calls.length, 1);
  h.store(JSON.stringify({ ...user, token: "synthetic-refreshed" }));
  h.window.dispatchEvent(new Event("lazymind:user-change"));
  assert.equal(h.calls.length, 2);
  assert.equal(h.calls[1][1].access_token, "synthetic-refreshed");
});

test("clearing or corrupting login storage clears notifications instead of keeping the previous user", () => {
  const h = fixture();
  h.store(JSON.stringify(user));
  h.window.dispatchEvent(new Event("lazymind:user-change"));
  h.store(null);
  h.window.dispatchEvent(new Event("storage"));
  assert.equal(h.calls[1][0], "clear");
  assert.equal(h.calls[1][1].expected_session.access_token, user.token);
  h.store(JSON.stringify(user));
  h.window.dispatchEvent(new Event("lazymind:user-change"));
  h.store("invalid JSON");
  h.window.dispatchEvent(new Event("storage"));
  assert.equal(h.calls[3][0], "clear");
});

test("an initially empty renderer cannot delete another connector login", () => {
  const h = fixture();
  h.window.dispatchEvent(new Event("DOMContentLoaded"));
  h.window.dispatchEvent(new Event("storage"));
  assert.deepEqual(h.calls, []);
  h.dispose();
});

test("failed logout cleanup retains its expected session for a later readiness retry", async () => {
  const h = fixture();
  h.store(JSON.stringify(user));
  h.window.dispatchEvent(new Event("lazymind:user-change"));
  let attempts = 0;
  h.bridge.assistantSessionClear = async request => {
    h.calls.push(["clear", request]);
    if (++attempts === 1) throw new Error("runtime restarting");
  };
  h.store(null);
  h.window.dispatchEvent(new Event("storage"));
  await new Promise(setImmediate);
  h.window.dispatchEvent(new Event("storage"));
  assert.equal(attempts, 2);
  assert.equal(h.calls.at(-1)[1].expected_session.access_token, user.token);
  h.dispose();
});

test("destroying a renderer removes its observers without logging out the resident notification bridge", () => {
  const h = fixture();
  h.store(JSON.stringify(user));
  h.window.dispatchEvent(new Event("lazymind:user-change"));
  h.dispose();
  h.window.dispatchEvent(new Event("lazymind:user-change"));
  assert.equal(h.calls.length, 1);
  assert.equal(h.calls[0][0], "set");
});

test("new preload replaces stale stored tokens before the application reads auth", () => {
  const { restoreNotificationSession } = require("../electron/src/preload.js");
  const raw = JSON.stringify(user);
  const values = new Map([["lazymind:user", raw]]);
  const target = { location: { origin: "http://127.0.0.1:50124" }, localStorage: {
    getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value),
  } };
  restoreNotificationSession(target, { sendSync: (channel, value) => {
    assert.equal(channel, "lazymind:notificationSessionRestore");
    assert.equal(value.access_token, user.token);
    return { ...value, access_token: "synthetic-background", refresh_token: "synthetic-rotated" };
  } });
  assert.equal(values.get("lazymind:user"), raw, "restore must not replace the active-session pointer");
  const refreshed = JSON.parse(values.get(`lazymind:refresh:${user.token}`));
  assert.equal(refreshed.credentials.token, "synthetic-background");
  assert.equal(refreshed.credentials.refreshToken, "synthetic-rotated");
  assert.equal(refreshed.base, raw);
});

test("preload cannot restore a logged-out or concurrently replaced login", () => {
  const { restoreNotificationSession } = require("../electron/src/preload.js");
  for (const initial of [null, JSON.stringify(user)]) {
    let stored = initial;
    const target = { location: { origin: "http://127.0.0.1:50124" }, localStorage: {
      getItem: () => stored, setItem: () => assert.fail("must not resurrect login"),
    } };
    restoreNotificationSession(target, { sendSync: (_channel, value) => {
      stored = null;
      return { ...value, access_token: "synthetic-late", refresh_token: "synthetic-late-refresh" };
    } });
    assert.equal(stored, null);
  }
});

test('preload sync consumes only the refresh overlay belonging to the active session', () => {
  const h = fixture();
  const base = JSON.stringify({ ...user, sessionId: 'A' });
  const values = new Map([
    ['lazymind:user', base],
    ['lazymind:refresh:A', JSON.stringify({ base, endpoint: h.window.location.origin + '/api/authservice/auth/refresh', credentials: { token: 'rotated-A', refreshToken: 'rotated-refresh-A' } })],
  ]);
  h.window.localStorage.getItem = key => values.get(key) ?? null;
  h.window.dispatchEvent(new Event('storage'));
  assert.equal(h.calls.at(-1)[1].access_token, 'rotated-A');
  values.set('lazymind:user', JSON.stringify({ ...user, sessionId: 'B', token: 'B' }));
  h.window.dispatchEvent(new Event('storage'));
  assert.equal(h.calls.at(-1)[1].access_token, 'B');
});
