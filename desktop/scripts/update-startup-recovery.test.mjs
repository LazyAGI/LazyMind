import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const { prepareDesktopStartup } = createRequire(import.meta.url)("../electron/src/update-startup.js");

test("completed bootstrap returns its recovered custom runtime to the Desktop caller", async () => {
  const result = await prepareDesktopStartup({
    isPackaged: true, ownerToken: "fixture-owner",
    invoke: async action => {
      assert.equal(action, "update-bootstrap");
      return JSON.stringify({ status: "allowed", runtimeRoot: "/fixture/custom-runtime" });
    },
  });
  assert.deepEqual(result, { allowed: true, runtimeStarted: false, runtimeRoot: "/fixture/custom-runtime" });
});
