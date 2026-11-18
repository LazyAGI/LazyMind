import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
const read = name => fs.readFileSync(new URL(`../../.github/workflows/${name}.yml`, import.meta.url), "utf8");

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
    assert.doesNotMatch(source, /UPDATE_POLICY_PRIVATE_KEY|sunjh2222/);
  }
  assert.match(read("windows-installer"), /signature.Status -ne 'Valid'/);
});

test("publication is a separate manually triggered serialized job with key cleanup", () => {
  const source = read("desktop-update-publish");
  assert.match(source, /on:\s*\n\s*workflow_dispatch:/);
  assert.doesNotMatch(source, /pull_request_target:|\n  push:|sunjh2222/);
  assert.match(source, /group: desktop-update-publication\s*\n\s*cancel-in-progress: false/);
  assert.match(source, /environment: desktop_updates/);
  assert.match(source, /--json conclusion --jq .conclusion\)" = success/);
  assert.ok(source.indexOf('update-publisher" sign') < source.indexOf('python desktop/scripts/publish-desktop-update.py'));
  assert.match(source, /trap 'rm -f "\$\{key_path\}"' EXIT/);
  assert.match(source, /name: Remove signing material\s*\n\s*if: always\(\)/);
  const archive = source.slice(source.indexOf('name: Save public publication records'), source.indexOf('name: Remove signing material'));
  assert.doesNotMatch(archive, /private|pem|key_path/);
});
