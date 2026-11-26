import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
const read = name => fs.readFileSync(new URL(`../../.github/workflows/${name}.yml`, import.meta.url), "utf8").replace(/\r\n/g, "\n");

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

test("enabled Desktop Release gates each platform publication on its own verified build", () => {
  const release = read("desktop-release");
  const reusable = read("desktop-update-publish");
  const jobs = source => [...source.matchAll(/^  [a-z][\w-]*:\n([\s\S]*?)(?=^  [a-z][\w-]*:|$(?![\s\S]))/gm)].map(m => m[1]);
  const publishing = job => job.includes("uses: ./.github/workflows/desktop-update-publish.yml") && !job.includes("unsigned_update_test: true");
  assert.match(reusable, /^  workflow_call:/m);
  for (const platform of ["macos", "windows"]) {
    const direct = jobs(release).find(job => publishing(job) && new RegExp(`\\bneeds: *\\[?${platform}\\]? *$`, "m").test(job));
    if (direct) {
      assertConjunctiveGate(direct, ["vars.LAZYMIND_DESKTOP_UPDATES_ENABLED == 'true'", "vars.LAZYMIND_DESKTOP_UPDATE_PUBLICATION_ENABLED == 'true'"]);
    } else {
      const build = jobs(release).find(job => job.includes(`uses: ./.github/workflows/${platform}-installer.yml`));
      assert.match(build, /enable_updates: .*vars\.LAZYMIND_DESKTOP_UPDATES_ENABLED == 'true'/);
      const nested = jobs(read(platform + "-installer")).find(publishing);
      assert.ok(nested, `missing gated ${platform} publication`);
      assertConjunctiveGate(nested, ["inputs.enable_updates", "vars.LAZYMIND_DESKTOP_UPDATE_PUBLICATION_ENABLED == 'true'"]);
    }
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
  const taggedRelease = read("desktop-release");
  assert.doesNotMatch(taggedRelease, /enable_unsigned_update_test|desktop_updates_test/);
  const caller = taggedRelease.match(/^  macos:\n([\s\S]*?)(?=^  [a-z][a-z_-]*:|$(?![\s\S]))/m)?.[1];
  assert.ok(caller, "the macOS Installer remains callable from the tag workflow");
  assert.match(caller, /permissions:\s*\n\s*contents: read\s*\n\s*actions: read/,
    "the nested release workflow must be allowed to read action artifacts");
});

test("a legacy template at the new URL cannot resume or receive components with its old trust domain", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "lm-nested-legacy-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const repo = "fixture/updates", prefix = "updates/unsigned-test/demo/";
  const base = `https://modelscope.cn/datasets/${repo}/resolve/master/${prefix}`;
  const template = { mode: "unsigned-test", audience: "personal", channel: "personal-test", updaterProtocol: 2,
    signingIdentity: "adhoc", trustDomain: "existing", policyURL: base + "in-place/round-41/policy.json",
    publicKeys: { key: Buffer.alloc(32, 3).toString("base64") }, allowedOrigins: ["https://modelscope.cn"] };
  const script = fileURLToPath(new URL("./resolve-unsigned-test-round.mjs", import.meta.url));
  const env = { PATH: process.env.PATH, RUNNER_TEMP: dir, GITHUB_RUN_ID: "41", TEST_VERSION: "0.5.225",
    TEST_BASE_URL: base, UPDATE_REPO: repo, LAZYMIND_UPDATE_BASE_URL: base,
    LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON: JSON.stringify(template) };
  const invoke = (args, extraEnv = {}) => execFileSync(process.execPath, [script, ...args],
    { encoding: "utf8", env: { ...env, ...extraEnv } });
  const fresh = JSON.parse(invoke([]));
  assert.equal(fresh.config.policyURL, template.policyURL);
  assert.notEqual(fresh.config.trustDomain, template.trustDomain);
  for (const [config, releaseBase] of [[template, base], [fresh.config, fresh.baseURL]]) {
    const releaseId = "darwin-arm64-0.5.224";
    const built = { ...config, releaseId, version: "0.5.224", platform: "darwin", arch: "arm64" };
    const release = { releaseId, version: built.version, platform: built.platform, arch: built.arch,
      updaterProtocol: built.updaterProtocol, signingIdentity: built.signingIdentity };
    for (const [kind, file] of [["package", "package.zip"], ["components", "components.json"], ["feed", "feed.json"]]) {
      release[kind] = { url: releaseBase + `releases/${releaseId}/${file}` };
    }
    const folder = path.join(dir, "unsigned-test-source", releaseId);
    fs.mkdirSync(folder, { recursive: true });
    fs.writeFileSync(path.join(folder, "release.json"), JSON.stringify({ release, config: built }));
    const signedConfig = path.join(dir, "update-config.json");
    fs.writeFileSync(signedConfig, JSON.stringify(built));
    if (config === template) {
      assert.throws(() => invoke([], { TEST_SOURCE_RUN: "40", GITHUB_RUN_ID: "42" }));
      assert.throws(() => invoke(["--component-prefix", signedConfig]));
      continue;
    }
    assert.deepEqual(JSON.parse(invoke([], { TEST_SOURCE_RUN: "40", GITHUB_RUN_ID: "42" })), { config, baseURL: releaseBase });
    assert.equal(invoke(["--component-prefix", signedConfig]), prefix + releaseBase.slice(base.length) + "RAG");
  }
});
