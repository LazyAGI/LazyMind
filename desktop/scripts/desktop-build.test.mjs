import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const manifestScript = path.join(scriptsDir, "write-runtime-manifest.mjs");
const iconScript = path.join(scriptsDir, "generate-windows-icon.mjs");
const icnsSource = path.join(scriptsDir, "..", "electron", "assets", "LazyMind.icns");

for (const target of [
  { platform: "darwin", arch: "arm64", suffix: "" },
  { platform: "windows", arch: "amd64", suffix: ".exe" },
]) {
  test(`writes ${target.platform}/${target.arch} desktop runtime manifest`, () => {
    const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-manifest-"));
    try {
      const bin = path.join(root, "bin");
      mkdirSync(bin, { recursive: true });
      for (const name of ["process-compose", "local-proxy", "core", "scan-control-plane", "file-watcher", "caddy"]) {
        writeFileSync(path.join(bin, `${name}${target.suffix}`), name);
      }
      execFileSync(process.execPath, [
        manifestScript,
        root,
        "--platform", target.platform,
        "--arch", target.arch,
      ]);
      const manifest = JSON.parse(readFileSync(path.join(root, "manifest.json"), "utf8"));
      assert.equal(manifest.platform, target.platform);
      assert.equal(manifest.arch, target.arch);
      assert.equal(manifest.binaries.core, `bin/core${target.suffix}`);
      assert.ok(manifest.checksums[`bin/core${target.suffix}`]);
      assert.equal(Object.keys(manifest.checksums).some((key) => key.includes("\\")), false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
}

test("generates a multi-resolution Windows ICO from the macOS icon", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-icon-"));
  try {
    const output = path.join(root, "LazyMind.ico");
    execFileSync(process.execPath, [iconScript, icnsSource, output]);
    const ico = readFileSync(output);
    assert.equal(ico.readUInt16LE(0), 0);
    assert.equal(ico.readUInt16LE(2), 1);
    assert.equal(ico.readUInt16LE(4), 4);
    assert.deepEqual(
      [0, 1, 2, 3].map((index) => ico.readUInt8(6 + index * 16) || 256),
      [32, 64, 128, 256],
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
