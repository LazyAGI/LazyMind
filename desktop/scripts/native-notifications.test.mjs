import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { EventEmitter } from "node:events";
import fs from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const require = createRequire(import.meta.url);
const API = "http://127.0.0.1:50123";
const FRONTEND = "http://127.0.0.1:50124";
const FEED = "/api/core/task-center/desktop-notifications";
const ME = "/api/authservice/auth/me";
const session = (name = "alice") => ({
  server_url: API, username: name,
  access_token: `synthetic-access-${name}`, refresh_token: `synthetic-refresh-${name}`,
});
const notice = (name = "one", extra = {}) => ({
  notification_id: createHash("sha256").update(name).digest("hex"),
  user_id: "user-alice", app_name: "LazyMind", channel: "desktop", status: "pending",
  task_id: `task-${name}`, execution_id: `task-${name}`, schedule_id: "daily",
  title: "每日简报", body: "今天完成三项接口联调，下一步准备验收。", event: "succeeded",
  navigation: { type: "task", task_id: `task-${name}`, schedule_id: "daily" }, ...extra,
});
const response = (data, status = 200) => new Response(JSON.stringify(data), {
  status, headers: { "Content-Type": "application/json" },
});
const page = (items, next = "") => response({ code: 0, data: { items, next_cursor: next, device_id: "local" } });
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
};
async function flush() {
  // Drain promise callbacks; application time advances only via Clock.advance.
  for (let i = 0; i < 30; i += 1) await Promise.resolve();
  await new Promise((resolve) => setImmediate(resolve));
}

class Clock {
  time = 0;
  sequence = 0;
  timers = new Map();
  now = () => this.time;
  setTimeout = (callback, delay) => {
    const id = ++this.sequence;
    this.timers.set(id, { at: this.time + delay, callback });
    return id;
  };
  clearTimeout = (id) => this.timers.delete(id);
  async advance(milliseconds) {
    const end = this.time + milliseconds;
    let steps = 0;
    for (;;) {
      const next = [...this.timers.entries()].filter(([, timer]) => timer.at <= end)
        .sort((a, b) => a[1].at - b[1].at)[0];
      if (!next) break;
      assert.ok(++steps < 1000, "unbounded polling/timer loop");
      this.time = next[1].at;
      this.timers.delete(next[0]);
      // Timer handlers may begin asynchronous work; do not wait on blocked network calls.
      next[1].callback();
      await flush();
    }
    this.time = end;
    await flush();
  }
}

function harness(t, overrides = {}) {
  const { createDesktopNotifications } = require("../electron/src/native-notifications.js");
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-native-notifications-"));
  const clock = new Clock();
  const h = {
    root, clock, calls: [], shown: [], opened: [], logs: [], items: [notice()], supported: true,
    runtime: { apiOrigin: API, frontendOrigin: FRONTEND, instanceId: "local-test-instance", ready: true },
    meStatus: 200, owner: "user-alice", ackStatus: 200, task: { conversation_id: "conversation one" },
    feed: null, request: null, onShow: null, bridges: [],
  };
  class NativeNotification extends EventEmitter {
    static isSupported() { return h.supported; }
    constructor(options) { super(); this.options = options; this.closed = false; }
    show() { h.shown.push(this); h.onShow?.(this); }
    close() { this.closed = true; this.emit("close"); }
  }
  const request = async (input, init = {}) => {
    const url = new URL(input);
    const call = { url, init, at: clock.now() };
    h.calls.push(call);
    assert.equal(url.origin, API, "credentials must stay within the trusted local API origin");
    assert.equal(init.redirect, "error", "authenticated requests must not follow redirects");
    assert.ok(init.signal instanceof AbortSignal, "every request must be cancellable");
    if (h.request) return h.request(call);
    if (url.pathname === ME) return response({ user_id: h.owner, status: "active" }, h.meStatus);
    if (url.pathname === FEED) return h.feed ? h.feed(call) : page(h.items);
    if (url.pathname.endsWith(":ack")) return response({ code: 0, data: { status: "delivered" } }, h.ackStatus);
    if (url.pathname.startsWith("/api/core/task-center/tasks/")) return response(h.task);
    assert.fail(`unexpected request: ${url.pathname}`);
  };
  h.statePath = path.join(root, "native-notifications.json");
  h.create = () => {
    const bridge = createDesktopNotifications({
      Notification: NativeNotification, fetch: request, clock, statePath: h.statePath,
      getRuntime: async () => h.runtime,
      openPath: async (url) => h.opened.push(url),
      report: (...values) => h.logs.push(values), ...overrides,
    });
    h.bridges.push(bridge);
    return bridge;
  };
  h.bridge = h.create();
  h.feeds = () => h.calls.filter((call) => call.url.pathname === FEED);
  h.acks = () => h.calls.filter((call) => call.url.pathname.endsWith(":ack"));
  h.login = async (value = session()) => { await h.bridge.setSession(value); await flush(); };
  t.after(async () => {
    for (const bridge of h.bridges) await bridge.stop();
    fs.rmSync(root, { recursive: true, force: true });
  });
  return h;
}

