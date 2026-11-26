import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import util from "node:util";
import vm from "node:vm";
import test from "node:test";
import { fileURLToPath } from "node:url";

const configPath = new URL("../electron/electron-builder.config.cjs", import.meta.url);
const require = createRequire(configPath);
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex");

function builderFixture(t, mode = "adhoc") {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "lm-update-signing-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const appOutDir = path.join(root, "output");
  const app = path.join(appOutDir, "LazyMind.app");
  const runtime = path.join(app, "Contents", "Resources", "runtime");
  const bin = path.join(runtime, "bin");
  fs.mkdirSync(bin, { recursive: true });
  fs.mkdirSync(path.join(app, "Contents", "Frameworks"), { recursive: true });
  fs.mkdirSync(path.join(app, "Contents", "MacOS"), { recursive: true });
  fs.writeFileSync(path.join(app, "Contents", "MacOS", "LazyMind"), "fixture Electron");
  const launcher = path.join(root, "launcher");
  fs.writeFileSync(launcher, "fixture launcher");
  const checksums = {};
  for (const name of ["lark-cli", "feishu-credential-helper", "lazymind-core"]) {
    const bytes = Buffer.concat([Buffer.from([0xfe, 0xed, 0xfa, 0xcf]), Buffer.from(name)]);
    fs.writeFileSync(path.join(bin, name), bytes);
    if (name !== "lazymind-core") {
      const pin = `${sha(bytes)}\n`;
      fs.writeFileSync(path.join(bin, `${name}.sha256`), pin);
      checksums[`bin/${name}.sha256`] = sha(pin);
    }
    checksums[`bin/${name}`] = sha(bytes);
  }
  fs.writeFileSync(path.join(runtime, "unchanged-resource"), "fixture resource");
  checksums["unchanged-resource"] = sha("fixture resource");
  const features = { recording: true, fixtureRevision: "preserve-me" };
  fs.writeFileSync(path.join(runtime, "manifest.json"), JSON.stringify({ version: 1, features, checksums }));
  const helperBuilds = [];
  const commands = [];
  const sealedRuntime = [];
  const execFile = () => {};
  execFile[util.promisify.custom] = async (command, args) => {
    commands.push({ command, args });
    if (command === "/usr/bin/security") {
      return { stdout: `1) ${"A".repeat(40)} "Developer ID Application: Fixture"`, stderr: "" };
    }
    if (command === "/usr/bin/codesign" && args.includes("--sign")) {
      const target = args.at(-1);
      if (fs.statSync(target).isFile()) fs.appendFileSync(target, "-signed-fixture");
      if (target === app && fs.existsSync(runtime)) {
        sealedRuntime.push({
          manifest: JSON.parse(fs.readFileSync(path.join(runtime, "manifest.json"), "utf8")),
          actual: Object.fromEntries(Object.keys(checksums).map((name) => [name, sha(fs.readFileSync(path.join(runtime, name)))])),
        });
      }
    }
    return { stdout: args.includes("-c") ? "arm64\n" : "", stderr: "" };
  };
  const module = { exports: {} };
  vm.runInNewContext(fs.readFileSync(configPath, "utf8"), {
    module,
    __dirname: path.dirname(fileURLToPath(configPath)),
    require: (name) => {
      if (name === "node:child_process") return { execFile };
      if (name === "../scripts/verify-macos-privacy-identity.cjs") {
        const { verifyMacosPrivacyIdentity } = require(name);
        return { verifyMacosPrivacyIdentity: (target, options) => verifyMacosPrivacyIdentity(target, {
          ...options,
          run: (args) => {
            commands.push({ command: "/usr/bin/codesign", args });
            const identifier = args.at(-1).endsWith("LazyMind Recorder.app") ? "ai.lazymind.recorder" : "ai.lazymind.desktop";
            if (args.includes("--requirements")) return `designated => identifier "${identifier}" and anchor apple generic and certificate leaf[subject.OU] = "ABCDEFGHIJ"`;
            if (args.includes("--display")) return `Identifier=${identifier}\nTeamIdentifier=ABCDEFGHIJ\nAuthority=Developer ID Application: Fixture`;
            return "";
          },
        }) };
      }
      if (name === "../scripts/build-recording-helper.cjs") {
        return { buildRecordingHelper: (options) => helperBuilds.push(options) };
      }
      return require(name);
    },
    process: { platform: "darwin", arch: "arm64", env: {
      LAZYMIND_DESKTOP_RUNTIME_STAGE: runtime,
      LAZYMIND_DESKTOP_MAC_LAUNCHER: launcher,
      LAZYMIND_DESKTOP_SIGNING_MODE: mode,
      LAZYMIND_DESKTOP_DEFER_PYTHON: "false",
    } },
    Buffer, setTimeout,
    console: { log() {}, warn() {} },
  });
  return { config: module.exports, bin, runtime, app, features, checksums, helperBuilds, commands, sealedRuntime,
    context: { electronPlatformName: "darwin", arch: 3, appOutDir,
      packager: { appInfo: { productFilename: "LazyMind" }, codeSigningInfo: { value: Promise.resolve({}) } } } };
}

