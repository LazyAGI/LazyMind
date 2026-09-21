const fs = require("node:fs");
const path = require("node:path");
const { createHash, randomUUID } = require("node:crypto");

const FEED = "/api/core/task-center/desktop-notifications";
const MAX_STATE_BYTES = 2 * 1024 * 1024;
const MAX_ENTRIES = 5000;
const hash = (value) => createHash("sha256").update(value).digest("hex");
const identifier = (value) => typeof value === "string" && value.length > 0 && value.length <= 256;
const notificationID = (value) => typeof value === "string" && /^[a-f0-9]{64}$/.test(value);

function localOrigin(value) {
  try {
    const url = new URL(value);
    if (url.protocol !== "http:" || !["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)
      || url.username || url.password || url.pathname !== "/" || url.search || url.hash) return null;
    return url.origin;
  } catch { return null; }
}

function isTrustedNotificationSender(event, window, frontendOrigin) {
  try {
    return Boolean(window && !window.isDestroyed() && !window.webContents.isDestroyed()
      && event.sender === window.webContents && event.senderFrame === window.webContents.mainFrame
      && event.senderFrame && localOrigin(frontendOrigin)
      && new URL(event.senderFrame.url).origin === frontendOrigin);
  } catch { return false; }
}

function createDesktopNotifications({ Notification, fetch = globalThis.fetch, getRuntime, openPath,
  statePath, icon, renewSession, report = () => {}, clock = { setTimeout, clearTimeout } }) {
  let generation = 0;
  let current = null;
  let timer;
  let backoff = 2500;
  let journal = { version: 1, entries: {} };
  let storageFailed = false;
  const requests = new Set();
  const native = new Map();
  const receipts = new Set();
  const reported = new Set();
  const reportOnce = (code) => {
    if (reported.has(code)) return;
    reported.add(code);
    report(code);
  };
  const alive = (context) => current === context && context.generation === generation;
  const activeRequest = (context, revision) => alive(context) && context.revision === revision;

  try {
    const info = fs.lstatSync(statePath);
    if (!info.isFile() || info.isSymbolicLink() || info.size > MAX_STATE_BYTES) throw new Error();
    journal = JSON.parse(fs.readFileSync(statePath, "utf8"));
    if (journal.version !== 1 || !journal.entries || typeof journal.entries !== "object"
      || Array.isArray(journal.entries) || Object.keys(journal.entries).length > MAX_ENTRIES
      || Object.entries(journal.entries).some(([key, value]) => !notificationID(key)
        || !notificationID(value?.id) || !notificationID(value?.scope)
        || !["submitting", "shown", "acked"].includes(value?.status))) throw new Error();
  } catch (error) {
    if (error.code !== "ENOENT") { storageFailed = true; report("DESKTOP_NOTIFICATION_STORAGE_UNAVAILABLE"); }
  }

  function persist() {
    const temporary = `${statePath}.${randomUUID()}.tmp`;
    let fd;
    try {
      if (storageFailed) throw new Error();
      fs.mkdirSync(path.dirname(statePath), { recursive: true });
      fd = fs.openSync(temporary, "wx", 0o600);
      fs.writeFileSync(fd, JSON.stringify(journal));
      fs.fsyncSync(fd);
      fs.closeSync(fd);
      fd = undefined;
      fs.renameSync(temporary, statePath);
    } catch {
      storageFailed = true;
      report("DESKTOP_NOTIFICATION_STORAGE_UNAVAILABLE");
      throw new Error("DESKTOP_NOTIFICATION_STORAGE_UNAVAILABLE");
    } finally {
      if (fd !== undefined) fs.closeSync(fd);
      try { fs.unlinkSync(temporary); } catch { /* Already renamed, or creation failed. */ }
    }
  }

  async function request(context, route, body) {
    const revision = context.revision;
    const accessToken = context.session.access_token;
    if (!alive(context) || context.suspended
      || (route !== "/api/authservice/auth/me" && (!context.verified || context.paused))) throw new Error("STALE_SESSION");
    const controller = new AbortController();
    requests.add(controller);
    const timeout = clock.setTimeout(() => controller.abort(), 10000);
    try {
      const response = await fetch(context.runtime.apiOrigin + route, {
        method: body ? "POST" : "GET", redirect: "error", signal: controller.signal,
        headers: { Authorization: `Bearer ${context.session.access_token}`, "Content-Type": "application/json" },
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      if (!activeRequest(context, revision) || context.session.access_token !== accessToken || controller.signal.aborted) throw new Error("STALE_SESSION");
      if (!response.ok) {
        if (response.status === 401 || response.status === 403) {
          context.paused = true;
          context.verified = false;
          if (response.status === 401 && context.user && renewSession) {
            context.needsRenewal = true;
          } else if (route === "/api/authservice/auth/me") clearSession();
        }
        throw Object.assign(new Error("DESKTOP_NOTIFICATION_REQUEST_FAILED"), { status: response.status });
      }
      // Bound response consumption as well as connection time, including streamed bodies.
      const reader = response.body.getReader();
      const chunks = [];
      let size = 0;
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          size += value.byteLength;
          if (size > 1024 * 1024) throw new Error("DESKTOP_NOTIFICATION_RESPONSE_INVALID");
          chunks.push(value);
        }
      } finally { await reader.cancel(); }
      if (!activeRequest(context, revision) || context.session.access_token !== accessToken || controller.signal.aborted) throw new Error("STALE_SESSION");
      return JSON.parse(Buffer.concat(chunks).toString("utf8"));
    } finally {
      clock.clearTimeout(timeout);
      requests.delete(controller);
    }
  }

  async function acknowledge(context, key) {
    const entry = journal.entries[key];
    const revision = context.revision;
    if (!alive(context) || !context.verified || context.paused || storageFailed || entry?.status !== "shown" || receipts.has(key)) return;
    receipts.add(key);
    try {
      await request(context, `${FEED}/${entry.id}:ack`, { device_id: "local", status: "delivered" });
      if (activeRequest(context, revision)) { entry.status = "acked"; persist(); }
    } catch { if (alive(context)) report("DESKTOP_NOTIFICATION_RECEIPT_PENDING"); }
    finally { receipts.delete(key); }
  }

  async function clicked(context, taskID) {
    const revision = context.revision;
    if (!context.verified || context.paused) return;
    try {
      const response = await request(context, `/api/core/task-center/tasks/${encodeURIComponent(taskID)}`);
      const task = response?.data ?? response;
      if (!activeRequest(context, revision) || !context.verified) return;
      const target = identifier(task.conversation_id)
        ? `/agent/chat/home/${encodeURIComponent(task.conversation_id)}` : "/task-center?tab=tasks";
      // The main process checks again after asynchronously restoring/creating a window.
      await openPath(context.runtime.frontendOrigin + target,
        () => activeRequest(context, revision) && context.verified && !context.paused);
    } catch { if (alive(context)) report("DESKTOP_NOTIFICATION_TASK_UNAVAILABLE"); }
  }

  function show(context, item) {
    if (!Notification.isSupported()) {
      reportOnce("DESKTOP_NOTIFICATION_OS_UNSUPPORTED");
      return;
    }
    if (!alive(context) || !context.verified || context.paused || storageFailed
      || item?.user_id !== context.user || item.channel !== "desktop" || item.status !== "pending"
      || !notificationID(item.notification_id) || !identifier(item.task_id)
      || !identifier(item.title) || typeof item.body !== "string" || [...item.body].length > 200) return;
    const key = hash(`${context.scope}:${item.notification_id}`);
    if (journal.entries[key]) return;
    if (Object.keys(journal.entries).length >= MAX_ENTRIES) {
      const completed = Object.keys(journal.entries).find((id) => journal.entries[id].status === "acked");
      if (!completed) { report("DESKTOP_NOTIFICATION_STORAGE_FULL"); return; }
      native.get(completed)?.removeAllListeners();
      try { native.get(completed)?.close(); } catch { /* Old notification already removed. */ }
      native.delete(completed);
      delete journal.entries[completed];
    }
    // Persist before the nontransactional OS call. A crash in this window stays unknown.
    const entry = journal.entries[key] = { id: item.notification_id, scope: context.scope, status: "submitting" };
    persist();
    try {
      const notification = new Notification({ title: item.title, body: item.body, ...(icon ? { icon } : {}) });
      native.set(key, notification);
      notification.on("show", () => {
        if (!alive(context) || entry.status !== "submitting" || storageFailed) return;
        entry.status = "shown";
        try { persist(); } catch { return; }
        void acknowledge(context, key);
      });
      notification.on("click", () => { if (alive(context)) void clicked(context, item.task_id); });
      notification.on("failed", () => { if (alive(context)) report("DESKTOP_NOTIFICATION_RESULT_UNKNOWN"); });
      notification.show();
    } catch { report("DESKTOP_NOTIFICATION_RESULT_UNKNOWN"); }
  }

  async function poll(context) {
    const revision = context.revision;
    let delay = 5000;
    try {
      const runtime = await getRuntime();
      if (!activeRequest(context, revision)) return;
      if (!runtime?.ready) { reportOnce("DESKTOP_NOTIFICATION_RUNTIME_NOT_READY"); return; }
      const apiOrigin = localOrigin(runtime.apiOrigin);
      const frontendOrigin = localOrigin(runtime.frontendOrigin);
      if (!apiOrigin || !frontendOrigin || !identifier(runtime.instanceId)
        || ![apiOrigin, frontendOrigin].includes(localOrigin(context.session.server_url))) {
        reportOnce("DESKTOP_NOTIFICATION_RUNTIME_BINDING_INVALID");
        context.paused = true;
        return;
      }
      if (context.runtime && (context.runtime.instanceId !== runtime.instanceId
        || context.runtime.apiOrigin !== apiOrigin || context.runtime.frontendOrigin !== frontendOrigin)) {
        // A changed runtime must be explicitly bound again by the existing login flow.
        clearSession();
        return;
      }
      context.runtime = { ...runtime, apiOrigin, frontendOrigin };
      if (context.needsRenewal) {
        const renewed = await renewSession(context.session, context.user,
          () => activeRequest(context, revision) && !context.suspended);
        if (!activeRequest(context, revision)) return;
        if (!renewed) throw new Error("DESKTOP_SESSION_RENEWAL_UNAVAILABLE");
        if (renewed.server_url !== context.session.server_url || !renewed.access_token || !renewed.refresh_token) {
          clearSession();
          return;
        }
        context.session = renewed;
        context.needsRenewal = false;
        context.paused = false;
      }
      const response = await request(context, "/api/authservice/auth/me");
      const identity = response?.data ?? response;
      if (!activeRequest(context, revision)) return;
      if (!identifier(identity?.user_id) || identity.status !== "active") { clearSession(); return; }
      if (context.user && context.user !== identity.user_id) {
        const replacement = context.rebinding ? context.session : null;
        clearSession();
        // A verified account switch gets an independent notification lifetime.
        // None of the previous user's objects or callbacks is transferred.
        if (replacement) await setSession(replacement);
        return;
      }
      context.user = identity.user_id;
      context.scope = hash(JSON.stringify([runtime.instanceId, apiOrigin, context.user]));
      context.verified = true;
      reportOnce("DESKTOP_NOTIFICATION_SESSION_VERIFIED");
      context.rebinding = false;
      for (const [key, entry] of Object.entries(journal.entries)) {
        if (entry.scope === context.scope && entry.status === "shown") await acknowledge(context, key);
      }
      if (!activeRequest(context, revision) || context.paused || storageFailed) return;
      let cursor = context.scanCursor || "";
      const seen = new Set();
      // Bound work per poll, including a buggy server emitting endlessly changing cursors.
      for (let count = 0; count < 20; count += 1) {
        const result = await request(context, `${FEED}?device_id=local&limit=100${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`);
        if (!activeRequest(context, revision)) return;
        const data = result.data;
        if (data?.device_id !== "local" || !Array.isArray(data.items) || data.items.length > 100
          || typeof data.next_cursor !== "string" || data.next_cursor.length > 512) throw new Error();
        for (const item of data.items) show(context, item);
        cursor = data.next_cursor;
        context.scanCursor = cursor;
        if (!cursor || seen.has(cursor)) { context.scanCursor = ""; break; }
        seen.add(cursor);
      }
      backoff = 2500;
    } catch (error) {
      if (activeRequest(context, revision)) {
        if (error?.status === 422) context.scanCursor = "";
        if (error?.code === "DESKTOP_SESSION_AUTHENTICATION_REQUIRED") {
          clearSession();
          report("DESKTOP_SESSION_AUTHENTICATION_REQUIRED");
          return;
        }
        report("DESKTOP_NOTIFICATION_POLL_UNAVAILABLE");
        delay = backoff = Math.min(backoff * 2, 60000);
      }
    } finally {
      if (activeRequest(context, revision) && (!context.paused || context.needsRenewal) && !context.suspended && !storageFailed) {
        timer = clock.setTimeout(() => { void poll(context); }, delay);
        timer?.unref?.();
      }
    }
  }

  function suspendSession(value) {
    if (current?.verified && !current.paused && !current.suspended
      && value?.access_token === current.session.access_token
      && value?.server_url === current.session.server_url) return;
    if (current) {
      current.revision += 1;
      current.suspended = true;
      current.verified = false;
      current.paused = true;
    }
    clock.clearTimeout(timer);
    timer = undefined;
    for (const controller of requests) controller.abort();
  }

  function clearSession() {
    suspendSession();
    generation += 1;
    current = null;
    for (const notification of native.values()) {
      notification.removeAllListeners();
      try { notification.close(); } catch { /* Already removed by the OS. */ }
    }
    native.clear();
  }

  function setSession(value) {
    if (current && value?.access_token === current.session.access_token
      && value?.server_url === current.session.server_url && !current.suspended && !current.paused) return current.started;
    if (!value || typeof value.access_token !== "string" || !value.access_token
      || value.access_token.length > 16384 || !localOrigin(value.server_url)) {
      clearSession();
      return Promise.resolve();
    }
    if (!current?.user || localOrigin(value.server_url) !== localOrigin(current.session.server_url)) clearSession();
    else suspendSession();
    backoff = 2500;
    const context = current ||= { generation, revision: 0 };
    context.session = { server_url: value.server_url, access_token: value.access_token, refresh_token: value.refresh_token };
    context.needsRenewal = false;
    context.suspended = false;
    context.paused = false;
    context.verified = false;
    context.rebinding = true;
    context.started = poll(context);
    return context.started;
  }

  return { setSession, suspendSession, clearSession, stop: clearSession };
}

module.exports = { createDesktopNotifications, isTrustedNotificationSender };
