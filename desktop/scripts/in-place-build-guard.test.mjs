import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../", import.meta.url));
const electron = createRequire(new URL("../electron/package.json", import.meta.url));
const yaml = createRequire(electron.resolve("electron-builder/package.json"))("js-yaml");
const job = yaml.load(fs.readFileSync(path.join(root, ".github/workflows/macos-installer.yml"))).jobs.build_unsigned_update_test;
const trustedPath = ".in-place-workflow";
const guard = path.join(root, ".github/actions/check-in-place-build/check.py");
const resolver = path.join(root, "desktop/scripts/resolve-unsigned-test-round.mjs");
const publicBase = "https://modelscope.cn/datasets/test/repo/resolve/master/updates/unsigned-test/demo/";

function fixture(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "lm-dispatch-guard-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const template = { mode: "unsigned-test", audience: "personal", channel: "personal-test", signingIdentity: "adhoc",
    updaterProtocol: 2, trustDomain: "template", policyURL: publicBase + "policy.json", allowedOrigins: ["https://modelscope.cn"],
    publicKeys: { fixture: Buffer.alloc(32, 3).toString("base64") } };
  return { dir, env: { PATH: process.env.PATH, GITHUB_RUN_ID: "41", RUNNER_TEMP: dir, TEST_SOURCE_RUN: "", TEST_VERSION: "0.5.301",
    LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON: JSON.stringify(template), LAZYMIND_UPDATE_BASE_URL: publicBase } };
}

test("the selected git ref must pass the dispatch revision guards before building and uploading", t => {
  const steps = job.steps;
  const pos = pred => { const i = steps.findIndex(pred); assert.ok(i >= 0); return i; };
  const selected = pos(s => s.uses?.startsWith("actions/checkout@") && s.with.ref.includes("inputs.git_ref"));
  const locked = pos(s => s.uses?.startsWith("actions/checkout@") && s.with.path === trustedPath);
  assert.equal(steps[locked].with.ref, "${{ github.workflow_sha }}");
  const source = pos(s => s.run?.includes("gh run download"));
  const build = pos(s => s.run?.includes("bash desktop/scripts/build-unsigned-update-test.sh"));
  const upload = pos(s => s.uses?.startsWith("actions/upload-artifact@"));
  const locate = phase => pos(s => s.uses === `./${trustedPath}/.github/actions/check-in-place-build` && s.with.phase === phase);
  assert.ok(selected < locked && locked < locate("resolver") && source < locate("resolver") &&
    locate("resolver") < build && build < locate("artifact") && locate("artifact") < upload);
  for (const phase of ["resolver", "artifact"]) {
    const step = steps[locate(phase)];
    assert.equal(step.if, undefined);
    assert.equal(step["continue-on-error"], undefined);
    assert.deepEqual(step.env, steps[build].env);
  }
  assert.equal(steps[upload].if, undefined);
  const { dir, env } = fixture(t);
  // A selectable ref may carry the original round-N policy contract.
  const scripts = path.join(dir, "desktop/scripts");
  fs.mkdirSync(scripts, { recursive: true });
  fs.writeFileSync(path.join(scripts, path.basename(resolver)), `console.log(JSON.stringify({config:{policyURL:"${publicBase}round-41/policy.json"},baseURL:"${publicBase}round-41/"}))`);
  const old = spawnSync("python3", [guard, "resolver"], { cwd: dir, env, encoding: "utf8" });
  assert.notEqual(old.status, 0);
  assert.match(old.stderr, /selected git_ref/i);
  const current = spawnSync("python3", [guard, "resolver"], { cwd: root, env, encoding: "utf8" });
  assert.equal(current.status, 0, current.stderr);
});

test("the dispatch guard reads the actual ZIP and binds its release to this round", { skip: process.platform !== "darwin" || process.arch !== "arm64" }, t => {
  const { dir, env } = fixture(t);
  const resolved = spawnSync(process.execPath, [resolver, "--target-config"], { cwd: root, env, encoding: "utf8" });
  assert.equal(resolved.status, 0, resolved.stderr);
  const config = JSON.parse(resolved.stdout);
  const releaseDir = path.join(dir, "unsigned-update-test", config.releaseId);
  fs.mkdirSync(releaseDir, { recursive: true });
  const identity = Object.fromEntries(["releaseId","version","platform","arch","mode","signingIdentity"].map(k => [k, config[k]]));
  const components = { id: "rag" };
  for (const scenario of ["valid", "legacy ZIP", "foreign embedded round", "foreign record", "extra release"]) {
    const app = scenario === "legacy ZIP" ? "LazyMind Test.app" : "LazyMind.app";
    const tag = scenario === "foreign embedded round" ? "unrelated" : config.trustDomain;
    const metadata = { lazymind: { ...identity, protocol: 2 }, updaterCacheDirName: `LazyMind-${tag}-${config.audience}-${config.channel}` };
    const zip = spawnSync("python3", ["-c", [
      "import json,plistlib,sys,zipfile",
      "app,version,meta=sys.argv[1:]",
      "with zipfile.ZipFile('package.zip','w') as z:",
      " z.writestr(app+'/Contents/Info.plist',plistlib.dumps(dict(CFBundleIdentifier='ai.lazymind.desktop',CFBundleExecutable='LazyMind',CFBundleShortVersionString=version)))",
      " z.writestr(app+'/Contents/Resources/app-update.yml',meta)",
    ].join("\n"), app, config.version, JSON.stringify(metadata)], { cwd: releaseDir, encoding: "utf8" });
    assert.equal(zip.status, 0, zip.stderr);
    fs.writeFileSync(path.join(releaseDir, "feed.json"), "{}");
    fs.writeFileSync(path.join(releaseDir, "components.json"), JSON.stringify({ schemaVersion: 1, platform: config.platform, arch: config.arch, components: { rag: components } }));
    const release = { ...identity, updaterProtocol: 2 };
    for (const [key, file] of [["package","package.zip"],["feed","feed.json"],["components","components.json"]]) {
      const data = fs.readFileSync(path.join(releaseDir, file));
      release[key] = { size: data.length, sha256: createHash("sha256").update(data).digest("hex"),
        url: new URL(`releases/${config.releaseId}/${file}`, config.policyURL).href };
    }
    release.package.unpackedBytes = 4096;
    const recorded = scenario === "foreign record" ? { ...config, trustDomain: "unrelated" } : config;
    fs.writeFileSync(path.join(releaseDir, "release.json"), JSON.stringify({ config: recorded, release,
      componentDescriptor: components, formatProfile: "format-v2-" + "a".repeat(64) }));
    if (scenario === "extra release") {
      const extra = path.join(dir, "unsigned-update-test/python-components/another");
      fs.mkdirSync(extra, { recursive: true }); fs.writeFileSync(path.join(extra, "release.json"), "{}");
    }
    const result = spawnSync("python3", [guard, "artifact"], { cwd: root, env, encoding: "utf8" });
    if (scenario === "valid") assert.equal(result.status, 0, result.stderr);
    else assert.notEqual(result.status, 0, scenario);
  }
});