test("Intel target builds an Intel recording helper using the builder architecture enum", async (t) => {
  const f = builderFixture(t);
  await f.config.beforePack({ ...f.context, arch: 1 });
  assert.equal(f.helperBuilds[0].arch, "x64");
});

for (const mode of ["adhoc", "developer-id"]) {
  test(`${mode} package pins the final signed CLI bytes before sealing the outer app`, async (t) => {
    const f = builderFixture(t, mode);
    await f.config.afterPack(f.context);
    if (mode === "developer-id") await f.config.afterSign(f.context);
    const manifest = JSON.parse(fs.readFileSync(path.join(f.runtime, "manifest.json"), "utf8"));
    for (const name of ["lark-cli", "feishu-credential-helper"]) {
      const bytes = fs.readFileSync(path.join(f.bin, name));
      assert.match(bytes.toString(), /-signed-fixture$/);
      assert.equal(fs.readFileSync(path.join(f.bin, `${name}.sha256`), "utf8").trim(), sha(bytes), name);
      assert.equal(manifest.checksums[`bin/${name}`], sha(bytes), `manifest ${name}`);
    }
    if (mode === "developer-id") {
      const identities = f.commands.filter(({ args }) => args.includes("--test-requirement"));
      assert.equal(identities.length, 2, "both desktop and recorder privacy identities are checked");
      assert.deepEqual(identities.map(({ args }) => args.at(-1)).sort(),
        [f.app, path.join(f.app, "Contents/Resources/recording-helper/LazyMind Recorder.app")].sort());
    }
    assert.equal(manifest.version, 1);
    assert.deepEqual(manifest.features, f.features);
    assert.deepEqual(Object.keys(manifest.checksums).sort(), Object.keys(f.checksums).sort());
    const sealed = f.sealedRuntime.at(-1);
    assert.ok(sealed, "outer app was sealed with runtime present");
    assert.deepEqual(sealed.manifest, manifest, "manifest cannot change after outer sealing");
    assert.deepEqual(manifest.checksums, sealed.actual, "all tracked bytes, including sidecar pins, match at sealing");
    assert.ok(f.commands.some(({ command, args }) => command === "/usr/bin/codesign" && args.includes("--verify") && args.at(-1) === f.app));
  });
}

test("packaging does not mutate signatures after the builder has finalized runtime hashes", () => {
  const script = fs.readFileSync(new URL("./build-darwin-arm64.sh", import.meta.url), "utf8");
  const tail = script.slice(script.indexOf('\nAPP_PATH="${DIST_ROOT}/'));
  assert.ok(tail.length > 100, "inspect the actual post-builder packaging path");
  for (const line of tail.split("\n").filter((line) => !line.trim().startsWith("#") && /\bcodesign\b/.test(line))) {
    assert.match(line, /codesign (?:--verify|-dv)\b/, `post-builder codesign must be read-only: ${line}`);
  }
});

