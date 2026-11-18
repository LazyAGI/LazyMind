const fs = require("node:fs");
const path = require("node:path");

async function nativeUpdateMetadata(options) {
  const { prepareUpdateBuildConfig } = await import("./prepare-update-build-config.mjs");
  const encoded = prepareUpdateBuildConfig(options);
  if (!encoded) return null;
  const config = JSON.parse(Buffer.from(encoded, "base64").toString("utf8"));
  if (![config.trustDomain, config.audience, config.channel, config.releaseId].every(value =>
    typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value))
    || typeof config.signingIdentity !== "string" || !config.signingIdentity.trim() || config.signingIdentity.length > 1024
    || /[\x00-\x1f\x7f]/.test(config.signingIdentity)) throw new Error("Invalid native update identity");
  return {
    provider: "generic", url: "https://updates.invalid/disabled/",
    updaterCacheDirName: `LazyMind-${config.trustDomain}-${config.audience}-${config.channel}`,
    ...(config.platform === "windows" && config.mode === "official" ? { publisherName: [config.signingIdentity] } : {}),
    lazymind: { protocol: config.updaterProtocol, releaseId: config.releaseId, version: config.version, mode: config.mode,
      platform: config.platform, arch: config.arch, signingIdentity: config.signingIdentity },
  };
}

async function writeNativeUpdateMetadata(context, options = {}) {
  const platform = { darwin: "darwin", win32: "windows" }[context.electronPlatformName];
  if (!platform) return null;
  const product = context.packager.appInfo.productFilename;
  if (!product || /[/\\]/.test(product) || product === "." || product === "..") throw new Error("Invalid product filename");
  const resources = platform === "darwin" ? path.join(context.appOutDir, `${product}.app`, "Contents", "Resources")
    : path.join(context.appOutDir, "resources");
  const destination = path.join(resources, "app-update.yml");
  const { readUpdateBuildConfig } = await import("./prepare-update-build-config.mjs");
  const configuration = Object.hasOwn(options, "configuration") ? options.configuration : readUpdateBuildConfig();
  const signingMode = options.signingMode ?? (platform === "darwin" ? process.env.LAZYMIND_DESKTOP_SIGNING_MODE || "adhoc"
    : process.env.WIN_CSC_LINK || process.env.CSC_LINK ? "authenticode" : "none");
  const metadata = await nativeUpdateMetadata({ configuration, packageVersion: context.packager.appInfo.version,
    platform, arch: { 1: "amd64", 3: "arm64" }[context.arch], signingMode });
  // Clear a stale local-test marker when repackaging an existing output.
  fs.rmSync(path.join(resources, "unsigned-test.json"), { force: true });
  if (!metadata) {
    fs.rmSync(destination, { force: true });
    return null;
  }
  fs.mkdirSync(resources, { recursive: true });
  // JSON is valid YAML; the runtime can verify these exact fields without a
  // second YAML parser. The updater never checks this disabled remote URL.
  if (metadata.lazymind.protocol === 2) {
    const recipe = path.join(__dirname, "..", "..", "local", "local-runtime-manager", "update-format-v2.json");
    const configDirectory = path.join(resources, "runtime", "config");
    fs.mkdirSync(configDirectory, { recursive: true });
    fs.copyFileSync(recipe, path.join(configDirectory, "update-format-v2.json"));
  }
  fs.writeFileSync(destination, `${JSON.stringify(metadata, null, 2)}\n`);
  return destination;
}
module.exports = { nativeUpdateMetadata, writeNativeUpdateMetadata };
