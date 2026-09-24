import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { resolvePandocTarget, stagePandoc } from "./stage-pandoc.mjs";

test("pins the executable paths used by the official Pandoc archives", async () => {
  const config = JSON.parse(await readFile(
    path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "dependencies", "pandoc.json"),
    "utf8",
  ));
  assert.equal(config.targets["darwin-arm64"].archivePath, "pandoc-3.11-arm64/bin/pandoc");
  assert.equal(config.targets["windows-x64"].archivePath, "pandoc-3.11/pandoc.exe");
});

test("orders an explicit Pandoc mirror before domestic and upstream URLs", () => {
  const selected = resolvePandocTarget({
    schemaVersion: 1,
    name: "pandoc",
    version: "3.11",
    targets: {
      "darwin-arm64": {
        fileName: "pandoc.zip",
        domesticUrls: ["https://mirror.example/pandoc.zip"],
        upstreamUrls: ["https://github.example/pandoc.zip"],
        sha256: "a".repeat(64),
        archivePath: "pandoc/bin/pandoc",
        runtimePath: "bin/pandoc",
      },
    },
  }, "darwin-arm64", { LAZYMIND_PANDOC_MIRROR_URL: "https://internal.example/pandoc.zip" });

  assert.deepEqual(selected.urls, [
    "https://internal.example/pandoc.zip",
    "https://mirror.example/pandoc.zip",
    "https://github.example/pandoc.zip",
  ]);
});

test("stages a cached, verified Pandoc executable into the runtime", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "lazymind-pandoc-"));
  try {
    const runtimeRoot = path.join(root, "runtime");
    const cacheRoot = path.join(root, "cache");
    const configPath = path.join(root, "pandoc.json");
    const archiveBody = "verified archive fixture";
    const sha256 = createHash("sha256").update(archiveBody).digest("hex");
    const config = {
      schemaVersion: 1,
      name: "pandoc",
      version: "3.11",
      targets: {
        "darwin-arm64": {
          fileName: "pandoc.zip",
          domesticUrls: [],
          upstreamUrls: ["https://example.invalid/pandoc.zip"],
          sha256,
          archivePath: "pandoc/bin/pandoc",
          runtimePath: "bin/pandoc",
        },
      },
    };
    await mkdir(cacheRoot, { recursive: true });
    await writeFile(configPath, JSON.stringify(config));
    await writeFile(path.join(cacheRoot, `${sha256}-pandoc.zip`), archiveBody);

    const result = await stagePandoc(runtimeRoot, "darwin-arm64", {
      cacheRoot,
      configPath,
      platform: "darwin",
      extractZip: async (_archive, destination) => {
        const executable = path.join(destination, "pandoc", "bin", "pandoc");
        await mkdir(path.dirname(executable), { recursive: true });
        await writeFile(executable, "pandoc executable");
      },
      runVersion: async () => "pandoc 3.11\n",
    });

    assert.equal(await readFile(result.runtimePath, "utf8"), "pandoc executable");
    assert.equal(result.runtimePath, path.join(runtimeRoot, "bin", "pandoc"));
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