test("Windows packaging pins the signed runtime copies and preserves other manifest entries", async t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "lm-windows-runtime-pins-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const stage = path.join(root, "stage"), appOutDir = path.join(root, "win-unpacked");
  const packed = path.join(appOutDir, "resources", "runtime");
  const launcher = path.join(root, "launcher.exe");
  fs.mkdirSync(appOutDir);
  fs.writeFileSync(launcher, "launcher fixture");
  fs.writeFileSync(path.join(appOutDir, "LazyMind.exe"), "Electron fixture");
  const pinnedNames = ["lark-cli", "feishu-credential-helper", "github-auth-helper", "another-tool"];
  const files = { "bin/core.exe": "core fixture", "bin/tool.cfg": "fixed settings", "builtin-skills/catalog.json": "{}" };
  for (const name of pinnedNames) {
    const bytes = "unsigned " + name;
    files[`bin/${name}.exe`] = bytes;
    // Both formats occur in build-windows-x64.ps1.
    files[`bin/${name}.sha256`] = sha(bytes) + (name === "lark-cli" ? "\r\n" : "");
  }
  for (const [name, bytes] of Object.entries(files)) {
    const file = path.join(stage, name);
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, bytes);
  }
  const manifest = { version: 1, platform: "windows", features: { recording: true },
    checksums: Object.fromEntries(Object.entries(files).map(([name, bytes]) => [name, sha(bytes)])) };
  fs.writeFileSync(path.join(stage, "manifest.json"), JSON.stringify(manifest));

  const module = { exports: {} };
  vm.runInNewContext(fs.readFileSync(configPath, "utf8"), {
    module, require, __dirname: path.dirname(fileURLToPath(configPath)), Buffer, setTimeout, console,
    process: { platform: "win32", arch: "x64", env: { LAZYMIND_DESKTOP_RUNTIME_STAGE: stage,
      LAZYMIND_DESKTOP_WINDOWS_LAUNCHER: launcher, WIN_CSC_LINK: "fixture" } },
  });
  const config = module.exports;
  const runtimeResource = config.extraResources.find(resource => resource.from === stage);
  assert.equal(runtimeResource?.to, "runtime");
  const context = { appOutDir, electronPlatformName: "win32", arch: 1, packager: { appInfo: { productFilename: "LazyMind", version: "1.0.0" } } };
  const builderRequire = createRequire(require.resolve("electron-builder/package.json"));
  const { WinPackager } = builderRequire("app-builder-lib/out/winPackager");
  const appBuilderRequire = createRequire(builderRequire.resolve("app-builder-lib/package.json"));
  const { copyDir, DO_NOT_USE_HARD_LINKS } = appBuilderRequire("builder-util/out/fs");
  const signing = { platformSpecificBuildOptions: config.win, shouldSignFile: WinPackager.prototype.shouldSignFile,
    sign: async file => fs.promises.appendFile(file, "|fixture signature") };
  // PlatformPackager copies extraResources with the Windows transformer before afterPack.
  const trace = message => console.log(`${new Date().toISOString()} [win-packaging] ${message}`);
  trace("before copyDir");
  await copyDir(stage, packed, { isUseHardLink: DO_NOT_USE_HARD_LINKS,
    transformer: WinPackager.prototype.createTransformerForExtraFiles.call(signing, context) });
  trace("after copyDir");
  trace("before afterPack");
  await config.afterPack(context);
  trace("after afterPack");
  trace("before afterSign");
  await config.afterSign?.(context);
  trace("after afterSign");

  const expected = { ...manifest, checksums: { ...manifest.checksums } };
  for (const [name, bytes] of Object.entries(files)) {
    const copied = fs.readFileSync(path.join(packed, name));
    if (name.endsWith(".exe")) assert.equal(copied.toString(), bytes + "|fixture signature", name);
    else if (name.endsWith(".sha256")) {
      const executable = name.slice(0, -".sha256".length) + ".exe";
      assert.equal(copied.toString().trim(), sha(fs.readFileSync(path.join(packed, executable))), name);
    } else assert.equal(copied.toString(), bytes, name);
    expected.checksums[name] = sha(copied);
  }
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(packed, "manifest.json"), "utf8")), expected);
  trace("assertions complete");
});

test("release CI uses the shared tag decision without an additional prerelease filter", () => {
  const workflow = fs.readFileSync(new URL("../../.github/workflows/desktop-release.yml", import.meta.url), "utf8");
  const job = workflow.slice(workflow.indexOf("\n  release:"));
  assert.doesNotMatch(job.slice(0, job.indexOf("\n    steps:")), /\bif:/);
  assert.match(job, /id: version_metadata/);
  assert.match(job, /node desktop\/scripts\/resolve-release-version\.mjs/);
  assert.match(job, /steps\.version_metadata\.outputs\.is_prerelease/);
  assert.match(job, /steps\.version_metadata\.outputs\.release_channel/);
});

test("release CLI decides canonical version, channel and prerelease status for all supported tag forms", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "lm-update-version-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const packagePath = path.join(root, "package.json");
  for (const [tag, version, channel] of [
    ["v1.2.3", "1.2.3", "stable"],
    ["v1.2.3a2", "1.2.3-alpha.2", "alpha"], ["v1.2.3-alpha.2", "1.2.3-alpha.2", "alpha"],
    ["v1.2.3b4", "1.2.3-beta.4", "beta"], ["v1.2.3-beta.4", "1.2.3-beta.4", "beta"],
    ["v1.2.3rc1", "1.2.3-rc.1", "rc"], ["v1.2.3-rc.1", "1.2.3-rc.1", "rc"],
  ]) {
    fs.writeFileSync(packagePath, JSON.stringify({ name: "fixture", version: "0.0.0" }));
    const output = execFileSync(process.execPath, [fileURLToPath(new URL("./resolve-release-version.mjs", import.meta.url)),
      "--tag", tag, "--package", packagePath], { encoding: "utf8" });
    const values = Object.fromEntries(output.trim().split("\n").map((line) => line.split("=")));
    assert.equal(values.package_version, version, tag);
    assert.equal(values.release_channel, channel, tag);
    assert.equal(values.is_prerelease, String(channel !== "stable"), tag);
    assert.equal(JSON.parse(fs.readFileSync(packagePath, "utf8")).version, version);
  }
});
