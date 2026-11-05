import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import vm from "node:vm";

const require = createRequire(import.meta.url);
const navigation = require("../electron/src/external-navigation.js");
const { createDesktopBridge } = require("../electron/src/preload.js");
const url = "https://github.com/login/device";

function trusted(value) {
  assert.equal(typeof navigation.isTrustedGitHubCLINavigation, "function", "GitHub-specific main-process validator is missing");
  return navigation.isTrustedGitHubCLINavigation(value);
}

test("permits the fixed GitHub device endpoint without Cloud configuration", () => {
  assert.equal(trusted(url), true);
});
for (const untrusted of [
  "http://github.com/login/device", "https://github.com.evil.test/login/device",
  "https://user:password@github.com/login/device", "https://github.com:444/login/device",
  "https://github.com/login/device?user_code=ABCD-EFGH", "https://github.com/login/device#fixture",
  "https://github.com/login/device/", "https://github.com/settings/applications/new",
  "https://github.com/login/%64evice", "javascript:alert(1)", "file:///tmp/github", "",
]) {
  test(`rejects an arbitrary renderer-supplied URL: ${untrusted}`, () => {
    assert.equal(trusted(untrusted), false);
  });
}

test("preload exposes the bounded GitHub IPC method without a credential getter", async () => {
  const calls = [];
  const bridge = createDesktopBridge({ invoke: async (...args) => { calls.push(args); return { opened: true }; } });
  assert.equal(typeof bridge.openGitHubCLIAuthorization, "function");
  assert.deepEqual(await bridge.openGitHubCLIAuthorization(url), { opened: true });
  assert.deepEqual(calls, [["lazymind:openGitHubCLIAuthorization", url]]);
  assert.equal("getGitHubToken" in bridge, false);
});

test("the actual main IPC handler rejects bypasses and calls shell.openExternal for the device URL", async () => {
  const source = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  // Existing main.js uses bounded top-level function and IPC declarations. Execute those declarations,
  // without booting Electron or replacing their URL validation with a permissive mock.
  const helper = source.match(/async function openTrustedGitHubCLINavigation\(rawURL\) \{[\s\S]*?\n\}/)?.[0];
  const registration = source.match(/ipcMain\.handle\("lazymind:openGitHubCLIAuthorization",[\s\S]*?\n\}\);/)?.[0];
  assert.ok(helper && registration, "main-process GitHub handler must be wired");
  const opened = [];
  const handlers = new Map();
  vm.runInNewContext(`${helper}\n${registration}`, {
    isTrustedGitHubCLINavigation: trusted,
    shell: { openExternal: async value => { opened.push(value); } },
    ipcMain: { handle: (name, handler) => handlers.set(name, handler) },
  });
  const handler = handlers.get("lazymind:openGitHubCLIAuthorization");
  for (const value of ["https://attacker.test/", "file:///tmp/github", `${url}?next=evil`]) {
    await assert.rejects(handler({}, value));
  }
  assert.deepEqual(opened, []);
  assert.equal((await handler({}, url)).opened, true);
  assert.deepEqual(opened, [url]);
});

test("GitHub does not broaden Feishu or Cloud navigation allowlists", () => {
  assert.equal(navigation.isTrustedFeishuCLINavigation(url), false);
  assert.equal(navigation.isTrustedCloudNavigation(url, "https://cloud.example.test", "provider-authorization"), false);
  assert.equal(navigation.isTrustedFeishuCLINavigation("https://open.feishu.cn/page/cli?user_code=fixture"), true);
  assert.equal(navigation.isTrustedCloudNavigation(`https://cloud.example.test/v1/provider-connections/authorize/${"a".repeat(43)}`,
    "https://cloud.example.test", "provider-authorization"), true);
});
