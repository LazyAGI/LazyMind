const path = require("node:path");

const DEV_READY_SERVICES = [
  "local-proxy",
  "auth-service",
  "core",
  "frontend",
  "chat",
  "lazyllm-doc-server",
  "lazyllm-parse-server",
  "lazyllm-parse-worker",
  "lazyllm-algo",
];

// External-runtime development uses the CLI built by the local workflow,
// not the separate installer staging directory.
function resolveAgentConnectorPath({ override, isExternalRuntimeDev, repoRoot, runtimeResourcesRoot, isWindows }) {
  if (override) return override;
  const binary = isWindows ? "lazymind.exe" : "lazymind";
  return isExternalRuntimeDev
    ? path.join(repoRoot, "local", "build", "bin", binary)
    : path.join(runtimeResourcesRoot, "bin", binary);
}

function normalizeLoopbackURL(raw, name = "URL") {
  const value = String(raw || "").trim();
  if (!value) return "";
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error(`${name} is not a valid URL`);
  }
  const hostname = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (!["127.0.0.1", "localhost", "::1"].includes(hostname)) {
    throw new Error(`${name} must use a loopback host`);
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error(`${name} must use http or https`);
  }
  parsed.username = "";
  parsed.password = "";
  parsed.pathname = parsed.pathname.replace(/\/+$/, "") || "/";
  parsed.search = "";
  parsed.hash = "";
  return parsed.toString().replace(/\/$/, "");
}

function desktopDevRendererURL(baseURL) {
  const normalized = normalizeLoopbackURL(baseURL, "LAZYMIND_DESKTOP_DEV_URL");
  if (!normalized) throw new Error("LAZYMIND_DESKTOP_DEV_URL is required");
  return new URL("/agent/chat/home", `${normalized}/`).toString();
}

function desktopDevRuntimeStatus(externalRuntimeURL) {
  const normalized = normalizeLoopbackURL(
    externalRuntimeURL,
    "LAZYMIND_DESKTOP_EXTERNAL_RUNTIME_URL",
  );
  if (!normalized) throw new Error("LAZYMIND_DESKTOP_EXTERNAL_RUNTIME_URL is required");
  return {
    overallStatus: "ready",
    profile: "desktop-dev",
    ownerMatched: false,
    externalRuntime: true,
    config: { externalRuntimeURL: normalized },
    services: Object.fromEntries(DEV_READY_SERVICES.map((name) => [name, { status: "ready" }])),
  };
}

function desktopNotificationRuntimeReady(status, externalRuntimeDev = false) {
  return status?.overallStatus === "ready"
    && (status.ownerMatched === true || (externalRuntimeDev && status.externalRuntime === true));
}

function desktopNotificationAPIOrigin(status, externalRuntimeDev = false) {
  if (externalRuntimeDev && status?.externalRuntime) {
    return normalizeLoopbackURL(status?.config?.externalRuntimeURL, "external runtime URL");
  }
  const proxy = status?.config?.localProxy || status?.config?.LocalProxy;
  const proxyPort = Number(proxy?.port || proxy?.Port);
  return Number.isInteger(proxyPort) && proxyPort > 0 ? `http://127.0.0.1:${proxyPort}` : "";
}

function desktopNotificationInstanceID(status, externalRuntimeDev = false) {
  if (externalRuntimeDev && status?.externalRuntime) {
    return normalizeLoopbackURL(status?.config?.externalRuntimeURL, "external runtime URL");
  }
  return String(status?.runtimeRoot || "").trim();
}

function restoreDesktopNotificationSession(saved, notificationSession, desktopNotifications) {
  if (!saved?.ok || !saved.session?.access_token || !saved.session?.server_url) return false;
  notificationSession.hydrate(saved.session);
  void desktopNotifications.setSession(saved.session);
  return true;
}

module.exports = {
  resolveAgentConnectorPath,
  desktopNotificationAPIOrigin,
  desktopNotificationInstanceID,
  desktopNotificationRuntimeReady,
  desktopDevRendererURL,
  desktopDevRuntimeStatus,
  normalizeLoopbackURL,
  restoreDesktopNotificationSession,
};
