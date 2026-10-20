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

test("source local runtimes stage and expose the pinned Pandoc executable", async () => {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
  const makefile = await readFile(path.join(root, "Makefile"), "utf8");
  const windowsScript = await readFile(path.join(root, "local", "scripts", "local-win.ps1"), "utf8");

  assert.match(makefile, /local-pandoc:[\s\S]*stage-pandoc\.mjs[\s\S]*--target darwin-arm64/);
  assert.match(makefile, /local-up:.*local-pandoc/);
  assert.match(makefile, /LOCAL_PANDOC_ENV := LAZYMIND_PANDOC_PATH="\$\(LOCAL_PANDOC_BIN\)"/);
  assert.match(windowsScript, /function Stage-Pandoc[\s\S]*stage-pandoc\.mjs[\s\S]*--target windows-x64/);
  assert.match(windowsScript, /\$env:LAZYMIND_PANDOC_PATH = \$pandocBin/);
  assert.match(windowsScript, /'up' \{ Build-Manager; Stage-Pandoc;/);
  assert.match(windowsScript, /'up-lan'[\s\S]*Build-Manager\s+Stage-Pandoc/);
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

test("Windows extracts verified ZIPs using literal paths and propagates extraction errors", {
  skip: process.platform !== "win32",
}, async () => {
  const { execFileSync } = await import("node:child_process");
  const root = await mkdtemp(path.join(os.tmpdir(), "lazymind pandoc 中文 ' $ & [test]-"));
  try {
    const sourceDir = path.join(root, "source");
    await mkdir(sourceDir);
    const source = path.join(sourceDir, "pandoc.exe");
    const archive = path.join(root, "fixture.zip");
    await writeFile(source, "pandoc executable fixture");
    execFileSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command",
      "$ErrorActionPreference = 'Stop'; Add-Type -AssemblyName System.IO.Compression.FileSystem; [System.IO.Compression.ZipFile]::CreateFromDirectory($env:TEST_PANDOC_SOURCE, $env:TEST_PANDOC_ZIP)",
    ], { env: { ...process.env, TEST_PANDOC_SOURCE: sourceDir, TEST_PANDOC_ZIP: archive } });
    const body = await readFile(archive);
    const sha256 = createHash("sha256").update(body).digest("hex");
    const configPath = path.join(root, "pandoc.json");
    const target = {
      fileName: "pandoc.zip", upstreamUrls: ["https://example.invalid/pandoc.zip"], sha256,
      archivePath: "pandoc.exe", runtimePath: "bin/pandoc.exe",
    };
    const config = { schemaVersion: 1, name: "pandoc", version: "3.11", targets: { "windows-x64": target } };
    await writeFile(configPath, JSON.stringify(config));
    await writeFile(path.join(root, `${sha256}-pandoc.zip`), body);
    const result = await stagePandoc(path.join(root, "runtime"), "windows-x64", {
      cacheRoot: root, configPath, runVersion: async () => "pandoc 3.11\r\n",
    });
    assert.equal(await readFile(result.runtimePath, "utf8"), "pandoc executable fixture");

    const invalid = Buffer.from("invalid zip");
    target.sha256 = createHash("sha256").update(invalid).digest("hex");
    await writeFile(configPath, JSON.stringify(config));
    await writeFile(path.join(root, `${target.sha256}-pandoc.zip`), invalid);
    await assert.rejects(stagePandoc(path.join(root, "runtime"), "windows-x64", {
      cacheRoot: root, configPath,
      runVersion: async () => { assert.fail("must not execute after extraction fails"); },
    }), (error) => error.code === 1 && /powershell/i.test(error.message));
    assert.equal(await readFile(result.runtimePath, "utf8"), "pandoc executable fixture");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

// Hold an actual Windows handle without FILE_SHARE_DELETE, reproducing scanner
// or recently executed image locks. stdin releases it, including on test failure.
async function holdWindowsFile(filePath) {
  const { spawn } = await import("node:child_process");
  const { once } = await import("node:events");
  const child = spawn("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command",
    "$ErrorActionPreference = 'Stop'; $file = [IO.File]::Open($env:TEST_LOCK_FILE, 'Open', 'Read', 'Read'); try { [Console]::WriteLine('LOCKED'); [Console]::Out.Flush(); [Console]::ReadLine() | Out-Null } finally { $file.Dispose() }",
  ], { env: { ...process.env, TEST_LOCK_FILE: filePath }, stdio: ["pipe", "pipe", "pipe"] });
  const closed = once(child, "close");
  let stderr = "";
  child.stderr.on("data", data => { stderr += data; });
  try {
    await Promise.race([
      (async () => {
        let output = "";
        for await (const chunk of child.stdout) {
          output += chunk;
          if (output.includes("LOCKED")) return;
        }
        throw new Error(`Lock helper exited before locking: ${stderr}`);
      })(),
      closed.then(() => { throw new Error(`Lock helper failed: ${stderr}`); }),
    ]);
    let released = false;
    return async () => {
      if (!released) { released = true; child.stdin.end("\n"); }
      await closed;
    };
  } catch (error) {
    child.kill();
    await closed;
    throw error;
  }
}

for (const locked of ["candidate", "installed", "persistent-candidate", "persistent-installed"]) {
  test(`Windows Pandoc replacement handles a real ${locked} file lock`, {
    skip: process.platform !== "win32", timeout: 30000,
  }, async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), "pandoc-locked-"));
    let release;
    let timer;
    try {
      const runtime = path.join(root, "runtime");
      const installed = path.join(runtime, "bin/pandoc.exe");
      await mkdir(path.dirname(installed), { recursive: true });
      await writeFile(installed, "previous executable");
      const archiveBody = "verified fixture";
      const sha256 = createHash("sha256").update(archiveBody).digest("hex");
      const configPath = path.join(root, "config.json");
      await writeFile(configPath, JSON.stringify({ schemaVersion: 1, name: "pandoc", version: "3.11", targets: {
        "windows-x64": { fileName: "pandoc.zip", upstreamUrls: ["https://example.invalid/pandoc.zip"], sha256,
          archivePath: "pandoc.exe", runtimePath: "bin/pandoc.exe" },
      } }));
      await writeFile(path.join(root, `${sha256}-pandoc.zip`), archiveBody);
      const options = {
        configPath, cacheRoot: root,
        extractZip: async (_archive, destination) => { await writeFile(path.join(destination, "pandoc.exe"), "new executable"); },
        runVersion: async candidate => {
          const lockPath = locked.endsWith("candidate") ? candidate : installed;
          release = await holdWindowsFile(lockPath);
          const { rename } = await import("node:fs/promises");
          await assert.rejects(rename(lockPath, `${lockPath}.probe`), error => ["EBUSY", "EPERM", "EACCES"].includes(error.code));
          if (!locked.startsWith("persistent-")) timer = setTimeout(() => { void release(); }, 1600);
          return "pandoc 3.11\n";
        },
      };
      if (locked.startsWith("persistent-")) {
        await assert.rejects(stagePandoc(runtime, "windows-x64", options), error => ["EBUSY", "EPERM", "EACCES"].includes(error.code));
        assert.equal(await readFile(installed, "utf8"), "previous executable");
      } else {
        await stagePandoc(runtime, "windows-x64", options);
        assert.equal(await readFile(installed, "utf8"), "new executable");
      }
    } finally {
      clearTimeout(timer);
      if (release) await release();
      await rm(root, { recursive: true, force: true, maxRetries: 5, retryDelay: 250 });
    }
  });
}
