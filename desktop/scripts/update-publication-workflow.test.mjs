import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
const read = name => fs.readFileSync(new URL(`../../.github/workflows/${name}.yml`, import.meta.url), "utf8");

function assertConjunctiveGate(job, expectedTerms) {
  const gate = job.match(/^    if: (.+)$/m)?.[1].trim().replace(/^\$\{\{\s*|\s*\}\}$/g, "");
  assert.ok(gate, "publication job must gate execution");
  assert.doesNotMatch(gate, /\|\|/, "publication conditions cannot be alternatives");
  const terms = new Set(gate.split(/\s*&&\s*/));
  for (const term of expectedTerms) assert.ok(terms.has(term), `publication must require ${term}`);
}

test("macOS signing selection depends on explicit mode and tag, never repository name", { skip: process.platform === "win32" }, t => {
  const source = read("macos-installer");
  const begin = source.indexOf('          case "${SIGNING_MODE}" in');
  const end = source.indexOf('\n      - name:', begin);
  assert.ok(begin > 0 && end > begin);
  const script = source.slice(begin, end).split("\n").map(line => line.replace(/^          /, "")).join("\n");
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "lm-signing-mode-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  for (const [mode, tag, expected] of [["auto", "", false], ["auto", "v1.0.0", true], ["developer-id", "", true], ["adhoc", "v1.0.0", false]]) {
    const out = path.join(root, `${mode}-${tag}.txt`);
    execFileSync("bash", ["-euc", script], { env: { ...process.env, SIGNING_MODE: mode, release_tag: tag, GITHUB_OUTPUT: out }, stdio: "pipe" });
    assert.equal(fs.readFileSync(out, "utf8").trim(), `official_release=${expected}`);
  }
  assert.throws(() => execFileSync("bash", ["-euc", script], { env: { ...process.env, SIGNING_MODE: "unknown", release_tag: "", GITHUB_OUTPUT: path.join(root, "bad") }, stdio: "pipe" }));
  assert.doesNotMatch(script, /LazyAGI|github.repository/);
});

