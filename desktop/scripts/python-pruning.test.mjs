import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

test("Mac installed RAG verification handles ad-hoc and official arguments with Bash nounset", {
  skip: process.platform === "win32",
}, () => {
  const workflow = readFileSync(path.join(root, ".github/workflows/macos-installer.yml"), "utf8");
  const step = workflow.split("      - name: Verify paired RAG with installed signed Python\n")[1]
    .split("\n      - name:")[0];
  const script = step.split("        run: |\n")[1].replace(/^          /gm, "");
  for (const official of ["false", "true"]) {
    const probe = script.replaceAll("${{ needs.build-macos-installer.outputs.official_release }}", official)
      .replace('"${runtime}/deps/python/algorithm/bin/python"', "printf '%s\\n'");
    // /bin/bash is Bash 3.2 on macOS: empty arrays fail under set -u there.
    const args = execFileSync("/bin/bash", ["-c", probe], {
      env: { ...process.env, INSTALLED_APP: "/tmp/Installed App/LazyMind.app", RUNNER_TEMP: "/tmp/Runner Temp" },
      encoding: "utf8",
    }).trim().split("\n");
    const expected = ["-I", "-B", "desktop/scripts/verify-python-components.py",
      "--runtime", "/tmp/Installed App/LazyMind.app/Contents/Resources/runtime",
      "--bundle-dir", "rag-under-test/darwin-arm64",
      "--report", "/tmp/Runner Temp/installed-rag-verification/report.json"];
    if (official === "true") expected.push("--require-macos-team-id");
    assert.deepEqual(args, expected);
  }
});

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

test("RAG publication uses ModelScope without advertising an unpublished HF mirror", () => {
  for (const filename of ["macos-installer.yml", "windows-installer.yml"]) {
    const workflow = readFileSync(path.join(root, ".github/workflows", filename), "utf8");
    assert.match(workflow, /LAZYMIND_MS_COMPONENT_REPO: \$\{\{ vars\.LAZYMIND_MS_COMPONENT_REPO \|\| 'CarlosShaoting\/lazymind-cst' \}\}/);
    const rag = workflow.split(/\n  publish-(?:macos|windows)-rag:/)[1].split(/\n  test-/)[0];
    assert.doesNotMatch(rag, /Realease/);
    assert.match(rag, /host: \[ms, hf\]/);
    assert.match(rag, /fail-fast: false/);
    assert.match(rag, /secrets\[matrix.host/);
    assert.match(rag, /--host "\$\{MIRROR_HOST\}"/);
    if (filename === "macos-installer.yml") {
      assert.match(rag, /host: \[ms, hf\]/);
      assert.match(rag, /signing_mode=adhoc/);
      assert.doesNotMatch(rag.split('    steps:')[0], /official_release == 'true'/);
    }
  }
});

test("RAG publishes tag metadata to ModelScope and verifies Mac signatures before publishing", () => {
  for (const filename of ["macos-installer.yml", "windows-installer.yml"]) {
    const workflow = readFileSync(path.join(root, ".github/workflows", filename), "utf8");
    assert.match(workflow, /MODELSCOPE_API_TOKEN:/);
    assert.match(workflow, /LAZYMIND_MS_COMPONENT_REPO/);
    assert.match(workflow, /publish-python-components\.py/);
    assert.match(workflow, /--directory desktop\/dist\/python-components\//);
  }
  const mac = readFileSync(path.join(root, ".github/workflows/macos-installer.yml"), "utf8");
  assert.match(mac, /publish-macos-rag:[\s\S]*installer_available == 'true'[\s\S]*--python/);
  assert.match(mac, /--require-macos-team-id/);
});

test("Installer mirrors and RAG publication cannot block artifact download or installation tests", () => {
  for (const platform of ["macos", "windows"]) {
    const workflow = readFileSync(path.join(root, `.github/workflows/${platform}-installer.yml`), "utf8");
    const build = workflow.split(`  build-${platform}-installer:`)[1].split(`  mirror-${platform}-installer:`)[0];
    assert.doesNotMatch(build, /publish-python-components\.py|publish-installer-mirror\.py|secrets\.MODELSCOPE_API_TOKEN/);
    assert.match(build, /archive: false/);
    const testJob = workflow.split(`  test-${platform}-installer:`)[1];
    assert.match(testJob, new RegExp(`needs: build-${platform}-installer`));
    assert.doesNotMatch(testJob, /needs\.(mirror|publish)-/);
    const mirror = workflow.split(`  mirror-${platform}-installer:`)[1].split(`  publish-${platform}-rag:`)[0];
    assert.match(mirror, /host: \[ms, hf\]/);
    assert.match(mirror, /fail-fast: false/);
    assert.match(mirror, /Upload installer to Realease/);
    assert.match(mirror, /secrets\[matrix.host/);
    if (platform === "macos") assert.match(testJob, /--bundle-dir rag-under-test\/darwin-arm64/);
  }
  execFileSync(process.platform === "win32" ? "python" : "python3", [
    "-m", "unittest", "discover", "-s", "tests", "-p", "test_desktop_installer_mirrors.py", "-v",
  ], { cwd: root, stdio: "pipe" });
});

test("RAG online catalogs and publication fail-closed gates", () => {
  execFileSync(process.platform === "win32" ? "python" : "python3", [
    "-m", "unittest", "discover", "-s", "tests", "-p", "test_desktop_component_publication.py", "-v",
  ], { cwd: root, stdio: "pipe" });
});