test("shows the actual task result and waits for native show before acknowledging", async (t) => {
  const h = harness(t);
  await h.login();
  assert.equal(h.shown.length, 1);
  assert.equal(h.shown[0].options.title, h.items[0].title);
  assert.equal(h.shown[0].options.body, h.items[0].body);
  assert.equal(h.shown[0].options.toastXml, undefined);
  assert.notEqual(h.shown[0].options.urgency, "critical");
  assert.equal(h.acks().length, 0);
  h.shown[0].emit("show");
  await flush();
  assert.equal(h.acks().length, 1);
  assert.deepEqual(JSON.parse(h.acks()[0].init.body), { device_id: "local", status: "delivered" });
  assert.equal(h.acks()[0].init.method, "POST");
  for (const call of h.calls) {
    assert.equal(new Headers(call.init.headers).get("authorization"), `Bearer ${session().access_token}`);
    assert.equal(new Headers(call.init.headers).get("X-User-Id"), null);
  }
});

test("polls every five seconds and deduplicates repeated feed items and native callbacks", async (t) => {
  const h = harness(t);
  h.items.push(h.items[0]);
  await h.login();
  await h.clock.advance(4999);
  assert.equal(h.feeds().length, 1);
  await h.clock.advance(1);
  assert.equal(h.feeds().length, 2);
  assert.equal(h.shown.length, 1);
  h.shown[0].emit("show");
  h.shown[0].emit("show");
  await flush();
  assert.equal(h.acks().length, 1);
});

test("follows bounded feed pages and begins the next poll at the first page", async (t) => {
  const h = harness(t);
  h.feed = ({ url }) => url.searchParams.get("cursor") ? page([notice("two")]) : page([notice()], "opaque-cursor");
  await h.login();
  assert.equal(h.shown.length, 2);
  assert.equal(h.feeds().length, 2);
  for (const call of h.feeds()) {
    assert.equal(call.url.searchParams.get("device_id"), "local");
    assert.ok(Number(call.url.searchParams.get("limit")) >= 1);
    assert.ok(Number(call.url.searchParams.get("limit")) <= 100);
  }
  assert.equal(h.feeds()[1].url.searchParams.get("cursor"), "opaque-cursor");
  await h.clock.advance(5000);
  assert.ok(!h.feeds()[2].url.searchParams.get("cursor"));
  assert.equal(h.shown.length, 2);
});

test("a repeated cursor cannot create an infinite paging loop", async (t) => {
  const h = harness(t);
  h.feed = () => page([notice()], "same-cursor");
  await h.login();
  assert.ok(h.feeds().length <= 2);
  assert.equal(h.shown.length, 1);
  await h.bridge.stop();
  assert.equal(h.clock.timers.size, 0);
});

