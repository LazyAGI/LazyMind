import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import * as build from "./prepare-update-build-config.mjs";

function fixture() {
  const configuration = { trustDomain: "fixture", audience: "official", channel: "stable", mode: "official",
    releaseId: "fixture-1", signingIdentity: "FIXTURE_PUBLISHER", updaterProtocol: 1,
    policyURL: "https://updates.example.invalid/policy.json", allowedOrigins: ["https://updates.example.invalid"],
    publicKeys: { key: Buffer.alloc(32, 1).toString("base64") } };
  const encoded = build.prepareUpdateBuildConfig({ configuration, packageVersion: "1.0.0", platform: "windows", arch: "amd64", signingMode: "authenticode" });
  return { configuration, encoded, config: JSON.parse(Buffer.from(encoded, "base64").toString()) };
}

test("all Windows packaging paths verify the staged helper before modifying the stage", () => {
  const source = readFileSync(new URL("./build-windows-x64.ps1", import.meta.url), "utf8");
  const finalize = source.slice(source.indexOf("function Finalize-Desktop("), source.indexOf("function Build-Desktop("));
  assert.match(finalize, /Assert-UpdateBuildConfiguration/);
  assert.ok(finalize.indexOf("Assert-UpdateBuildConfiguration") < finalize.indexOf("Materialize-PythonAliases"));
  const validation = source.slice(source.indexOf("function Assert-UpdateBuildConfiguration"), source.indexOf("function Finalize-Desktop("));
  assert.match(validation, /prepare-update-build-config\.mjs/);
  assert.match(validation, /--verify-helper/);
  for (const action of ["resume", "resume-installer"]) assert.match(source, new RegExp(`'${action}' \\{ Invoke-Doctor; Finalize-Desktop`));
});

test("staged helper must match current public configuration, not just the version", () => {
  const { encoded, config } = fixture();
  assert.equal(typeof build.verifyStagedUpdateConfig, "function");
  // JSON key order differs between Go's typed config and the original input.
  assert.doesNotThrow(() => build.verifyStagedUpdateConfig(encoded, { status: "enabled", config: Object.fromEntries(Object.entries(config).reverse()) }));
  for (const delta of [{ version: "0.9.0" }, { releaseId: "old" }, { policyURL: "https://old.example.invalid/policy.json" },
    { publicKeys: { old: Buffer.alloc(32, 2).toString("base64") } }, { audience: "personal-test", mode: "unsigned-test" }, { arch: "arm64" }]) {
    assert.throws(() => build.verifyStagedUpdateConfig(encoded, { status: "enabled", config: { ...config, ...delta } }), /staged.*mismatch/i);
  }
  assert.doesNotThrow(() => build.verifyStagedUpdateConfig("", { status: "disabled", reason: "not_configured" }));
  assert.throws(() => build.verifyStagedUpdateConfig(encoded, { status: "disabled", reason: "not_configured" }), /mismatch/i);
  assert.throws(() => build.verifyStagedUpdateConfig("", { status: "enabled", config }), /mismatch/i);
  assert.throws(() => build.verifyStagedUpdateConfig("", {}), /mismatch/i);
});

test("verification CLI re-evaluates signing/version and executes only helper config", (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lm-update-resume-")); t.after(() => rmSync(root, { recursive: true, force: true }));
  const { configuration, encoded } = fixture();
  const publicPath = path.join(root, "public.json"), packagePath = path.join(root, "package.json"), helper = path.join(root, process.platform === "win32" ? "fixture-helper.exe" : "fixture-helper");
  writeFileSync(publicPath, JSON.stringify(configuration)); writeFileSync(packagePath, JSON.stringify({ version: "1.0.0" }));
  execFileSync("go", ["build", "-mod=readonly", `-ldflags=-X main.embeddedConfig=${encoded}`, "-o", helper, "./cmd/update-helper"], {
    cwd: new URL("../../local/local-runtime-manager/", import.meta.url),
    env: { ...process.env, GOPROXY: "off", GOSUMDB: "off", GOCACHE: path.join(os.tmpdir(), "lazymind-update-gocache") }, timeout: 60000,
  });
  const args = [new URL("./prepare-update-build-config.mjs", import.meta.url).pathname, "--platform", "windows", "--arch", "amd64", "--signing-mode", "authenticode", "--package", packagePath, "--verify-helper", helper];
  const options = { encoding: "utf8", env: { ...process.env, LAZYMIND_DESKTOP_UPDATE_CONFIG: publicPath }, stdio: ["ignore", "pipe", "pipe"] };
  assert.equal(execFileSync(process.execPath, args, options).trim(), "");
  writeFileSync(packagePath, JSON.stringify({ version: "2.0.0" }));
  assert.throws(() => execFileSync(process.execPath, args, options), /staged.*mismatch/i);
  writeFileSync(packagePath, JSON.stringify({ version: "1.0.0" }));
  const unsigned = [...args]; unsigned[unsigned.indexOf("authenticode")] = "none";
  assert.throws(() => execFileSync(process.execPath, unsigned, options), /require authenticode/);
  rmSync(helper);
  assert.throws(() => execFileSync(process.execPath, args, options), /staged.*helper/i);
});
