import assert from "node:assert/strict";
import { EventEmitter, once } from "node:events";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createDesktopNotifications } = require("../electron/src/native-notifications.js");
const FEED = "/api/core/task-center/desktop-notifications";

async function setup(t, handler) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-native-http-"));
  const server = http.createServer(handler);
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const origin = `http://127.0.0.1:${server.address().port}`;
  const shown = [];
  class Native extends EventEmitter {
    static isSupported() { return true; }
    constructor(options) { super(); this.options = options; }
    show() { shown.push(this); this.emit("show"); }
    close() {}
  }
  const bridge = createDesktopNotifications({
    Notification: Native, statePath: path.join(root, "receipts.json"),
    getRuntime: async () => ({ ready: true, apiOrigin: origin, frontendOrigin: origin, instanceId: "http-test" }),
    openPath: async () => {},
  });
  t.after(() => {
    bridge.stop();
    server.closeAllConnections();
    server.close();
    fs.rmSync(root, { recursive: true, force: true });
  });
  return { bridge, shown, origin, session: { server_url: origin, access_token: "synthetic-http-token" } };
}
function send(res, payload) {
  res.writeHead(200, { "Content-Type": "application/json" });
  res.end(JSON.stringify(payload));
}

test("real local HTTP consumes existing auth/Core contracts and submits a native receipt", { timeout: 5000 }, async (t) => {
  const recorded = [];
  const receipt = new EventEmitter();
  const received = once(receipt, "ack");
  const id = "a".repeat(64);
  const h = await setup(t, (req, res) => {
    recorded.push(req.url);
    assert.equal(req.headers.authorization, "Bearer synthetic-http-token");
    assert.equal(req.headers["x-user-id"], undefined);
    if (req.url === "/api/authservice/auth/me") return send(res, { user_id: "owner", status: "active" });
    if (req.url.startsWith(FEED + "?")) return send(res, { code: 0, data: {
      device_id: "local", next_cursor: "", items: [{
        notification_id: id, user_id: "owner", channel: "desktop", status: "pending",
        task_id: "run", title: "每日简报", body: "完成三项工作。",
      }],
    } });
    assert.equal(req.url, `${FEED}/${id}:ack`);
    assert.equal(req.method, "POST");
    let body = "";
    req.on("data", (chunk) => { body += chunk; });
    req.on("end", () => {
      assert.deepEqual(JSON.parse(body), { device_id: "local", status: "delivered" });
      send(res, { code: 0, data: { status: "delivered" } });
      receipt.emit("ack");
    });
  });
  await h.bridge.setSession(h.session);
  await received;
  assert.equal(h.shown.length, 1);
  assert.equal(h.shown[0].options.body, "完成三项工作。");
  assert.equal(recorded.length, 3);
});

test("logout cancels a real in-flight HTTP response without displaying old data", { timeout: 5000 }, async (t) => {
  const events = new EventEmitter();
  const waiting = once(events, "waiting");
  const h = await setup(t, (req, res) => {
    if (req.url === "/api/authservice/auth/me") return send(res, { user_id: "owner", status: "active" });
    events.emit("waiting");
    // Keep the real HTTP response open until the bridge aborts it.
  });
  const login = h.bridge.setSession(h.session);
  await waiting;
  h.bridge.clearSession();
  await login;
  assert.equal(h.shown.length, 0);
});
