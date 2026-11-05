#!/usr/bin/env node
// Materialize immutable update files from one completed native build.
import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { isDeepStrictEqual } from "node:util";

export async function fingerprint(filename) {
  const stat = fs.lstatSync(filename);
  if (!stat.isFile() || stat.size <= 0) throw new Error("Expected a nonempty regular release file");
  const sha256 = createHash("sha256"), sha512 = createHash("sha512");
  let size = 0;
  for await (const chunk of fs.createReadStream(filename)) { size += chunk.length; sha256.update(chunk); sha512.update(chunk); }
  if (size !== stat.size) throw new Error("Release file changed while hashing");
  return { size, sha256: sha256.digest("hex"), sha512: sha512.digest("base64") };
}

export async function prepareRelease({ resources, packagePath, baseURL, minimumSystemVersion, output }, inspectSignature = spawnSync) {
  const runtime = path.join(resources, "runtime");
  const helper = path.join(runtime, "bin", `lazymind-update-helper${process.platform === "win32" ? ".exe" : ""}`);
  const status = JSON.parse(execFileSync(helper, ["config"], { encoding: "utf8", timeout: 35000, maxBuffer: 1024 * 1024, stdio: ["ignore", "pipe", "pipe"] }));
  if (status.status !== "enabled" || status.config?.mode !== "official") throw new Error("Update configuration must be enabled in the built helper");
  const config = status.config;
  const native = JSON.parse(fs.readFileSync(path.join(resources, "app-update.yml"), "utf8"));
  const expected = { protocol: config.updaterProtocol, releaseId: config.releaseId, version: config.version, mode: config.mode,
    platform: config.platform, arch: config.arch, signingIdentity: config.signingIdentity };
  if (!isDeepStrictEqual(native.lazymind, expected)) throw new Error("Native metadata does not match the built helper");
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(config.releaseId)) throw new Error("Invalid release identity");
  if (!/^\d+\.\d+\.\d+$/.test(minimumSystemVersion)) throw new Error("Specify minimum system version as major.minor.patch");
  const base = new URL(baseURL);
  if (base.protocol !== "https:" || base.username || base.password || base.search || base.hash || !config.allowedOrigins.some(origin => origin.replace(/\/$/, "") === base.origin)) throw new Error("Release base URL must use a pinned HTTPS origin");
  const extension = config.platform === "darwin" ? ".zip" : ".exe";
  if (path.extname(packagePath) !== extension) throw new Error(`Update package must be ${extension}`);
  const manifest = path.join(runtime, "manifest.json"), components = path.join(runtime, "config", "python-components.json");
  const manifestHash = (await fingerprint(manifest)).sha256;
  const catalog = JSON.parse(fs.readFileSync(components, "utf8"));
  if (catalog.schemaVersion !== 1 || !catalog.components || typeof catalog.components !== "object" || catalog.platform !== config.platform || catalog.arch !== config.arch) throw new Error("Invalid built component catalog");
  if (config.platform === "darwin") {
    const app = path.resolve(resources, "..", "..");
    const verified = inspectSignature("/usr/bin/codesign", ["--verify", "--deep", "--strict", app], { encoding: "utf8" });
    const signature = inspectSignature("/usr/bin/codesign", ["-dv", "--verbose=4", app], { encoding: "utf8" });
    const team = String(signature.stderr).match(/^TeamIdentifier=(.+)$/m)?.[1]?.trim();
    if (verified.status !== 0 || signature.status !== 0 || team !== config.signingIdentity || !String(signature.stderr).includes("Authority=Developer ID Application:")) throw new Error("App signature does not match update identity");
    // Read only fixed members; do not extract an untrusted archive into the filesystem.
    for (const relative of ["runtime/manifest.json", "runtime/config/python-components.json", "app-update.yml"]) {
      const archived = execFileSync("/usr/bin/unzip", ["-p", packagePath, `LazyMind.app/Contents/Resources/${relative}`], { maxBuffer: 16 * 1024 * 1024, stdio: ["ignore", "pipe", "pipe"] });
      if (!archived.equals(fs.readFileSync(path.join(resources, relative)))) throw new Error("ZIP metadata does not match the built application");
    }
  }
  // Check before creating output; never overwrite an earlier staged release.
  const directory = path.join(output, config.releaseId);
  if (fs.existsSync(directory)) throw new Error("Release output already exists");
  fs.mkdirSync(output, { recursive: true });
  fs.mkdirSync(directory);
  fs.copyFileSync(packagePath, path.join(directory, `package${extension}`), fs.constants.COPYFILE_EXCL);
  fs.copyFileSync(components, path.join(directory, "components.json"), fs.constants.COPYFILE_EXCL);
  const packageHash = await fingerprint(path.join(directory, `package${extension}`));
  const sourceHash = await fingerprint(packagePath);
  if (!isDeepStrictEqual(packageHash, sourceHash)) throw new Error("Package changed while staging");
  const feed = { version: config.version, files: [{ url: `package${extension}`, size: packageHash.size, sha512: packageHash.sha512 }],
    path: `package${extension}`, sha512: packageHash.sha512 };
  fs.writeFileSync(path.join(directory, "feed.json"), JSON.stringify(feed) + "\n", { flag: "wx" });
  const release = { releaseId: config.releaseId, version: config.version, platform: config.platform, arch: config.arch,
    minimumSystemVersion, minUpdaterProtocol: config.updaterProtocol, updaterProtocol: config.updaterProtocol, signingIdentity: config.signingIdentity };
  const prefix = new URL(`releases/${config.releaseId}/`, base.href.endsWith("/") ? base.href : base.href + "/");
  for (const [kind, filename] of [["package", `package${extension}`], ["feed", "feed.json"], ["components", "components.json"]]) {
    const { size, sha256 } = await fingerprint(path.join(directory, filename));
    release[kind] = { url: new URL(filename, prefix).href, size, sha256 };
  }
  if (config.updaterProtocol === 2) {
    const application = config.platform === "darwin" ? path.resolve(resources, "..", "..") : path.dirname(resources);
    const allocated = directory => fs.readdirSync(directory, { withFileTypes: true }).reduce((total, entry) => {
      const filename = path.join(directory, entry.name), info = fs.lstatSync(filename);
      return total + (entry.isDirectory() ? allocated(filename) : Math.max(info.size, (info.blocks || 0) * 512));
    }, 0);
    release.package.unpackedBytes = allocated(application);
  }
  const record = { config, release, manifestSHA256: manifestHash };
  if (config.updaterProtocol === 2) {
    const recipe = JSON.parse(fs.readFileSync(path.join(runtime, "config", "update-format-v2.json"), "utf8"));
    const normalized = { version: recipe.version,
      families: Object.fromEntries(Object.entries(recipe.families).sort(([a], [b]) => a.localeCompare(b))),
      segments: recipe.segments.map(entry => ({ type: entry.type, name: entry.name, table: entry.table, sql: entry.sql })),
      lazyllmVariants: recipe.lazyllmVariants ?? null };
    record.formatProfile = "format-v2-" + createHash("sha256").update(JSON.stringify(normalized)).digest("hex");
    // Build metadata stays in the pinned catalog, outside the strict protocol descriptor.
    record.componentDescriptor = { ...catalog.components.rag };
    delete record.componentDescriptor.schemaVersion;
    delete record.componentDescriptor.signingMode;
    if (!record.componentDescriptor || !/^[a-f0-9]{64}$/.test(record.componentDescriptor.revision || "")) throw new Error("Final target component descriptor is required");
  }
  fs.writeFileSync(path.join(directory, "release.json"), JSON.stringify(record, null, 2) + "\n", { flag: "wx" });
  return record;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = {};
    const names = { "--resources": "resources", "--package": "packagePath", "--base-url": "baseURL", "--minimum-system-version": "minimumSystemVersion", "--output": "output" };
    for (let i = 2; i < process.argv.length; i += 2) {
      if (!names[process.argv[i]] || !process.argv[i + 1]) throw new Error("Invalid release arguments");
      options[names[process.argv[i]]] = process.argv[i + 1];
    }
    if (Object.values(names).some(name => !options[name])) throw new Error("Missing release arguments");
    await prepareRelease(options);
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
