import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import test from "node:test";

const { createDesktopNotifications } = createRequire(import.meta.url)("../electron/src/native-notifications.js");
const origin = "http://127.0.0.1:50124";
const feed = "/api/core/task-center/desktop-notifications";
const envelope = (data) => ({ code: 0, message: "success", data });
const session = { server_url: origin, access_token: "synthetic-access", refresh_token: "synthetic-refresh" };
const flush = () => new Promise((resolve) => setImmediate(resolve));

function fixture(t, { wrapped = true, status = "active", recipient = "owner" } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-envelope-"));
  const shown = [], opened = [], calls = [];
  class Native extends EventEmitter {
    static isSupported() { return true; }
    constructor(options) { super(); this.options = options; }
    show() { shown.push(this); this.emit("show"); }
    close() {}
  }
  const bridge = createDesktopNotifications({
    Notification: Native, statePath: path.join(root, "receipts.json"),
    getRuntime: async () => ({ ready: true, apiOrigin: origin, frontendOrigin: origin, instanceId: "test-instance" }),
    openPath: async (url, valid) => { assert.ok(valid()); opened.push(url); },
    fetch: async (input) => {
      const route = new URL(input).pathname; calls.push(route);
      let result;
      if (route === "/api/authservice/auth/me") {
        const identity = { user_id: "owner", status };
        result = wrapped ? envelope(identity) : identity;
      } else if (route === feed) {
        result = envelope({ device_id: "local", next_cursor: "", items: [{
          notification_id: "a".repeat(64), user_id: recipient, task_id: "run",
          channel: "desktop", status: "pending", title: "每日简报", body: "验收完成",
        }] });
      } else if (route === `${feed}/${"a".repeat(64)}:ack`) {
        result = envelope({ status: "delivered" });
      } else if (route === "/api/core/task-center/tasks/run") {
        result = envelope({ conversation_id: "conversation one" });
      } else assert.fail("unexpected route: " + route);
      return new Response(JSON.stringify(result), { headers: { "Content-Type": "application/json" } });
    },
  });
  t.after(() => { bridge.stop(); fs.rmSync(root, { recursive: true, force: true }); });
  return { bridge, shown, opened, calls };
}

test("actual proxied auth envelope starts delivery and acknowledges once", async (t) => {
  const h = fixture(t);
  await h.bridge.setSession(session); await flush();
  await h.bridge.setSession(session); await flush();
  assert.equal(h.shown.length, 1);
  assert.equal(h.shown[0].options.body, "验收完成");
  assert.equal(h.calls.filter((route) => route.endsWith(":ack")).length, 1);
});

test("wrapped inactive identity cannot fetch or display notifications", async (t) => {
  const h = fixture(t, { status: "disabled" });
  await h.bridge.setSession(session);
  assert.equal(h.shown.length, 0);
  assert.deepEqual(h.calls, ["/api/authservice/auth/me"]);
});

test("wrapped active identity still rejects another user's notification", async (t) => {
  const h = fixture(t, { recipient: "another-owner" });
  await h.bridge.setSession(session);
  assert.ok(h.calls.includes(feed), "must reach feed after verifying the wrapped identity");
  assert.equal(h.shown.length, 0);
  assert.equal(h.calls.some((route) => route.endsWith(":ack")), false);
});

test("clicking notification resolves the conversation from the Core response envelope", async (t) => {
  const h = fixture(t, { wrapped: false });
  await h.bridge.setSession(session); await flush();
  assert.equal(h.shown.length, 1);
  h.shown[0].emit("click"); await flush();
  assert.deepEqual(h.opened, [origin + "/agent/chat/home/conversation%20one"]);
});
