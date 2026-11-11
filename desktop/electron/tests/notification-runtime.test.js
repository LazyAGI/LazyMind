const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { createNotificationRuntimeReader } = require("../src/notification-runtime");

test("resident notifications reuse verified runtime status without delaying feed polls", async (t) => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "lazymind-notification-runtime-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const stateDir = path.join(root, "state");
  const statePath = path.join(stateDir, "runtime-state.json");
  await fs.mkdir(stateDir);
  await fs.writeFile(statePath, '{"status":"ready"}');
  let monitor = {};
  let checks = 0;
  let now = 0;
  const read = createNotificationRuntimeReader({
    getMonitor: () => monitor,
    getRuntimeRoot: () => root,
    readRuntime: async () => ({ ready: true, instanceId: ++checks }),
    now: () => now,
  });

  const first = await read();
  for (now = 5000; now < 60000; now += 5000) assert.equal(await read(), first);
  assert.equal(checks, 1);
  assert.equal((await read()).instanceId, 2, "expiry triggers live verification");

  await fs.writeFile(statePath, '{"status":"restarting"}');
  assert.equal((await read()).instanceId, 3, "runtime state updates invalidate the cache");
  monitor = {};
  assert.equal((await read()).instanceId, 4, "a new supervisor invalidates the cache");
  await fs.unlink(statePath);
  assert.equal((await read()).instanceId, 5);
  assert.equal((await read()).instanceId, 6, "missing state always triggers live verification");

  await fs.writeFile(statePath, '{"status":"ready"}');
  monitor = null;
  assert.equal((await read()).instanceId, 7);
  assert.equal((await read()).instanceId, 8, "an unmonitored runtime is never cached");
});

test("an in-flight runtime check cannot be reused across monitor or state changes", async (t) => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "lazymind-notification-race-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const stateDir = path.join(root, "state");
  const statePath = path.join(stateDir, "runtime-state.json");
  await fs.mkdir(stateDir);
  await fs.writeFile(statePath, '{"status":"ready"}');
  let monitor = {};
  let change = () => { monitor = {}; };
  const read = createNotificationRuntimeReader({
    getMonitor: () => monitor,
    getRuntimeRoot: () => root,
    readRuntime: async () => {
      await change();
      return { ready: true };
    },
  });
  assert.equal(await read(), null);
  change = () => fs.writeFile(statePath, '{"status":"stopped"}');
  assert.equal(await read(), null);
});
