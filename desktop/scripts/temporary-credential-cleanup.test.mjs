import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";

const { clearTemporaryCredentials } = createRequire(import.meta.url)("../electron/src/temporary-credential-cleanup.js");

test("local-only Desktop performs no temporary credential request", async () => {
  await clearTemporaryCredentials({ cloudEnabled: false, corePort: 18000, internalToken: "fixture", fetch: () => assert.fail("unexpected request") });
});

test("temporary cleanup uses owner authentication and survives renderer destruction", async () => {
  let options;
  await clearTemporaryCredentials({
    cloudEnabled: true, corePort: 18000, internalToken: "fixture",
    fetch: async (url, init) => { assert.equal(url, "http://127.0.0.1:18000/internal/credential-vault/restores:clear-temporary"); options = init; return { ok: true }; },
    reportError: () => assert.fail("unexpected error"),
  });
  assert.equal(options.headers["X-LazyMind-Internal-Token"], "fixture");
  assert.equal(options.redirect, "error");
  assert.ok(options.signal instanceof AbortSignal);
  assert.equal(options.method, "POST");
});

test("temporary cleanup reports HTTP and transport failures without rejecting", async () => {
  for (const fetch of [async () => ({ ok: false }), async () => { throw new Error("offline"); }]) {
    let reports = 0;
    await clearTemporaryCredentials({ cloudEnabled: true, corePort: 18000, internalToken: "fixture", fetch, reportError: () => reports++ });
    assert.equal(reports, 1);
  }
});
