import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { clearFrontendCaches } = require("../electron/src/frontend-cache.js");

test("clears renderer caches without deleting login or application storage", async () => {
  const calls = [];
  const logs = [];
  const rendererSession = {
    async clearCache() {
      calls.push(["clearCache"]);
    },
    async clearStorageData(options) {
      calls.push(["clearStorageData", options]);
    },
  };

  await clearFrontendCaches(rendererSession, (message) => logs.push(message));

  assert.deepEqual(calls, [
    ["clearCache"],
    ["clearStorageData", { storages: ["serviceworkers", "cachestorage"] }],
  ]);
  assert.equal(logs.length, 1);
});

test("does nothing when a renderer session is unavailable", async () => {
  await assert.doesNotReject(clearFrontendCaches(undefined));
});
