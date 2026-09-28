// Source contracts complement behavioral tests; packaged Electron remains a separate acceptance step.
import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

const source = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
function handler(channel) {
  const start = source.indexOf(`ipcMain.handle("${channel}"`);
  assert.ok(start >= 0, `missing existing IPC handler ${channel}`);
  const end = source.indexOf("\nipcMain.", start + 1);
  return source.slice(start, end < 0 ? undefined : end);
}

test("main process creates the native bridge with Electron Notification", () => {
  assert.ok(/require\(["']\.\/native-notifications\.js["']\)/.test(source), "native bridge is not imported");
  assert.ok(/createDesktopNotifications\(\s*\{[\s\S]*?\bNotification\b/.test(source),
    "native bridge must receive Electron Notification");
});

test("existing login and logout IPC synchronize the bridge after checking the sender", () => {
  const login = handler("lazymind:assistantSessionSet");
  const logout = handler("lazymind:assistantSessionClear");
  assert.match(login, /isTrustedNotificationSender\(/);
  assert.match(logout, /isTrustedNotificationSender\(/);
  assert.match(login, /\.setSession\(value\)/);
  assert.match(logout, /\.clearSession\(\)/);
  assert.ok(login.indexOf("isTrustedNotificationSender(") < login.indexOf(".setSession(value)"));
  assert.ok(logout.indexOf("isTrustedNotificationSender(") < logout.indexOf(".clearSession()"));
  // Preserve the existing assistant credential synchronization; no frontend rewrite.
  assert.match(login, /"internal", "session", "set"/);
  assert.match(logout, /"internal", "session", "clear"/);
});

test("explicit app shutdown stops the bridge while ordinary background mode keeps it resident", () => {
  const start = source.indexOf("function beginFastQuit(");
  const end = source.indexOf("function enterBackgroundMode(", start);
  assert.ok(start >= 0 && end > start);
  assert.match(source.slice(start, end), /\.stop\(\)/);
  const backgroundEnd = source.indexOf("\nfunction ", end + 1);
  assert.doesNotMatch(source.slice(end, backgroundEnd), /desktopNotifications\??\.stop\(\)/);
});