for (const kind of ["unsupported", "native-failed", "no-native-callback"]) {
  test(`${kind} is never reported as delivered or fabricated permission denial`, async (t) => {
    const h = harness(t);
    h.supported = kind !== "unsupported";
    await h.login();
    if (kind === "native-failed") h.shown[0].emit("failed", {}, "synthetic private OS diagnostic");
    await h.clock.advance(60000);
    assert.equal(h.acks().length, 0);
    if (kind === "unsupported") assert.equal(h.shown.length, 0);
    if (kind === "no-native-callback") assert.equal(h.shown.length, 1);
    assert.ok(!JSON.stringify(h.logs).includes("synthetic private OS diagnostic"));
  });
}

test("retries a failed receipt after restart without showing the notification again", async (t) => {
  const h = harness(t);
  h.ackStatus = 503;
  await h.login();
  h.shown[0].emit("show");
  await flush();
  assert.equal(h.acks().length, 1);
  await h.bridge.stop();
  h.ackStatus = 200;
  h.bridge = h.create();
  await h.login();
  assert.equal(h.shown.length, 1);
  assert.equal(h.acks().length, 2);
});

test("persists delivery state privately without result text or credentials", async (t) => {
  const h = harness(t);
  await h.login();
  h.shown[0].emit("show");
  await flush();
  const stored = fs.readFileSync(h.statePath, "utf8");
  assert.ok(stored.includes(h.items[0].notification_id));
  for (const secret of [session().access_token, session().refresh_token, h.items[0].title, h.items[0].body]) {
    assert.ok(!stored.includes(secret));
    assert.ok(!JSON.stringify(h.logs).includes(secret));
  }
  if (process.platform !== "win32") assert.equal(fs.statSync(h.statePath).mode & 0o777, 0o600);
});

test("a crash between native submission and its callback remains unknown after restart", async (t) => {
  const h = harness(t);
  await h.login();
  assert.equal(h.shown.length, 1);
  const submissionJournal = fs.readFileSync(h.statePath);
  await h.bridge.stop();
  // Restore exactly the durable bytes at the simulated crash boundary.
  fs.writeFileSync(h.statePath, submissionJournal);
  h.bridge = h.create();
  await h.login();
  await h.clock.advance(60000);
  assert.equal(h.shown.length, 1);
  assert.equal(h.acks().length, 0);
});

for (const kind of ["corrupt", "unwritable"]) {
  test(`${kind} delivery journal fails closed before any native side effect`, async (t) => {
    const h = harness(t);
    await h.bridge.stop();
    if (kind === "corrupt") fs.writeFileSync(h.statePath, "invalid JSON");
    else fs.mkdirSync(h.statePath);
    h.bridge = h.create();
    await h.login();
    assert.equal(h.shown.length, 0);
    assert.equal(h.acks().length, 0);
  });
}

for (const value of [
  { ...session(), server_url: "https://remote.invalid" },
  { ...session(), server_url: "http://127.0.0.1:50125" },
  { ...session(), server_url: "http://user:secret@127.0.0.1:50123" },
  { ...session(), server_url: `${API}/unexpected-path` },
  { ...session(), access_token: "" },
]) {
  test(`rejects an untrusted or incomplete session (${value.server_url}, token=${Boolean(value.access_token)})`, async (t) => {
    const h = harness(t);
    await h.login(value);
    assert.equal(h.calls.length, 0);
    assert.equal(h.shown.length, 0);
  });
}

test("does not query notifications until the current local runtime is ready", async (t) => {
  const h = harness(t);
  h.runtime.ready = false;
  await h.login();
  assert.equal(h.calls.length, 0);
  h.runtime.ready = true;
  await h.clock.advance(5000);
  assert.equal(h.shown.length, 1);
});

test("expired authentication pauses until existing session synchronization supplies a new token", async (t) => {
  const h = harness(t);
  h.meStatus = 401;
  await h.login();
  await h.clock.advance(60000);
  assert.equal(h.feeds().length, 0);
  assert.ok(h.calls.every((call) => call.url.pathname === ME));
  h.meStatus = 200;
  await h.login({ ...session(), access_token: "synthetic-refreshed-token" });
  assert.equal(h.shown.length, 1);
  assert.equal(new Headers(h.feeds()[0].init.headers).get("authorization"), "Bearer synthetic-refreshed-token");
});

