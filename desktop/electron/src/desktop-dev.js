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

module.exports = {
  desktopDevRendererURL,
  desktopDevRuntimeStatus,
  normalizeLoopbackURL,
};
