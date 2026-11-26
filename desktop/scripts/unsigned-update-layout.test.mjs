import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../", import.meta.url));
const require = createRequire(import.meta.url);
const mac = { skip: process.platform !== "darwin" };
const base = "https://modelscope.cn/datasets/fixture/repo/resolve/master/updates/unsigned-test/demo/in-place/round-41/";
const config = { mode: "unsigned-test", audience: "personal", channel: "personal-test", signingIdentity: "adhoc",
  updaterProtocol: 2, trustDomain: "inplace-41-" + "a".repeat(64), policyURL: base + "policy.json",
  allowedOrigins: ["https://modelscope.cn"], publicKeys: { fixture: Buffer.alloc(32, 3).toString("base64") } };
function stage(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), ".lazymind-unsigned-v2."));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  for (const file of ["desktop/electron/electron-builder.config.cjs", "desktop/scripts/native-update-metadata.cjs",
    "desktop/scripts/unsigned-update-build.cjs", "desktop/scripts/prepare-update-build-config.mjs",
    "local/local-runtime-manager/update-format-v2.json"]) {
    fs.mkdirSync(path.dirname(path.join(dir, file)), { recursive: true });
    fs.copyFileSync(path.join(root, file), path.join(dir, file));
  }
  fs.writeFileSync(path.join(dir, "desktop/electron/package.json"), '{"version":"0.5.301"}');
  fs.writeFileSync(path.join(dir, "desktop/scripts/.unsigned-update-test.local.json"), JSON.stringify(config));
  return dir;
}

test("in-place electron-builder settings retain the ad-hoc installer and emit metadata into the regular app", mac, async t => {
  const dir = stage(t);
  const priorStage = process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE;
  process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE = dir;
  t.after(() => {
    if (priorStage === undefined) delete process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE;
    else process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE = priorStage;
  });
  const regular = require(path.join(root, "desktop/electron/electron-builder.config.cjs"));
  const build = require(path.join(dir, "desktop/electron/electron-builder.config.cjs"));
  assert.equal(regular.productName, "LazyMind");
  assert.equal(regular.appId, "ai.lazymind.desktop");
  assert.notEqual(regular.extraMetadata?.lazymindUnsignedTest, true);
  assert.equal(build.productName, regular.productName);
  assert.equal(build.appId, regular.appId);
  assert.equal(build.extraMetadata.lazymindUnsignedTest, true);
  const { writeNativeUpdateMetadata } = require(path.join(dir, "desktop/scripts/native-update-metadata.cjs"));
  const dest = await writeNativeUpdateMetadata({ appOutDir: dir, arch: 3, electronPlatformName: "darwin",
    packager: { appInfo: { productFilename: build.productName, version: "0.5.301" } } }, { signingMode: "adhoc" });
  assert.equal(dest, path.join(dir, "LazyMind.app/Contents/Resources/app-update.yml"));
  const metadata = JSON.parse(fs.readFileSync(dest));
  assert.equal(metadata.lazymind.mode, "unsigned-test");
  assert.equal(metadata.lazymind.signingIdentity, "adhoc");
  assert.ok(metadata.updaterCacheDirName.includes(config.trustDomain));
});

function section(text, start, end) {
  const begin = text.indexOf(start);
  const finish = text.indexOf(end, begin + start.length);
  assert.ok(begin >= 0 && finish > begin, `native build section ${start} through ${end}`);
  return text.slice(begin, finish);
}

