const fs = require("node:fs");
const path = require("node:path");

function normalizeCloudOrigin(value, allowLoopbackHTTP) {
  const candidate = String(value || "");
  if (!candidate || candidate.trim() !== candidate) {
    throw new Error("Cloud release configuration is invalid");
  }
  let parsed;
  try {
    parsed = new URL(candidate);
  } catch {
    throw new Error("Cloud release configuration is invalid");
  }
  const loopback = parsed.hostname === "localhost" || parsed.hostname === "127.0.0.1" || parsed.hostname === "::1";
  if (
    (parsed.protocol !== "https:" && !(allowLoopbackHTTP && parsed.protocol === "http:" && loopback)) ||
    parsed.username ||
    parsed.password ||
    parsed.pathname !== "/" ||
    parsed.search ||
    parsed.hash
  ) {
    throw new Error("Cloud release configuration is invalid");
  }
  return parsed.origin;
}

function resolveDesktopCloudBaseURL({
  isPackaged,
  runtimeResourcesRoot,
  environment = process.env,
  manifest,
}) {
  return resolveDesktopCloudConfiguration({
    isPackaged,
    runtimeResourcesRoot,
    environment,
    manifest,
  }).baseURL;
}

function normalizeCallbackConfiguration(cloud, buildAudience) {
  const mode = String(cloud?.oauthCallbackMode || "direct").trim();
  if (mode !== "direct" && mode !== "localhost-relay") {
    throw new Error("Cloud release configuration is invalid");
  }
  if (mode === "direct") {
    return { oauthCallbackMode: "direct", oauthCallbackPort: 0 };
  }
  const port = Number(cloud?.oauthCallbackPort);
  if (buildAudience !== "internal" || !Number.isInteger(port) || port < 1024 || port > 65535) {
    throw new Error("Cloud release configuration is invalid");
  }
  return { oauthCallbackMode: mode, oauthCallbackPort: port };
}

function resolveDesktopCloudConfiguration({
  isPackaged,
  runtimeResourcesRoot,
  environment = process.env,
  manifest,
}) {
  if (!isPackaged) {
    const developmentValue = String(environment.LAZYMIND_CLOUD_BASE_URL || "").trim();
    const baseURL = developmentValue ? normalizeCloudOrigin(developmentValue, true) : "";
    const buildAudience = String(environment.LAZYMIND_DESKTOP_BUILD_AUDIENCE || "production").trim();
    const callback = normalizeCallbackConfiguration({
      oauthCallbackMode: environment.LAZYMIND_CLOUD_OAUTH_CALLBACK_MODE,
      oauthCallbackPort: environment.LAZYMIND_CLOUD_OAUTH_CALLBACK_PORT,
    }, buildAudience);
    if (!baseURL && callback.oauthCallbackMode !== "direct") {
      throw new Error("Cloud release configuration is invalid");
    }
    return { baseURL, ...callback };
  }

  let releaseManifest = manifest;
  if (!releaseManifest) {
    try {
      releaseManifest = JSON.parse(fs.readFileSync(path.join(runtimeResourcesRoot, "manifest.json"), "utf8"));
    } catch {
      throw new Error("Cloud release configuration is invalid");
    }
  }
  const releaseValue = releaseManifest?.cloud?.baseURL;
  const baseURL = releaseValue ? normalizeCloudOrigin(releaseValue, false) : "";
  const callback = normalizeCallbackConfiguration(
    releaseManifest?.cloud,
    String(releaseManifest?.buildAudience || "production").trim(),
  );
  if (!baseURL && callback.oauthCallbackMode !== "direct") {
    throw new Error("Cloud release configuration is invalid");
  }
  return { baseURL, ...callback };
}

function loadDesktopCloudConfiguration(options) {
  try {
    return resolveDesktopCloudConfiguration(options);
  } catch {
    // Disable only the optional Cloud integration; never trust another origin.
    return { baseURL: "", oauthCallbackMode: "direct", oauthCallbackPort: 0, errorCode: "CLOUD_CONFIG_INVALID" };
  }
}

module.exports = { resolveDesktopCloudBaseURL, resolveDesktopCloudConfiguration, loadDesktopCloudConfiguration };
