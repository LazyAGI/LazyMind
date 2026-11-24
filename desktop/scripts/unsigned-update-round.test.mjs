import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { prepareUpdateBuildConfig } from "./prepare-update-build-config.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
const electronRequire = createRequire(new URL("../electron/package.json", import.meta.url));
const yaml = createRequire(electronRequire.resolve("electron-builder/package.json"))("js-yaml");
const workflow = name => yaml.load(readFileSync(path.join(root, ".github/workflows", name + ".yml"), "utf8"));
const installer = workflow("macos-installer");
const build = installer.jobs.build_unsigned_update_test;
const publisher = workflow("desktop-update-publish").jobs.publish;
const dataset = "fixture/updates";
const base = `https://modelscope.cn/datasets/${dataset}/resolve/master/updates/unsigned-test/demo/`;
const template = {
  trustDomain: "desktop-test", audience: "personal", channel: "personal-test", mode: "unsigned-test",
  updaterProtocol: 2, signingIdentity: "adhoc", policyURL: base + "policy.json",
  allowedOrigins: ["https://modelscope.cn", "https://cdn-lfs-cn-1.modelscope.cn"],
  publicKeys: { release: Buffer.alloc(32, 3).toString("base64") },
};
const variables = {
  LAZYMIND_MAC_UNSIGNED_UPDATE_TEST_CONFIG_JSON: JSON.stringify(template),
  LAZYMIND_UNSIGNED_UPDATE_TEST_BASE_URL: base,
  LAZYMIND_UPDATE_MODELSCOPE_REPO: dataset,
  LAZYMIND_UPDATE_BASE_URL: "https://modelscope.cn/datasets/fixture/updates/resolve/master/updates/official/",
};

// Execute the checked-in shell with the step's actual input bindings. Only
// the expensive native builder and network upload at its boundary are captured.
function runStep(step, context, dir, capture) {
  const env = { PATH: process.env.PATH, RUNNER_TEMP: dir, GITHUB_RUN_ID: context.github.run_id,
    GITHUB_STEP_SUMMARY: path.join(dir, "summary"), LAZYMIND_UPDATE_MINIMUM_SYSTEM_VERSION: "13.0.0" };
  for (const [name, binding] of Object.entries(step.env || {})) {
    const match = /^\$\{\{\s*(\w+)\.(\w+)\s*\}\}$/.exec(binding);
    assert.ok(match && Object.hasOwn(context[match[1]] || {}, match[2]), `Unbound step input ${name}: ${binding}`);
    env[name] = String(context[match[1]][match[2]]);
  }
  return spawnSync("bash", ["-euc", capture + "\n" + step.run], { cwd: root, env, encoding: "utf8" });
}

