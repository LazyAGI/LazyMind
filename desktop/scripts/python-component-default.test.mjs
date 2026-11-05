import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import test from "node:test";
import vm from "node:vm";

const filename = fileURLToPath(new URL("../electron/electron-builder.config.cjs", import.meta.url));
const source = readFileSync(filename, "utf8");
const originalRequire = createRequire(filename);

function fixture(environment = {}, failScript) {
  const calls = [];
  let restored = 0;
  const failure = new Error("fixture component validation failed");
  const invoke = async (_file, args) => {
    if (args.includes("-c")) return { stdout: "arm64\n" };
    const script = path.basename(args[0]);
    calls.push({ script, args: Array.from(args) });
    if (script === failScript) throw failure;
    return { stdout: "" };
  };
  const sandbox = {
    module: { exports: {} }, __dirname: path.dirname(filename), Buffer, setTimeout,
    process: { platform: "darwin", arch: "arm64", env: {
      LAZYMIND_DESKTOP_RUNTIME_STAGE: path.resolve("fixture-runtime"),
      LAZYMIND_DESKTOP_SIGNING_MODE: "adhoc", ...environment,
    } },
    console: { log() {} },
    require(name) {
      if (name === "node:util") return { ...originalRequire(name), promisify: () => invoke };
      return originalRequire(name);
    },
    stageFixture: () => ({ stagedRuntime: path.resolve("fixture-runtime") }),
    restoreFixture: () => { restored++; },
  };
  // Execute the actual build selector, replacing only subprocess and staging effects.
  vm.runInNewContext(source + "\nstageEmbeddedRuntime = stageFixture; restoreEmbeddedRuntime = restoreFixture; module.exports = splitPythonComponents;", sandbox, { filename });
  return { calls, failure, execute: () => sandbox.module.exports(path.resolve("fixture-app/Contents/Resources/runtime")), get restored() { return restored; } };
}

for (const [name, env, expected] of [
  ["default ad-hoc build", {}, "build-python-components.py"],
  ["explicit rebuild", { LAZYMIND_DESKTOP_REBUILD_PYTHON_COMPONENTS: "true" }, "build-python-components.py"],
  ["explicit published reuse", { LAZYMIND_DESKTOP_REBUILD_PYTHON_COMPONENTS: "false" }, "stage-published-python-components.py"],
  ["Developer ID cannot reuse a foreign-signed component", { LAZYMIND_DESKTOP_SIGNING_MODE: "developer-id", LAZYMIND_DESKTOP_REBUILD_PYTHON_COMPONENTS: "false" }, "build-python-components.py"],
]) test(name, async () => {
  const f = fixture(env);
  await f.execute();
  assert.equal(f.calls[0].script, expected);
  assert.equal(f.calls.some(call => call.script === "verify-python-components.py"), expected === "build-python-components.py");
  if (expected === "build-python-components.py") assert.ok(f.calls[0].args.includes("--slim-providers"));
  else for (const option of ["--catalog", "--lock", "--cache"]) assert.ok(f.calls[0].args.includes(option));
  assert.equal(f.restored, 1);
});

test("bundled Python does not generate an optional component", async () => {
  const f = fixture({ LAZYMIND_DESKTOP_DEFER_PYTHON: "false" });
  await f.execute();
  assert.deepEqual(f.calls.map(call => call.script), ["share-python-dependencies.py"]);
  assert.equal(f.restored, 1);
});

for (const script of ["build-python-components.py", "verify-python-components.py", "stage-published-python-components.py"]) {
  test(`failure in ${script} fails the build and restores staging`, async () => {
    const env = script === "stage-published-python-components.py" ? { LAZYMIND_DESKTOP_REBUILD_PYTHON_COMPONENTS: "false" } : {};
    const f = fixture(env, script);
    await assert.rejects(f.execute(), error => error === f.failure);
    assert.equal(f.calls.at(-1).script, script);
    assert.equal(f.restored, 1);
  });
}