test("network failures back off to at most sixty seconds and recover without resetting dedupe", async (t) => {
  const h = harness(t);
  let unavailable = true;
  h.feed = () => {
    if (unavailable) throw new Error(`synthetic private error ${session().access_token}`);
    return page(h.items);
  };
  await h.login();
  await h.clock.advance(240000);
  const times = h.feeds().map((call) => call.at);
  const intervals = times.slice(1).map((at, index) => at - times[index]);
  assert.ok(intervals.length >= 4 && intervals.length <= 10);
  assert.ok(intervals.every((delay) => delay >= 5000 && delay <= 60000));
  assert.ok(intervals.some((delay) => delay === 60000));
  unavailable = false;
  await h.clock.advance(60000);
  assert.equal(h.shown.length, 1);
  assert.ok(!JSON.stringify(h.logs).includes(session().access_token));
});

test("times out a blocked request at ten seconds and never overlaps polls", async (t) => {
  const h = harness(t);
  let aborted = false;
  h.feed = ({ init }) => new Promise((_resolve, reject) => {
    init.signal.addEventListener("abort", () => { aborted = true; reject(new Error("aborted")); }, { once: true });
  });
  const login = h.login();
  await flush();
  assert.equal(h.feeds().length, 1);
  await h.clock.advance(9999);
  assert.equal(h.feeds().length, 1);
  assert.equal(aborted, false);
  await h.clock.advance(1);
  await login;
  assert.equal(aborted, true);
  assert.equal(h.shown.length, 0);
});

test("logout aborts pending work and ignores its late response", async (t) => {
  const h = harness(t);
  const pending = deferred();
  h.feed = () => pending.promise;
  const login = h.login();
  await flush();
  const signal = h.feeds()[0].init.signal;
  await h.bridge.clearSession();
  assert.equal(signal.aborted, true);
  pending.resolve(page(h.items));
  await login;
  await h.clock.advance(60000);
  assert.equal(h.shown.length, 0);
  assert.equal(h.acks().length, 0);
  assert.equal(h.clock.timers.size, 0);
});

test("switching users closes old notifications and fences old callbacks and clicks", async (t) => {
  const h = harness(t);
  await h.login();
  const old = h.shown[0];
  h.owner = "user-bob";
  h.items = [notice("bob", { user_id: "user-bob" })];
  await h.login(session("bob"));
  assert.equal(old.closed, true);
  old.emit("show");
  old.emit("click");
  await flush();
  assert.equal(h.acks().length, 0);
  assert.equal(h.opened.length, 0);
  assert.equal(h.shown.length, 2);
});

test("dedupe is isolated by authenticated user and runtime instance", async (t) => {
  const h = harness(t);
  await h.login();
  h.shown[0].emit("show");
  await flush();
  await h.bridge.clearSession();
  h.owner = "user-bob";
  h.items[0] = { ...h.items[0], user_id: "user-bob" };
  await h.login(session("bob"));
  assert.equal(h.shown.length, 2);
  h.shown[1].emit("show");
  await flush();
  await h.bridge.stop();
  h.runtime.instanceId = "different-local-instance";
  h.bridge = h.create();
  await h.login(session("bob"));
  assert.equal(h.shown.length, 3);
});

for (const payload of [
  notice("wrong-owner", { user_id: "user-bob" }),
  notice("suppressed", { status: "skipped" }),
  notice("wrong-channel", { channel: "wechat" }),
  notice("bad-id", { notification_id: "../../outside" }),
  notice("oversize", { body: "a".repeat(100000) }),
]) {
  test(`rejects an inapplicable or malformed notification (${payload.task_id})`, async (t) => {
    const h = harness(t);
    h.items = [payload];
    await h.login();
    assert.equal(h.shown.length, 0);
    assert.equal(h.acks().length, 0);
  });
}

