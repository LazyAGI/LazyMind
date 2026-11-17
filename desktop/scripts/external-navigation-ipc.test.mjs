import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { canOpenWebURL } = require("../electron/src/external-navigation.js");
const { isTrustedNotificationSender } = require("../electron/src/native-notifications.js");
const source = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");

function fixture() {
  const calls = [], handlers = new Map();
  const frame = { url: "http://localhost:50124/agent/chat/home" };
  const webContents = { mainFrame: frame, isDestroyed: () => false };
  const owner = { webContents, isDestroyed: () => false };
  const context = {
    isQuitting: false,
    BrowserWindow: { fromWebContents: sender => sender === webContents ? owner : null },
    ipcMain: { handle: (channel, handler) => handlers.set(channel, handler) },
    notificationFrontendOrigin: () => "http://localhost:50124",
    isTrustedNotificationSender, canOpenWebURL,
    shell: { openExternal: async url => { calls.push(url); } },
  };
  const start = source.indexOf("ipcMain.handle('lazymind:openExternal'");
  const end = source.indexOf('ipcMain.handle("lazymind:appVersion"', start);
  assert.ok(start > 0 && end > start);
  vm.runInNewContext(source.slice(start, end), context);
  const event = { sender: webContents, senderFrame: frame };
  const invoke = (url, sender = event) => handlers.get("lazymind:openExternal")(sender, url);
  return { calls, context, frame, event, owner, invoke };
}

test("external navigation IPC opens the exact web URL from a trusted application frame", async () => {
  const h = fixture();
  const url = "https://example.org/docs?item=fixture#section";
  await h.invoke(url);
  assert.deepEqual(h.calls, [url]);
});

test("external navigation IPC rejects unsafe targets, foreign frames and shutdown", async () => {
  const h = fixture();
  for (const url of [null, {}, "javascript:alert(1)", "file:///tmp/file", "mailto:a@example.org",
    "https://user:pass@example.org", "https://example.org/\npath", "https://example.org/" + "a".repeat(8192)]) {
    await assert.rejects(h.invoke(url), /EXTERNAL_NAVIGATION_REJECTED/);
  }
  await assert.rejects(h.invoke("https://example.org", { sender: {}, senderFrame: h.frame }), /EXTERNAL_NAVIGATION_REJECTED/);
  await assert.rejects(h.invoke("https://example.org", { ...h.event, senderFrame: { url: h.frame.url } }), /EXTERNAL_NAVIGATION_REJECTED/);
  h.frame.url = "https://untrusted.example";
  await assert.rejects(h.invoke("https://example.org"), /EXTERNAL_NAVIGATION_REJECTED/);
  h.frame.url = "http://localhost:50124/agent/chat/home";
  h.context.isQuitting = true;
  await assert.rejects(h.invoke("https://example.org"), /EXTERNAL_NAVIGATION_REJECTED/);
  h.context.isQuitting = false;
  h.owner.isDestroyed = () => true;
  await assert.rejects(h.invoke("https://example.org"), /EXTERNAL_NAVIGATION_REJECTED/);
  assert.deepEqual(h.calls, []);
});

test("external browser failures return a safe error code and permit the next retry", async () => {
  const h = fixture();
  h.context.shell.openExternal = async () => { throw new Error("private operating system details"); };
  await assert.rejects(h.invoke("https://example.org"), error => error.message === "EXTERNAL_BROWSER_OPEN_FAILED");
  h.context.shell.openExternal = async url => { h.calls.push(url); };
  await h.invoke("https://example.org");
  assert.deepEqual(h.calls, ["https://example.org"]);
});
