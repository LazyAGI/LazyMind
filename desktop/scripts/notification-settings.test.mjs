import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
const { registerNotificationSettingsIPC } = require("../electron/src/notification-settings.js");
const { isTrustedNotificationSender } = require("../electron/src/native-notifications.js");

function setup(platform = "darwin", systemRelease = "25.0.0", openExternal = async () => {}) {
  const frame = { url: "http://127.0.0.1:5173/settings?section=notifications" };
  const webContents = { mainFrame: frame, isDestroyed: () => false };
  const window = { webContents, isDestroyed: () => false };
  const opened = [];
  let handler;
  registerNotificationSettingsIPC({
    platform, systemRelease,
    ipcMain: { handle(channel, callback) {
      assert.equal(channel, "lazymind:openNotificationSettings");
      handler = callback;
    } },
    shell: { async openExternal(url) { opened.push(url); await openExternal(url); } },
    isTrustedSender: (event) => isTrustedNotificationSender(event, window, "http://127.0.0.1:5173"),
  });
  return { opened, handler, frame, event: { sender: webContents, senderFrame: frame } };
}

for (const [name, platform, systemRelease, expected] of [
  ["macOS System Settings", "darwin", "25.0.0", "x-apple.systempreferences:com.apple.Notifications-Settings.extension"],
  ["macOS Monterey", "darwin", "21.6.0", "x-apple.systempreferences:com.apple.preference.notifications"],
  ["Windows notification settings", "win32", "10.0.26100", "ms-settings:notifications"],
]) {
  test(`opens ${name} only after an explicit trusted request`, async () => {
    const h = setup(platform, systemRelease);
    assert.deepEqual(h.opened, []);
    await h.handler(h.event, "https://ignored.example");
    assert.deepEqual(h.opened, [expected]);
  });
}

test("rejects requests from other windows, embedded frames and foreign origins", async () => {
  for (const kind of ["window", "frame", "origin"]) {
    const h = setup();
    if (kind === "window") h.event.sender = { mainFrame: h.frame };
    if (kind === "frame") h.event.senderFrame = { ...h.frame };
    if (kind === "origin") h.frame.url = "https://foreign.example/settings";
    await assert.rejects(h.handler(h.event));
    assert.deepEqual(h.opened, []);
  }
});

test("reports OS launch failures and allows a later retry", async () => {
  let attempts = 0;
  const h = setup("darwin", "25.0.0", async () => {
    if (++attempts === 1) throw new Error("OS launch failed");
  });
  await assert.rejects(h.handler(h.event), /OS launch failed/);
  await h.handler(h.event);
  assert.equal(attempts, 2);
});

test("unsupported platforms fail without launching anything", async () => {
  const h = setup("linux");
  await assert.rejects(h.handler(h.event), /not supported/);
  assert.deepEqual(h.opened, []);
});
