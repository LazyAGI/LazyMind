import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

test("Python pruning preserves runtime helpers and Skill assets", () => {
  execFileSync(process.platform === "win32" ? "python" : "python3", [
    "-m", "unittest", "discover", "-s", "tests", "-p", "test_desktop_python_pruning.py", "-v",
  ], { cwd: root, stdio: "pipe" });
});

test("Mac pruning retains functional gates without the verbose size audit", () => {
  const source = readFileSync(path.join(root, "desktop/scripts/build-darwin-arm64.sh"), "utf8");
  const workflow = readFileSync(path.join(root, ".github/workflows/macos-installer.yml"), "utf8");
  const rag = source.indexOf("install rag");
  assert.ok(rag >= 0 && source.indexOf("prune-python-runtime.py") > rag);
  assert.match(source, /LAZYMIND_DESKTOP_PRUNE_PYTHON/);
  assert.match(source, /--verify-doubao/);
  assert.doesNotMatch(source + workflow, /python-size-report/);
});

test("Windows uses stable packaging with functional gates and no size audit", () => {
  const source = readFileSync(path.join(root, "desktop/scripts/build-windows-x64.ps1"), "utf8");
  const workflow = readFileSync(path.join(root, ".github/workflows/windows-installer.yml"), "utf8");
  for (const [key, value] of [["DEFER_HISTORY", "true"], ["DEFER_PYTHON", "true"], ["PRUNE_PYTHON", "true"], ["SHARE_PYTHON", "false"]]) {
    assert.ok(source.includes(`$env:LAZYMIND_DESKTOP_${key} = '${value}'`));
    assert.ok(workflow.includes(`LAZYMIND_DESKTOP_${key}: "${value}"`));
  }
  assert.doesNotMatch(workflow, /inputs\.(defer_history|defer_python|prune_python|share_python)/);
  assert.doesNotMatch(source + workflow, /python-size-report|report-runtime-size|final-runtime-size|Get-PSDrive|share-python-dependencies/);
  const locked = source.indexOf("windows-amd64-requirements.lock");
  const prune = source.indexOf("prune-python-runtime.py");
  const split = source.indexOf("build-python-components.py");
  assert.ok(locked >= 0 && locked < prune && prune < split);
  assert.match(source.slice(prune, split), /'--apply'/);
  assert.match(source.slice(prune, split), /'--verify-doubao'/);
  assert.match(source, /--require-windows-milvus-patch/);
  assert.match(workflow, /windows-python-components/);
});