test("click resolves the owned task and opens only its encoded local conversation route", async (t) => {
  const h = harness(t);
  h.items[0].navigation.url = "https://untrusted.invalid/collect";
  h.task.conversation_id = "conversation/one?x=#fragment";
  await h.login();
  h.shown[0].emit("click");
  await flush();
  assert.deepEqual(h.opened, [`${FRONTEND}/agent/chat/home/${encodeURIComponent(h.task.conversation_id)}`]);
  assert.ok(h.calls.some((call) => call.url.pathname === "/api/core/task-center/tasks/task-one"));
});

test("click on a task without a conversation opens the existing task list", async (t) => {
  const h = harness(t);
  h.task = { conversation_id: "" };
  await h.login();
  h.shown[0].emit("click");
  await flush();
  assert.deepEqual(h.opened, [`${FRONTEND}/task-center?tab=tasks`]);
});

test("logout during task lookup prevents a late navigation", async (t) => {
  const h = harness(t);
  await h.login();
  const pending = deferred();
  h.request = () => pending.promise;
  h.shown[0].emit("click");
  await flush();
  await h.bridge.clearSession();
  pending.resolve(response({ conversation_id: "previous-user-conversation" }));
  await flush();
  assert.deepEqual(h.opened, []);
});

test("stop closes native objects, cancels timers and prevents further network work", async (t) => {
  const h = harness(t);
  await h.login();
  await h.bridge.stop();
  const requests = h.calls.length;
  h.shown[0].emit("show");
  h.shown[0].emit("click");
  await h.clock.advance(60000);
  assert.ok(h.shown.every((item) => item.closed));
  assert.equal(h.clock.timers.size, 0);
  assert.equal(h.calls.length, requests);
  assert.deepEqual(h.opened, []);
});

test("an unauthorized task lookup never opens a notification-supplied URL", async (t) => {
  const h = harness(t);
  await h.login();
  h.request = () => response({ message: "not found" }, 404);
  h.shown[0].emit("click");
  await flush();
  assert.deepEqual(h.opened, []);
});

test("a feed for another device is not consumed", async (t) => {
  const h = harness(t);
  h.feed = () => response({ data: { items: h.items, next_cursor: "", device_id: "another-device" } });
  await h.login();
  assert.equal(h.shown.length, 0);
  assert.equal(h.acks().length, 0);
});

test("a synchronous native show failure cannot forge a successful receipt", async (t) => {
  const h = harness(t);
  h.onShow = () => { throw new Error("synthetic private native failure"); };
  await h.login();
  assert.equal(h.acks().length, 0);
  assert.ok(!JSON.stringify(h.logs).includes("synthetic private native failure"));
});

for (const variant of ["main-frame", "other-window", "child-frame", "remote-frame", "destroyed-window", "missing-frame"]) {
  test(`session IPC trusts only the live local main frame (${variant})`, () => {
    const { isTrustedNotificationSender } = require("../electron/src/native-notifications.js");
    const frame = { url: `${FRONTEND}/agent/chat/home` };
    const webContents = { mainFrame: frame, isDestroyed: () => false };
    const window = { webContents, isDestroyed: () => variant === "destroyed-window" };
    const event = { sender: webContents, senderFrame: frame };
    if (variant === "other-window") event.sender = { mainFrame: frame };
    if (variant === "child-frame") event.senderFrame = { url: frame.url };
    if (variant === "remote-frame") frame.url = "https://untrusted.invalid";
    if (variant === "missing-frame") delete event.senderFrame;
    assert.equal(isTrustedNotificationSender(event, window, FRONTEND), variant === "main-frame");
  });
}

