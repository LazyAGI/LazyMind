import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import test from "node:test";
import vm from "node:vm";
import { fileURLToPath } from "node:url";
const require = createRequire(import.meta.url);
const { writeNativeUpdateMetadata } = require("./native-update-metadata.cjs");
const configuration = { trustDomain: "fixture", audience: "official", channel: "stable", mode: "official", releaseId: "fixture-source", signingIdentity: "Fixture Publisher", updaterProtocol: 1,
  policyURL: "https://fixture.invalid/policy.json", allowedOrigins: ["https://fixture.invalid"], publicKeys: { fixture: Buffer.alloc(32, 7).toString("base64") } };

test("native update configuration is written into each platform's actual resources directory", async t => {
  const directory = mkdtempSync(path.join(tmpdir(), "lazymind-native-metadata-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  for (const platform of ["darwin", "win32"]) {
    const context = { appOutDir: path.join(directory, platform), electronPlatformName: platform, arch: 1,
      packager: { appInfo: { version: "1.0.0", productFilename: "LazyMind" } } };
    const output = await writeNativeUpdateMetadata(context, { configuration, signingMode: platform === "darwin" ? "developer-id" : "authenticode" });
    assert.equal(output, path.join(context.appOutDir, platform === "darwin" ? "LazyMind.app/Contents/Resources" : "resources", "app-update.yml"));
    const result = JSON.parse(readFileSync(output, "utf8"));
    assert.equal(result.lazymind.platform, platform === "win32" ? "windows" : "darwin");
    assert.equal(result.lazymind.arch, "amd64");
    assert.equal(result.lazymind.releaseId, configuration.releaseId);
    if (platform === "win32") assert.deepEqual(result.publisherName, [configuration.signingIdentity]);
    await writeNativeUpdateMetadata(context, { configuration: undefined });
    assert.equal(existsSync(output), false, "a disabled rebuild must not retain old native update metadata");
  }
});

test("the actual afterPack consumes build inputs before signing, including arm64 and disabled rebuilds", async t => {
  const directory = mkdtempSync(path.join(tmpdir(), "lazymind-native-hook-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const keys = ["LAZYMIND_DESKTOP_RUNTIME_STAGE", "LAZYMIND_DESKTOP_UPDATE_CONFIG", "LAZYMIND_DESKTOP_SIGNING_MODE", "WIN_CSC_LINK", "CSC_LINK"];
  const original = Object.fromEntries(keys.map(key => [key, process.env[key]]));
  t.after(() => { for (const key of keys) { if (original[key] === undefined) delete process.env[key]; else process.env[key] = original[key]; } });
  const configPath = path.join(directory, "public-config.json");
  writeFileSync(configPath, JSON.stringify(configuration));
  process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE = path.join(directory, "runtime");
  process.env.LAZYMIND_DESKTOP_SIGNING_MODE = "developer-id";
  process.env.WIN_CSC_LINK = "fixture-certificate-never-opened.p12";
  const filename = fileURLToPath(new URL("../electron/electron-builder.config.cjs", import.meta.url));
  const source = readFileSync(filename, "utf8");
  for (const platform of ["darwin", "win32"]) {
    const context = { appOutDir: path.join(directory, platform), electronPlatformName: platform, arch: platform === "darwin" ? 3 : 1,
      packager: { appInfo: { version: "1.0.0", productFilename: "LazyMind" } } };
    const output = path.join(context.appOutDir, platform === "darwin" ? "LazyMind.app/Contents/Resources" : "resources", "app-update.yml");
    for (const enabled of [true, false]) {
      if (enabled) process.env.LAZYMIND_DESKTOP_UPDATE_CONFIG = configPath;
      else delete process.env.LAZYMIND_DESKTOP_UPDATE_CONFIG;
      let signed = false;
      const sandbox = { module: { exports: {} }, exports: {}, require: createRequire(filename), __dirname: path.dirname(filename),
        __filename: filename, process, Buffer, console, setTimeout, Map,
        verifySignBoundary: async () => { signed = true; assert.equal(existsSync(output), enabled); }, };
      // Execute the real exported hook. Replace only launcher-copy/signing side
      // effects, not metadata production or the afterPack orchestration.
      vm.runInNewContext(source + "\ninstallWindowsDesktopLauncher = () => {}; installMacDesktopLauncher = () => {}; signAndStageEmbeddedRuntime = verifySignBoundary;", sandbox, { filename });
      await sandbox.module.exports.afterPack(context);
      assert.equal(signed, true);
      if (enabled) {
        const result = JSON.parse(readFileSync(output, "utf8"));
        assert.equal(result.lazymind.releaseId, configuration.releaseId);
        assert.equal(result.lazymind.platform, platform === "win32" ? "windows" : "darwin");
        assert.equal(result.lazymind.arch, platform === "darwin" ? "arm64" : "amd64");
        if (platform === "win32") assert.deepEqual(result.publisherName, [configuration.signingIdentity]);
      }
    }
  }
});
