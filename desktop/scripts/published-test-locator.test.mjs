import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../", import.meta.url));
const electron = createRequire(new URL("../electron/package.json", import.meta.url));
const yaml = createRequire(electron.resolve("electron-builder/package.json"))("js-yaml");
const workflow = yaml.load(fs.readFileSync(path.join(root, ".github/workflows/macos-installer.yml"), "utf8"));
const steps = workflow.jobs.build_unsigned_update_test.steps;
const base = "https://modelscope.cn/datasets/fixture/repo/resolve/master/updates/unsigned-test/demo/";
const template = { mode: "unsigned-test", audience: "fixture", trustDomain: "legacy", updaterProtocol: 2,
  signingIdentity: "adhoc", channel: "personal-test", policyURL: base + "policy.json",
  allowedOrigins: ["https://modelscope.cn"], publicKeys: { fixture: Buffer.alloc(32, 4).toString("base64") } };

for (const [locator, policy] of [["published:legacy", base + "policy.json"], ["published:41", base + "round-41/policy.json"]]) {
  test(`${locator} uses the configured keys and requires verification before building`, { skip: process.platform === "win32" }, t => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "lm-public-round-"));
    t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
    const env = { PATH: process.env.PATH, GITHUB_RUN_ID: "42", RUNNER_TEMP: dir, GITHUB_STEP_SUMMARY: path.join(dir, "summary"),
      TEST_SOURCE_RUN: locator, TEST_ROUND: "continue", TEST_VERSION: "0.5.225", TEST_BASE_URL: base,
      ENABLE_OFFICIAL_UPDATES: "false", OFFICIAL_BASE_URL: base + "other", LAZYMIND_UPDATE_MINIMUM_SYSTEM_VERSION: "13.0.0",
      LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON: JSON.stringify(template), LAZYMIND_UPDATE_BASE_URL: base };
    const run = (step, mocks, extra = {}) => spawnSync("bash", ["-euc", mocks + "\n" + step.run],
      { cwd: root, env: { ...env, ...extra }, encoding: "utf8" });
    assert.equal(run(steps.find(s => s.name === "Validate test build inputs"), "").status, 0);
    const lookup = steps.find(s => s.run?.includes("gh run download"));
    assert.equal(run(lookup, 'gh() { touch "$RUNNER_TEMP/queried-gh"; return 1; }').status, 0);
    assert.equal(fs.existsSync(path.join(dir, "queried-gh")), false);
    const build = steps.find(s => s.run?.includes("desktop/scripts/build-unsigned-update-test.sh"));
    const external = `go() { :; }
      uv() {
        [[ " $* " == *" desktop/scripts/verify-published-test-baseline.py "* ]] || return 94
        while [[ "$1" != --target-config ]]; do shift; done
        cp "$2" "$RUNNER_TEMP/verified.json"
        return "$VERIFY_EXIT"
      }
      bash() {
        [[ "$1" == desktop/scripts/build-unsigned-update-test.sh && -f "$RUNNER_TEMP/verified.json" ]] || return 95
        printf '%s' "$LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON" > "$RUNNER_TEMP/built.json"
      }`;
    const rejected = run(build, external, { VERIFY_EXIT: "1" });
    assert.notEqual(rejected.status, 0);
    assert.equal(fs.existsSync(path.join(dir, "built.json")), false);
    fs.unlinkSync(path.join(dir, "verified.json"));
    const accepted = run(build, external, { VERIFY_EXIT: "0" });
    assert.equal(accepted.status, 0, accepted.stderr);
    const verified = JSON.parse(fs.readFileSync(path.join(dir, "verified.json")));
    const built = JSON.parse(fs.readFileSync(path.join(dir, "built.json")));
    assert.equal(verified.policyURL, policy);
    assert.deepEqual(verified.publicKeys, template.publicKeys);
    assert.deepEqual(verified, { ...built, version: "0.5.225", releaseId: "darwin-arm64-0.5.225", platform: "darwin", arch: "arm64" });
    assert.equal(built.trustDomain === template.trustDomain, locator === "published:legacy");
  });
}
