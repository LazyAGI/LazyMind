const fs = require("node:fs");

function unsignedTestBuild(env = process.env) {
  if (!env.LAZYMIND_DESKTOP_UPDATE_CONFIG) return false;
  const config = JSON.parse(fs.readFileSync(env.LAZYMIND_DESKTOP_UPDATE_CONFIG, "utf8"));
  if (config.mode !== "unsigned-test") return false;
  if (env.LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST !== "true" || config.audience === "official"
    || config.channel !== "personal-test" || (config.platform && config.platform !== "darwin") || config.signingIdentity !== "adhoc"
    || (env.LAZYMIND_DESKTOP_SIGNING_MODE || "adhoc") !== "adhoc"
    || (env.LAZYMIND_DESKTOP_PACKAGE_KIND || "zip") !== "zip"
    || env.LAZYMIND_CLOUD_BASE_URL) throw new Error("Invalid isolated macOS test build");
  const origins = config.allowedOrigins;
  if (!Array.isArray(origins) || !origins.length || origins.some(origin => {
    const url = new URL(origin); return url.protocol !== "http:" || url.hostname !== "127.0.0.1" || url.username || url.password || url.pathname !== "/" || url.search || url.hash;
  }) || !origins.includes(new URL(config.policyURL).origin)) throw new Error("Unsigned test updates require a fixed loopback origin");
  return true;
}
module.exports = { unsignedTestBuild };
