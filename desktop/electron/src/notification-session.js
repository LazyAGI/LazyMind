const { createHash } = require("node:crypto");

// Handoff between a destroyed renderer and its replacement. Exact
// credential matching prevents an old page, logout or another login being restored.
function createNotificationSession() {
  let original;
  let latest;
  let savedFingerprint;
  const fingerprint = (value) => createHash("sha256").update(JSON.stringify([
    value.server_url, value.access_token, value.refresh_token,
  ])).digest("hex");
  const matches = (a, b) => Boolean(a && b && a.server_url === b.server_url
    && a.access_token === b.access_token && a.refresh_token === b.refresh_token);
  return {
    hydrate(value) {
      if (latest || !/^[a-f0-9]{64}$/.test(value?.desktop_handoff)
        || !value.access_token || !value.refresh_token) return;
      latest = { ...value };
      savedFingerprint = value.desktop_handoff;
    },
    remember(value) {
      savedFingerprint = undefined;
      if (matches(value, latest)) { original = { ...value }; return; }
      if (matches(value, original)) return;
      original = latest = { ...value };
    },
    restore(value) {
      if (!value || typeof value.server_url !== "string" || value.server_url.length > 2048
        || typeof value.access_token !== "string" || !value.access_token || value.access_token.length > 16384
        || typeof value.refresh_token !== "string" || !value.refresh_token || value.refresh_token.length > 16384) return null;
      return matches(value, original) || matches(value, latest)
        || (latest && value && value.server_url === latest.server_url && savedFingerprint === fingerprint(value))
        ? { ...latest } : null;
    },
    rotated(before, after) {
      if (!matches(before, latest) || before.server_url !== after.server_url) return false;
      latest = { ...after };
      return true;
    },
    clear() { original = latest = savedFingerprint = undefined; },
  };
}

module.exports = { createNotificationSession };
