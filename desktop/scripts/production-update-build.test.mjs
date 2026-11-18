import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";
import os from "node:os";
const require = createRequire(import.meta.url);

test("default builder keeps the ordinary application identity without a local lab", t => {
  const previous = process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE;
  process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE = os.tmpdir();
  t.after(() => { if (previous === undefined) delete process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE; else process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE = previous; });
  const builder = require("../electron/electron-builder.config.cjs");
  assert.equal(builder.appId, "ai.lazymind.desktop");
  assert.equal(builder.productName, "LazyMind");
  assert.equal(builder.extraMetadata, undefined);
});
