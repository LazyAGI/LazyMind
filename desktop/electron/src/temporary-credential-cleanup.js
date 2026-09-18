// Use the managed Core's loopback port and owner-provided internal token, never
// a renderer URL or a Cloud endpoint. Closing the renderer must not cancel this.
async function clearTemporaryCredentials({ cloudEnabled, corePort, internalToken, fetch, reportError }) {
  if (!cloudEnabled || !Number.isInteger(corePort) || corePort <= 0 || corePort > 65535 || !internalToken) return;
  try {
    const response = await fetch(`http://127.0.0.1:${corePort}/internal/credential-vault/restores:clear-temporary`, {
      method: "POST",
      headers: { "X-LazyMind-Internal-Token": internalToken },
      redirect: "error",
      signal: AbortSignal.timeout(2000),
    });
    if (!response.ok) throw new Error("Temporary credential cleanup failed");
  } catch {
    reportError();
  }
}

module.exports = { clearTemporaryCredentials };