for (const beforeShow of [false, true]) {
  test(`same-user refresh preserves native notification and click (before show=${beforeShow})`, async (t) => {
    const h = harness(t);
    await h.login();
    const original = h.shown[0];
    if (!beforeShow) { original.emit("show"); await flush(); }
    await h.login({ ...session(), access_token: "synthetic-refreshed" });
    assert.equal(original.closed, false);
    assert.equal(original.listenerCount("click"), 1);
    if (beforeShow) { original.emit("show"); await flush(); }
    original.emit("click");
    await flush();
    assert.equal(h.shown.length, 1);
    assert.equal(h.acks().length, 1);
    assert.equal(h.opened.length, 1);
    const lookup = h.calls.findLast((call) => call.url.pathname.startsWith("/api/core/task-center/tasks/"));
    assert.equal(lookup.init.headers.Authorization, "Bearer synthetic-refreshed");
    assert.equal(Object.values(JSON.parse(fs.readFileSync(h.statePath)).entries)[0].status, "acked");
  });
}

test("show during credential verification is saved and acknowledged only after same-user verification", async (t) => {
  const h = harness(t);
  await h.login();
  const identity = deferred();
  h.request = ({ url }) => {
    assert.equal(url.pathname, ME);
    return identity.promise;
  };
  const refresh = h.bridge.setSession({ ...session(), access_token: "synthetic-refreshed" });
  await flush();
  h.shown[0].emit("show");
  h.shown[0].emit("click");
  await flush();
  assert.equal(h.acks().length, 0);
  assert.equal(h.opened.length, 0);
  assert.equal(Object.values(JSON.parse(fs.readFileSync(h.statePath)).entries)[0].status, "shown");
  h.request = null;
  identity.resolve(response({ user_id: h.owner, status: "active" }));
  await refresh;
  await flush();
  assert.equal(h.acks().length, 1);
  assert.equal(h.acks()[0].init.headers.Authorization, "Bearer synthetic-refreshed");
  assert.equal(h.shown.length, 1);
});

test("a suspended session retains show progress and rejects clicks until credentials are synchronized", async (t) => {
  const h = harness(t);
  await h.login();
  h.bridge.suspendSession();
  h.shown[0].emit("show");
  h.shown[0].emit("click");
  await h.clock.advance(60000);
  assert.equal(h.acks().length, 0);
  assert.equal(h.opened.length, 0);
  await h.login();
  assert.equal(h.shown[0].closed, false);
  assert.equal(h.acks().length, 1);
});

test("repeated identical session synchronization keeps shown notification clickable", async (t) => {
  const h = harness(t);
  await h.login();
  h.shown[0].emit("show");
  await flush();
  await h.login();
  h.shown[0].emit("click");
  await flush();
  assert.equal(h.shown[0].closed, false);
  assert.equal(h.shown.length, 1);
  assert.equal(h.opened.length, 1);
});

test("a refreshed token for another user cannot inherit a pending old notification", async (t) => {
  const h = harness(t);
  await h.login();
  const old = h.shown[0];
  h.owner = "user-bob";
  h.items = [];
  // The supplied username is unchanged: identity must come from auth/me.
  await h.login({ ...session(), access_token: "synthetic-bob" });
  old.emit("show");
  old.emit("click");
  await flush();
  assert.equal(old.closed, true);
  assert.equal(old.listenerCount("click"), 0);
  assert.equal(h.acks().length, 0);
  assert.equal(h.opened.length, 0);
});

test("invalid refreshed identity revokes old callbacks instead of accepting old-user receipts", async (t) => {
  const h = harness(t);
  await h.login();
  h.meStatus = 401;
  await h.login({ ...session(), access_token: "synthetic-invalid" });
  h.shown[0].emit("show");
  h.shown[0].emit("click");
  await flush();
  assert.equal(h.shown[0].closed, true);
  assert.equal(h.acks().length, 0);
  assert.equal(h.opened.length, 0);
});

