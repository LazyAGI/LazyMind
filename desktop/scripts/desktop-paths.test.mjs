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

test("an installed DataRoot places the complete Electron profile on the selected volume", () => {
  const paths = resolveWindowsDesktopPaths(
    {
      LOCALAPPDATA: String.raw`C:\Users\alice\AppData\Local`,
      LAZYMIND_DESKTOP_DATA_ROOT: String.raw`D:\QA Tools\中文\Data`,
    },
    String.raw`C:\Users\alice`,
  );

  assert.deepEqual(paths, {
    runtimeRoot: String.raw`D:\QA Tools\中文\Data`,
    profileDir: String.raw`D:\QA Tools\中文\Data\Desktop`,
    logsDir: String.raw`D:\QA Tools\中文\Data\Logs\desktop`,
    crashDumpsDir: String.raw`D:\QA Tools\中文\Data\Logs\crash-dumps`,
  });
});

for (const [name, value] of [
  ["empty", ""],
  ["blank", "   "],
  ["relative", "Data"],
  ["drive-relative", String.raw`D:Data`],
  ["parent-relative", String.raw`..\Data`],
]) {
  test(`rejects an explicitly supplied ${name} installed root instead of using Local AppData`, () => {
    assert.throws(() => resolveWindowsDesktopPaths(
      {
        LOCALAPPDATA: String.raw`C:\Users\alice\AppData\Local`,
        LAZYMIND_DESKTOP_DATA_ROOT: value,
      },
      String.raw`C:\Users\alice`,
    ));
  });
}

test("resolving an installed root does not rewrite host or third-party environment values", () => {
  const environment = {
    LOCALAPPDATA: String.raw`C:\Users\alice\AppData\Local`,
    APPDATA: String.raw`C:\Users\alice\AppData\Roaming`,
    USERPROFILE: String.raw`C:\Users\alice`,
    GH_CONFIG_DIR: String.raw`C:\Users\alice\.config\gh`,
    LAZYMIND_DESKTOP_DATA_ROOT: String.raw`D:\QA Tools\中文\Data`,
  };
  const before = { ...environment };

  resolveWindowsDesktopPaths(environment, environment.USERPROFILE);

  assert.deepEqual(environment, before);
});