test("update build inputs are opt-in, public and injected only into packaging", () => {
  for (const platform of ["macos", "windows"]) {
    const source = read(`${platform}-installer`);
    assert.match(source, /enable_updates:[\s\S]*type: boolean\s*\n\s*default: false/);
    assert.match(source, /LAZYMIND_UPDATE_BUILD_CONFIG_JSON: \$\{\{ inputs.enable_updates && vars\./);
    assert.match(source, /LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON: \$\{\{ env.LAZYMIND_UPDATE_BUILD_CONFIG_JSON \}\}/);
    assert.match(source, /name: Prepare immutable update artifacts\s*\n\s*if: inputs.enable_updates/);
    assert.match(source, /name: Upload immutable update artifacts\s*\n\s*if: inputs.enable_updates/);
    let buildSource = source;
    if (platform === "macos") {
      const publishJob = source.match(/^  publish_unsigned_update_test:\n[\s\S]*?(?=^  [a-z][a-z_-]*:|$(?![\s\S]))/m)?.[0];
      const wiring = publishJob?.match(/^    secrets:\n(?: {6}[^\n]+\n)+/m)?.[0];
      assert.equal(wiring, [
        "    secrets:",
        "      LAZYMIND_UPDATE_POLICY_PRIVATE_KEY: ${{ secrets.LAZYMIND_UPDATE_POLICY_PRIVATE_KEY }}",
        "      MODELSCOPE_UPDATE_TOKEN: ${{ secrets.MODELSCOPE_UPDATE_TOKEN }}",
        "",
      ].join("\n"), "the test publisher must receive only its two environment secrets by name");
      const callable = source.slice(0, source.indexOf("  workflow_dispatch:"));
      buildSource = source.replace(publishJob, publishJob.replace(wiring, ""))
        .replace(callable, callable.replace("\n      LAZYMIND_UPDATE_POLICY_PRIVATE_KEY:\n        required: false\n", "\n"));
    }
    assert.doesNotMatch(buildSource, /UPDATE_POLICY_PRIVATE_KEY|sunjh2222/);
  }
  assert.match(read("windows-installer"), /signature.Status -ne 'Valid'/);
});

test("manual publication remains serialized and scoped to the release environment", () => {
  const source = read("desktop-update-publish");
  assert.match(source, /on:\s*\n\s*workflow_dispatch:/);
  assert.doesNotMatch(source, /pull_request_target:|\n  push:|sunjh2222/);
  assert.match(source, /group: desktop-update-publication\s*\n\s*cancel-in-progress: false/);
  assert.match(source, /environment:[^\n]*\bdesktop_updates\b/);
  assert.match(source, /--json conclusion --jq .conclusion\)" = success/);
  assert.ok(source.indexOf('update-publisher" sign') < source.indexOf('python desktop/scripts/publish-desktop-update.py'));
  assert.match(source, /trap 'rm -f "\$\{key_path\}"' EXIT/);
  assert.match(source, /name: Remove signing material\s*\n\s*if: always\(\)/);
  const archive = source.slice(source.indexOf('name: Save public publication records'), source.indexOf('name: Remove signing material'));
  assert.doesNotMatch(archive, /private|pem|key_path/);
});

test("enabled Desktop Release reuses the publisher with this run's two verified build artifacts", () => {
  const release = read("desktop-release");
  const reusable = read("desktop-update-publish");
  const publishJob = release.match(/^  publish_update:\n([\s\S]*?)(?=^  [a-z][a-z_-]*:|$(?![\s\S]))/m)?.[1];
  assert.ok(publishJob, "Desktop Release needs an update publication job");
  assertConjunctiveGate(publishJob, ["vars.LAZYMIND_DESKTOP_UPDATES_ENABLED == 'true'",
    "vars.LAZYMIND_DESKTOP_UPDATE_PUBLICATION_ENABLED == 'true'"]);
  assert.match(publishJob, /needs:[\s\S]*?\bmacos\b[\s\S]*?\bwindows\b/);
  assert.match(publishJob, /uses: \.\/\.github\/workflows\/desktop-update-publish\.yml/);
  assert.match(reusable, /^  workflow_call:/m);
  for (const platform of ["macos", "windows"]) {
    assert.ok(reusable.includes(`desktop-update-${platform}-\${{ github.run_id }}`),
      `publication must select the ${platform} artifact from this run`);
  }
  assert.match(reusable, /uses: actions\/download-artifact@/);
  assert.match(reusable, /environment:[^\n]*\bdesktop_updates\b/);
});

test("the same macOS Installer exposes a separate opt-in unsigned update test", () => {
  const installer = read("macos-installer");
  const dispatch = installer.match(/^  workflow_dispatch:\n([\s\S]*?)(?=^[a-z])/m)?.[1];
  assert.ok(dispatch, "testers need the existing manual Installer entry");
  assert.match(dispatch, /enable_unsigned_update_test:\s*\n(?:\s+[a-z]+: [^\n]*\n)*\s+default: false/);
  const reusableInputs = installer.slice(installer.indexOf("  workflow_call:"), installer.indexOf("  workflow_dispatch:"));
  assert.doesNotMatch(reusableInputs, /enable_unsigned_update_test/,
    "formal release callers cannot request the unsigned test build");
  const testBuild = installer.match(/^  build_unsigned_update_test:\n([\s\S]*?)(?=^  [a-z][a-z_-]*:|$(?![\s\S]))/m)?.[1];
  assert.ok(testBuild, "the isolated test app has an independent build job");
  assert.match(testBuild, /LAZYMIND_UPDATE_MINIMUM_SYSTEM_VERSION: \$\{\{ vars\.LAZYMIND_MAC_UNSIGNED_UPDATE_TEST_MINIMUM_SYSTEM_VERSION \}\}/);
  const testPublication = installer.match(/^  publish_unsigned_update_test:\n([\s\S]*?)(?=^  [a-z][a-z_-]*:|$(?![\s\S]))/m)?.[1];
  assert.ok(testPublication, "a successful explicitly selected test build must have a publication path");
  assertConjunctiveGate(testPublication, ["github.event_name == 'workflow_dispatch'",
    "inputs.enable_unsigned_update_test", "!inputs.enable_updates"]);
  assert.match(testPublication, /uses: \.\/\.github\/workflows\/desktop-update-publish\.yml/);
  assert.match(testPublication, /unsigned_update_test: true/);
  assert.match(read("desktop-update-publish"),
    /environment: \$\{\{\s*inputs\.unsigned_update_test\s*&&\s*'desktop_updates_test'\s*\|\|\s*'desktop_updates'\s*\}\}/,
    "test calls must use the test environment, while ordinary calls use the official environment");
  const taggedRelease = read("desktop-release");
  assert.doesNotMatch(taggedRelease, /enable_unsigned_update_test|desktop_updates_test/);
  const caller = taggedRelease.match(/^  macos:\n([\s\S]*?)(?=^  [a-z][a-z_-]*:|$(?![\s\S]))/m)?.[1];
  assert.ok(caller, "the macOS Installer remains callable from the tag workflow");
  assert.match(caller, /permissions:\s*\n\s*contents: read\s*\n\s*actions: read/,
    "the nested release workflow must be allowed to read action artifacts");
});