test("late refresh authentication cannot overwrite a newer validated session", async (t) => {
  const h = harness(t);
  await h.login();
  const late = deferred();
  h.request = () => late.promise;
  const first = h.bridge.setSession({ ...session(), access_token: "synthetic-first" });
  await flush();
  h.request = null;
  await h.login({ ...session(), access_token: "synthetic-second" });
  late.resolve(response({ user_id: "other-user", status: "active" }));
  await first;
  h.shown[0].emit("show");
  await flush();
  assert.equal(h.shown[0].closed, false);
  assert.equal(h.acks().length, 1);
  assert.equal(h.acks()[0].init.headers.Authorization, "Bearer synthetic-second");
});

for (const beforeShow of [false, true]) {
  test(`actual main-process session IPC preserves native objects on refresh (before show=${beforeShow})`, async (t) => {
    const { default: vm } = await import("node:vm");
    const h = harness(t);
    await h.login();
    const original = h.shown[0];
    if (!beforeShow) { original.emit("show"); await flush(); }
    const credentials = deferred();
    const handlers = new Map();
    const context = {
      isQuitting: false, notificationSessionRevision: 0, sessionWrites: Promise.resolve(),
      notificationSession: require("../electron/src/notification-session.js").createNotificationSession(),
      mainWindow: {}, notificationFrontendOrigin: () => FRONTEND,
      isTrustedNotificationSender: () => true, agentConnectorActionTimeoutMs: 15000,
      desktopNotifications: h.bridge, runConnectorJSON: () => credentials.promise,
      ipcMain: { handle: (name, callback) => handlers.set(name, callback), on: (name, callback) => handlers.set(name, callback) },
    };
    const source = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
    const start = source.indexOf('ipcMain.handle("lazymind:assistantSessionSet"');
    const end = source.indexOf('ipcMain.handle("lazymind:restartRuntime"', start);
    vm.runInNewContext(source.slice(start, end), context);
    const refreshed = handlers.get("lazymind:assistantSessionSet")({}, { ...session(), access_token: "synthetic-ipc-refresh" });
    await flush();
    assert.equal(original.closed, false);
    if (beforeShow) { original.emit("show"); await flush(); assert.equal(h.acks().length, 0); }
    credentials.resolve({ ok: true });
    await refreshed;
    await flush();
    original.emit("click");
    await flush();
    assert.equal(original.closed, false);
    assert.equal(h.shown.length, 1);
    assert.equal(h.acks().length, 1);
    assert.equal(h.opened.length, 1);
    assert.equal(h.calls.findLast((call) => call.url.pathname.startsWith("/api/core/task-center/tasks/"))
      .init.headers.Authorization, "Bearer synthetic-ipc-refresh");
    original.emit("click");
    await handlers.get("lazymind:assistantSessionSet")({}, { ...session(), access_token: "synthetic-ipc-refresh" });
    await flush();
    assert.equal(h.opened.length, 2, "identical IPC synchronization must not invalidate an in-flight click");
  });
}

test("resident polling renews expired credentials without a renderer and preserves pending show/click", async (t) => {
  let h;
  const renewals = [];
  h = harness(t, { renewSession: async (old, user, alive) => {
    assert.equal(user, "user-alice"); assert.ok(alive()); renewals.push(old);
    h.meStatus = 200;
    return { ...old, access_token: "synthetic-background-access", refresh_token: "synthetic-background-refresh" };
  } });
  await h.login();
  const original = h.shown[0];
  h.meStatus = 401;
  await h.clock.advance(5000);
  original.emit("show");
  await flush();
  assert.equal(h.acks().length, 0);
  assert.equal(original.closed, false);
  await h.clock.advance(5000);
  assert.equal(renewals.length, 1);
  assert.equal(renewals[0].refresh_token, session().refresh_token);
  assert.equal(h.acks().length, 1);
  assert.equal(h.acks()[0].init.headers.Authorization, "Bearer synthetic-background-access");
  original.emit("click"); await flush();
  assert.equal(h.opened.length, 1);
  assert.equal(h.shown.length, 1);
});