// This job uses direct input checks. Fail loudly if a future gating expression
// needs different semantics; never infer whether to run a step from the fixture.
function stepEnabled(condition, inputs) {
  if (condition === undefined) return true;
  const expression = condition.replace(/^\$\{\{\s*|\s*\}\}$/g, "");
  return expression.split(/\s*\|\|\s*/).some(branch => branch.split(/\s*&&\s*/).every(term => {
    const m = /^inputs\.(\w+)(?:\s*(==|!=)\s*'([^']*)')?$/.exec(term.trim());
    assert.ok(m && Object.hasOwn(inputs, m[1]), `Unsupported step condition: ${condition}`);
    if (!m[2]) return Boolean(inputs[m[1]]);
    return m[2] === "==" ? inputs[m[1]] === m[3] : inputs[m[1]] !== m[3];
  }));
}
function temporary(t) {
  const dir = mkdtempSync(path.join(os.tmpdir(), "lm-update-round-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}
function writeRecord(dir, record) {
  const folder = path.join(dir, record.release.releaseId);
  mkdirSync(folder, { recursive: true });
  writeFileSync(path.join(folder, "release.json"), JSON.stringify(record));
}
function sourceRecord(configuration, version) {
  const bytes = prepareUpdateBuildConfig({ configuration, packageVersion: version, platform: "darwin",
    arch: "arm64", signingMode: "adhoc", isolatedTestBuild: true });
  const config = JSON.parse(Buffer.from(bytes, "base64").toString());
  const { releaseId, platform, arch, signingIdentity, updaterProtocol } = config;
  const prefix = new URL(`releases/${releaseId}/`, config.policyURL);
  const artifact = name => ({ url: new URL(name, prefix).href, sha256: "a".repeat(64), size: 128 });
  const tag = "a".repeat(64), componentBase = new URL(`RAG/tags/${tag}/`, config.policyURL);
  const componentDescriptor = { id: "rag", platform, arch, tag, revision: tag, sha256: tag, pythonAbi: "cp311",
    baseFingerprint: "b".repeat(64), packages: { "milvus-lite": "3.0.0" }, filename: "rag-adhoc.zip",
    sizeBytes: 128, unpackedBytes: 512, url: new URL("rag-adhoc.zip", componentBase).href,
    catalogUrl: new URL("python-components.json", componentBase).href };
  return { config, componentDescriptor, formatProfile: "format-v2-" + tag, manifestSHA256: tag,
    release: { releaseId, version, platform, arch, signingIdentity, updaterProtocol,
    minUpdaterProtocol: 2, minimumSystemVersion: "13.0.0", package: { ...artifact("package.zip"), unpackedBytes: 512 },
    feed: artifact("feed.json"), components: artifact("components.json") } };
}
function runBuild(t, mode, run, version, sourceID = "", record, conclusion = "success") {
  const dir = temporary(t);
  if (record) writeRecord(path.join(dir, "fixture-artifact"), record);
  writeFileSync(path.join(dir, "source-id"), sourceID);
  writeFileSync(path.join(dir, "conclusion"), conclusion);
  const context = { inputs: { enable_updates: false, test_version: version, source_build_run_id: sourceID, test_round: mode },
    vars: variables, github: { run_id: run, repository: "fixture/LazyMind", token: "fixture-token" } };
  const captured = path.join(dir, "native-build.json");
  const gh = `gh() {
    touch "$RUNNER_TEMP/queried-baseline"
    [[ "$1" == run && "$3" == "$(cat "$RUNNER_TEMP/source-id")" && -n "$3" ]] || return 91
    case "$2" in
      view) cat "$RUNNER_TEMP/conclusion" ;;
      download)
        local name="" dest="" id="$3"
        shift 3
        while [[ $# -gt 0 ]]; do
          case "$1" in --name|-n) name="$2";; --dir|-D) dest="$2";; *) return 92;; esac
          shift 2
        done
        [[ "$name" == "desktop-update-macos-$id" && "$dest" == "$RUNNER_TEMP/"* && -d "$RUNNER_TEMP/fixture-artifact" ]] || return 93
        mkdir -p "$dest"
        cp -R "$RUNNER_TEMP/fixture-artifact/." "$dest/"
        touch "$RUNNER_TEMP/downloaded-baseline" ;;
      *) return 94 ;;
    esac
  }`;
  const boundaries = gh + `\nbash() {
    [[ "$1" == desktop/scripts/build-unsigned-update-test.sh ]] || return 90
    node -e 'const e = process.env; require("fs").writeFileSync(e.RUNNER_TEMP + "/native-build.json",
      JSON.stringify({ version: process.argv[1], config: JSON.parse(e.LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON),
        sourceDownloadedAtBuild: require("fs").existsSync(e.RUNNER_TEMP + "/downloaded-baseline"),
        baseURL: e.LAZYMIND_UPDATE_BASE_URL, componentURL: e.LAZYMIND_PYTHON_COMPONENT_BASE_URL }))' "$2"
  }`;
  let call;
  for (const step of build.steps) {
    // Checkout, tool setup, and artifact upload are external to these shells.
    if (!step.run || step.run.includes("corepack prepare")) continue;
    if (!stepEnabled(step.if, context.inputs)) continue;
    call = runStep(step, context, dir, boundaries);
    if (call.status !== 0) break;
  }
  return { call, captured, read() {
    assert.equal(call.status, 0, call.stderr);
    const result = JSON.parse(readFileSync(captured));
    assert.equal(result.sourceDownloadedAtBuild, mode === "continue", "the continued build must receive its selected baseline before packaging");
    assert.equal(existsSync(path.join(dir, "queried-baseline")), mode === "continue", "new rounds must not query a baseline");
    return result;
  } };
}
const bashOnly = { skip: process.platform === "win32" };

test("manual installer selects new or continued test rounds and leaves the ordinary installer runnable", () => {
  const choice = installer.on.workflow_dispatch.inputs.test_round;
  assert.equal(choice?.type, "choice");
  assert.deepEqual([...choice.options].sort(), ["continue", "new"]);
  assert.equal(installer.on.workflow_dispatch.inputs.enable_unsigned_update_test.default, false);
  assert.equal(installer.jobs["build-macos-installer"].if, "!inputs.enable_unsigned_update_test");
  assert.equal(installer.on.workflow_call.inputs.test_round, undefined);
});

test("two new rounds can reuse an app version without reusing policy, trust or RAG addresses", bashOnly, t => {
  const one = runBuild(t, "new", "45348758446", "0.5.223").read();
  const two = runBuild(t, "new", "45348758447", "0.5.223").read();
  for (const [built, run] of [[one, "45348758446"], [two, "45348758447"]]) {
    assert.equal(built.baseURL, base + `round-${run}/`);
    assert.equal(built.config.policyURL, built.baseURL + "policy.json");
    assert.equal(built.componentURL, built.baseURL + "RAG/");
    assert.match(built.config.trustDomain, /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/);
    assert.notEqual(built.config.trustDomain, template.trustDomain);
    assert.deepEqual(built.config, { ...template, policyURL: built.config.policyURL, trustDomain: built.config.trustDomain });
    assert.equal(built.version, "0.5.223");
  }
  assert.notEqual(one.config.trustDomain, two.config.trustDomain);
});

test("continuing from B keeps A's public identity through C", bashOnly, t => {
  const a = runBuild(t, "new", "41", "1.1.0").read();
  const b = runBuild(t, "continue", "42", "1.2.0", "41", sourceRecord(a.config, a.version)).read();
  const c = runBuild(t, "continue", "43", "1.3.0", "42", sourceRecord(b.config, b.version)).read();
  for (const result of [b, c]) {
    assert.equal(result.baseURL, base + "round-41/");
    assert.deepEqual(result.config, a.config);
    assert.equal(result.componentURL, result.baseURL + "RAG/");
  }
});

test("a published legacy baseline continues at its existing policy URL", bashOnly, t => {
  const b = runBuild(t, "continue", "42", "1.2.0", "41", sourceRecord(template, "1.1.0")).read();
  assert.deepEqual(b.config, template);
  assert.equal(b.baseURL, base);
});

const valid = sourceRecord(template, "1.1.0");
const foreign = sourceRecord({ ...template, policyURL: template.policyURL.replace("/fixture/", "/other/") }, "1.1.0");
const sibling = sourceRecord({ ...template, policyURL: template.policyURL.replace("/demo/", "/demo-other/round-41/") }, "1.1.0");
const official = { ...valid, config: { ...valid.config, audience: "official", mode: "official" } };
for (const [name, mode, sourceID, record, conclusion] of [
  ["new with a source", "new", "41", valid], ["continue without an ID", "continue", ""],
  ["missing source artifact", "continue", "41"],
  ["failed source run with no authenticated published baseline", "continue", "41", valid, "failure"],
  ["different dataset", "continue", "41", foreign], ["sibling prefix in the same dataset", "continue", "41", sibling],
  ["official identity", "continue", "41", official],
]) {
  test(`reject ${name} before starting the native build`, bashOnly, t => {
    const { call, captured } = runBuild(t, mode, "42", "1.2.0", sourceID, record, conclusion);
    assert.notEqual(call.status, 0, `native build accepted ${name}`);
    assert.equal(existsSync(captured), false);
  });
}

test("a baseline cannot pair one round's policy URL with another round's trust domain", bashOnly, t => {
  const other = runBuild(t, "new", "42", "1.1.0").read();
  const mixed = sourceRecord({ ...other.config, policyURL: base + "round-41/policy.json" }, "1.1.0");
  const { call, captured } = runBuild(t, "continue", "43", "1.2.0", "41", mixed);
  assert.notEqual(call.status, 0, "accepted a baseline from two different rounds");
  assert.equal(existsSync(captured), false);
});

test("the unsigned component upload uses the signed build's prefix alongside its policy", bashOnly, t => {
  const dir = temporary(t);
  const config = { ...template, policyURL: base + "round-41/policy.json", trustDomain: "desktop-test-round-41" };
  writeFileSync(path.join(dir, "update-config.json"), JSON.stringify(sourceRecord(config, "1.1.0").config));
  const step = publisher.steps.find(item => item.run?.includes("desktop/scripts/publish-python-components.py"));
  const call = runStep(step, { github: { run_id: "42" }, vars: variables, secrets: { MODELSCOPE_UPDATE_TOKEN: "fixture" } }, dir,
    `uv() { printf '%s\\n' "$@" > "$RUNNER_TEMP/upload.args"; }`);
  assert.equal(call.status, 0, call.stderr);
  const args = readFileSync(path.join(dir, "upload.args"), "utf8").trimEnd().split("\n");
  assert.ok(args.includes("desktop/scripts/publish-python-components.py"));
  assert.equal(args[args.indexOf("--prefix") + 1], "updates/unsigned-test/demo/round-41/RAG");
});