function runMacBuild(t, unsigned) {
  const dir = stage(t);
  if (!unsigned) fs.unlinkSync(path.join(dir, "desktop/scripts/.unsigned-update-test.local.json"));
  const native = fs.readFileSync(path.join(root, "desktop/scripts/build-darwin-arm64.sh"), "utf8");
  const wrapper = fs.readFileSync(path.join(root, "desktop/scripts/build-unsigned-update-test.sh"), "utf8");
  const flags = section(native, "GO_BUILD_FLAGS=(", ': "${ELECTRON_CACHE');
  const compile = section(native, "UPDATE_BUILD_CONFIG=", 'GOBIN="${RUNTIME_ROOT}/bin"');
  // Run the actual conditional compile commands. Only the compiler is a stand-in.
  for (const [, project] of compile.matchAll(/cd "\$\{ROOT\}\/([^"]+)"/g)) {
    fs.mkdirSync(path.join(dir, project), { recursive: true });
  }
  const output = path.join(dir, "runtime");
  fs.mkdirSync(path.join(output, "bin"), { recursive: true });
  const capture = `record_go() {
    node -e 'const fs=require("node:fs"),args=process.argv.slice(1);fs.appendFileSync(process.env.LOG,JSON.stringify(args)+"\\n");fs.writeFileSync(args[args.indexOf("-o")+1],"#!/bin/sh\\nexit 0\\n",{mode:0o755})' -- "$@"
  }`;
  const commands = [capture, flags, compile, 'printf "%s" "$APP_NAME" > "$ROOT/app-name"'];
  if (unsigned) {
    commands.push('node() { printf "%s\\n" "$@" > "$ROOT/stage-args"; }',
      section(wrapper, 'node "${stage}/desktop/scripts/prepare-update-release.mjs"', 'cp -R "${dist}/python-components'));
  }
  const result = spawnSync("/bin/bash", ["-euc", commands.join("\n")], {
    encoding: "utf8", cwd: dir, env: { HOME: dir, PATH: process.env.PATH, ROOT: dir, GO_BIN: "record_go", GO_ARCH: "arm64", SIGNING_MODE: "adhoc",
      RELEASE_BUILD: "false", PACKAGE_KIND: "zip", RUNTIME_ROOT: output, DESKTOP_LAUNCHER: path.join(output, "launcher"),
      LOG: path.join(dir, "go-calls"), stage: dir, dist: path.join(dir, "dist"), target: "arm64", app_directory: "mac-arm64",
      LAZYMIND_UPDATE_BASE_URL: base, LAZYMIND_UPDATE_MINIMUM_SYSTEM_VERSION: "13.0.0" } });
  assert.equal(result.status, 0, result.stderr);
  const calls = fs.readFileSync(path.join(dir, "go-calls"), "utf8").trim().split("\n").map(s => JSON.parse(s));
  const binaries = new Map(calls.map(args => [path.basename(args[args.indexOf("-o") + 1]), args.join(" ")]));
  return { dir, binaries };
}

test("an ordinary macOS build with the same product name omits the test installer and build tag", mac, t => {
  const { dir, binaries } = runMacBuild(t, false);
  assert.equal(fs.readFileSync(path.join(dir, "app-name"), "utf8"), "LazyMind");
  assert.equal(binaries.has("unsigned-update-helper"), false);
  for (const binary of ["launcher", "local-runtime-manager", "lazymind-update-helper"]) {
    assert.ok(binaries.has(binary), `missing executable ${binary}`);
    assert.doesNotMatch(binaries.get(binary), /lazymind_unsigned_test/);
  }
});

test("the macOS build links Core, manager and test installer for the same transaction root and stages LazyMind.app", mac, t => {
  const { dir, binaries } = runMacBuild(t, true);
  for (const binary of ["launcher", "local-runtime-manager", "lazymind-update-helper", "unsigned-update-helper"]) {
    assert.ok(binaries.has(binary), `missing executable ${binary}`);
    assert.match(binaries.get(binary), /-tags[= ]lazymind_unsigned_test/);
  }
  const override = binaries.get("core").match(/componentStateNamespace=([^ ]+)/);
  assert.ok(!override || override[1] === "LazyMindUpdater", "Core must observe the same native transaction as the runtime manager");
  assert.equal(fs.readFileSync(path.join(dir, "app-name"), "utf8"), "LazyMind");
  const args = fs.readFileSync(path.join(dir, "stage-args"), "utf8").trim().split("\n");
  assert.equal(args[args.indexOf("--resources") + 1], path.join(dir, "dist/mac-arm64/LazyMind.app/Contents/Resources"));
});

test("native archive production and compatibility evidence use the in-place app", mac, () => {
  const call = spawnSync("python3", [path.join(root, "desktop/scripts/test_unsigned_update_layout.py"), "-v"], { encoding: "utf8" });
  assert.equal(call.status, 0, call.stderr);
});