test("temporary renewal failures back off and recover without closing existing notifications", async (t) => {
  let attempts = 0;
  let h;
  h = harness(t, { renewSession: async (old) => {
    attempts += 1;
    if (attempts < 3) throw new Error("temporary synthetic outage");
    h.meStatus = 200;
    return { ...old, access_token: "synthetic-recovered" };
  } });
  await h.login(); h.meStatus = 401;
  await h.clock.advance(20000);
  assert.equal(attempts, 2);
  assert.equal(h.shown[0].closed, false);
  await h.clock.advance(20000);
  assert.equal(attempts, 3);
  assert.equal(h.shown.length, 1);
  h.shown[0].emit("show"); await flush();
  assert.equal(h.acks()[0].init.headers.Authorization, "Bearer synthetic-recovered");
});

test("revoked refresh stops background polling and requires login", async (t) => {
  let attempts = 0;
  const h = harness(t, { renewSession: async () => {
    attempts += 1;
    throw Object.assign(new Error("revoked"), { code: "DESKTOP_SESSION_AUTHENTICATION_REQUIRED" });
  } });
  await h.login(); h.meStatus = 401;
  await h.clock.advance(120000);
  assert.equal(attempts, 1);
  assert.equal(h.shown[0].closed, true);
  assert.equal(h.clock.timers.size, 0);
});

for (const action of ["logout", "switch"]) {
  test(`pending background refresh cannot revive a session after ${action}`, async (t) => {
    const gate = deferred(); let canApply;
    const h = harness(t, { renewSession: async (_old, _user, alive) => { canApply = alive; return gate.promise; } });
    await h.login(); h.meStatus = 401;
    await h.clock.advance(10000);
    assert.ok(canApply());
    if (action === "logout") h.bridge.clearSession();
    else { h.meStatus = 200; h.owner = "user-bob"; h.items = []; await h.bridge.setSession(session("bob")); }
    assert.equal(canApply(), false);
    gate.resolve({ ...session(), access_token: "synthetic-late-renewal" }); await flush();
    await h.clock.advance(60000);
    assert.equal(h.shown.length, 1);
    assert.equal(h.shown[0].closed, true);
    assert.ok(!h.calls.some((call) => call.init.headers.Authorization === "Bearer synthetic-late-renewal"));
  });
}

test("runtime replacement stops expired-session renewal before sending refresh credentials", async (t) => {
  let renewals = 0;
  const h = harness(t, { renewSession: async () => { renewals += 1; return session(); } });
  await h.login(); h.meStatus = 401;
  await h.clock.advance(5000);
  h.runtime.instanceId = "replacement";
  await h.clock.advance(60000);
  assert.equal(renewals, 0);
  assert.equal(h.shown[0].closed, true);
});

for (const count of [1999, 2000, 2001]) {
  test(`bounded native scan progresses past ${count} unknown submissions`, async t => {
    const h = harness(t);
    // Seed uncertainty using the real submission path (no synthetic receipts).
    const prefix = Array.from({ length: count }, (_, i) => notice(`unknown-${i}`));
    let items = prefix;
    h.feed = ({ url }) => {
      const offset = Number(url.searchParams.get('cursor') || 0);
      return page(items.slice(offset, offset + 100), offset + 100 < items.length ? String(offset + 100) : '');
    };
    await h.login();
    await h.clock.advance(5000);
    assert.equal(h.shown.length, count);
    items = [...prefix, notice('new-after-unknown')];
    await h.clock.advance(15000);
    assert.equal(h.shown.length, count + 1);
    assert.equal(h.acks().length, 0);
  });
}

test('an obsolete continuation cursor restarts at the head on a later poll', async t => {
  const h = harness(t);
  let invalid = false;
  h.feed = ({ url }) => {
    const cursor = url.searchParams.get('cursor') || '';
    if (invalid && cursor) return response({}, 422);
    return page([], invalid ? '' : String(Number(cursor) + 1));
  };
  await h.login();
  assert.equal(h.feeds().length, 20);
  invalid = true;
  await h.clock.advance(15000);
  assert.equal(h.feeds().at(-1).url.searchParams.get('cursor'), null);
});
