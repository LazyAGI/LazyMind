import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { createDesktopBridge } = require("../electron/src/preload.js");

test("exposes only bounded Cloud navigation methods", async () => {
  const calls = [];
  const bridge = createDesktopBridge({ invoke: async (...args) => calls.push(args) });
  await bridge.openCloudLogin("https://cloud.lazymind.example/desktop/authorize?id=fixture");
  await bridge.openCloudRegister();
  await bridge.openCloudTokenPlan("https://cloud.lazymind.example/zh/console#token-plan");
  assert.deepEqual(calls, [
    ["lazymind:openCloudLogin", "https://cloud.lazymind.example/desktop/authorize?id=fixture"],
    ["lazymind:openCloudRegister"],
    ["lazymind:openCloudTokenPlan", "https://cloud.lazymind.example/zh/console#token-plan"],
  ]);
  assert.equal("getCloudToken" in bridge, false);
});
