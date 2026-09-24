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

test("both desktop builds verify Doubao HTTP after pruning and support an unpruned comparison", () => {
  for (const name of ["build-darwin-arm64.sh", "build-windows-x64.ps1"]) {
    const source = readFileSync(path.join(root, "desktop/scripts", name), "utf8");
    const rag = Math.max(source.indexOf("install rag"), source.indexOf("'install', 'rag'"));
    assert.ok(rag >= 0 && source.indexOf("prune-python-runtime.py") > rag);
    assert.match(source, /LAZYMIND_DESKTOP_PRUNE_PYTHON/);
    assert.match(source, /--verify-doubao/);
    assert.match(source, /python-size-report\.json/);
  }
});
