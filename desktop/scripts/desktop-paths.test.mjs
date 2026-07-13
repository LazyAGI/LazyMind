import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { resolveWindowsDesktopPaths } = require("../electron/src/desktop-paths.js");

test("places the Windows Electron profile under LOCALAPPDATA LazyMind", () => {
  const paths = resolveWindowsDesktopPaths(
    { LOCALAPPDATA: String.raw`D:\Profiles\alice\AppData\Local` },
    String.raw`C:\Users\alice`,
  );

  assert.deepEqual(paths, {
    runtimeRoot: String.raw`D:\Profiles\alice\AppData\Local\LazyMind`,
    profileDir: String.raw`D:\Profiles\alice\AppData\Local\LazyMind\Desktop`,
    logsDir: String.raw`D:\Profiles\alice\AppData\Local\LazyMind\Logs\desktop`,
    crashDumpsDir: String.raw`D:\Profiles\alice\AppData\Local\LazyMind\Logs\crash-dumps`,
  });
});

test("falls back to the Windows local application data directory under home", () => {
  const paths = resolveWindowsDesktopPaths({}, String.raw`C:\Users\alice`);

  assert.equal(paths.runtimeRoot, String.raw`C:\Users\alice\AppData\Local\LazyMind`);
  assert.equal(paths.profileDir, String.raw`C:\Users\alice\AppData\Local\LazyMind\Desktop`);
});

test("does not derive any path from APPDATA", () => {
  const paths = resolveWindowsDesktopPaths(
    { APPDATA: String.raw`C:\Users\alice\AppData\Roaming` },
    String.raw`C:\Users\alice`,
  );

  for (const value of Object.values(paths)) {
    assert.equal(value.includes("Roaming"), false);
  }
});
