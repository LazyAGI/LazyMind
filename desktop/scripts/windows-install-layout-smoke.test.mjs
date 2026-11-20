import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";

const source = readFileSync(fileURLToPath(new URL("./windows-install-layout-smoke.ps1", import.meta.url)), "utf8");
const entry = source.match(/\$moduleSmoke = @'\r?\n([\s\S]*?)\r?\n'@/);
assert.ok(entry, "the PowerShell harness must contain the explicit Node module entry");

// Execute only the harness entry with a test export. This checks actual
// invocation/awaiting, not PowerShell syntax or Windows application behavior.
function runEntry(t, exportedSource) {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-smoke-entry-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const module = path.join(root, "smoke export 中文.mjs");
  writeFileSync(module, exportedSource);
  const app = "C:\\QA Tools\\中文\\App\\LazyMind.exe";
  const runtimeRoot = "D:\\QA Tools\\中文\\Data";
  const result = spawnSync(process.execPath, ["--input-type=module", "-e", entry[1], module, app, runtimeRoot], { encoding: "utf8" });
  return { result, app, runtimeRoot };
}

test("Windows harness invokes and awaits the smoke export", (t) => {
  const exported = [
    "export async function runPackagedAppSmoke(options) {",
    "await new Promise(resolve => setTimeout(resolve, 5));",
    "console.log(JSON.stringify({ invocation: options }));",
    'return { gateway: "http://127.0.0.1:18080", state: { profile: "desktop" } };',
    "}",
  ].join("\n");
  const { result, app, runtimeRoot } = runEntry(t, exported);
  assert.equal(result.status, 0, result.stderr);
  const [called, verified] = result.stdout.trim().split("\n").map(JSON.parse);
  assert.deepEqual(called.invocation, { app, runtimeRoot, platform: "win32", leaveRunning: true, timeoutMs: 1800000 });
  assert.deepEqual(verified, { verified: true, gateway: "http://127.0.0.1:18080", profile: "desktop" });
});

test("Windows harness fails if the smoke export throws", (t) => {
  const { result } = runEntry(t, 'export async function runPackagedAppSmoke() { throw new Error("startup failed"); }');
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /startup failed/);
});

test("Windows harness fails for a missing or empty smoke export", async (t) => {
  for (const exported of ["export const unused = true;", "export async function runPackagedAppSmoke() {}"]) {
    await t.test(exported, (t) => {
      const { result } = runEntry(t, exported);
      assert.notEqual(result.status, 0, "a no-op entry must not be reported as verification");
    });
  }
});
