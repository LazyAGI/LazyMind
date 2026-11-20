const fs = require("node:fs");
const path = require("node:path");

const filename = path.join(__dirname, ".unsigned-update-test.local.json");
const policyPath = /^\/datasets\/[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+\/resolve\/master\/updates\/unsigned-test\/(?:[A-Za-z0-9._-]+\/)*[A-Za-z0-9_-]+\.json$/;
const artifactPath = /^(\/datasets\/[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+\/resolve\/master\/updates\/unsigned-test\/)(?:[A-Za-z0-9._-]+\/)*$/;

function validateUnsignedTestConfig(config) {
  if (config?.mode !== "unsigned-test" || config.audience === "official" || config.channel !== "personal-test" ||
    config.signingIdentity !== "adhoc" || config.updaterProtocol !== 2) throw new Error("Invalid isolated v2 update configuration");
  const policy = new URL(config.policyURL);
  if (policy.origin !== "https://modelscope.cn" || policy.username || policy.password || policy.search || policy.hash ||
    !policyPath.test(policy.pathname)) throw new Error("Test policy must use the isolated ModelScope update prefix");
  return config;
}

function unsignedTestBase(url, config) {
  const base = new URL(url);
  const policy = new URL(config.policyURL);
  if (base.origin !== policy.origin || base.username || base.password || base.search || base.hash ||
    !artifactPath.test(base.pathname) || !policy.pathname.startsWith(base.pathname)) {
    throw new Error("Test artifacts and policy must share the isolated ModelScope update prefix");
  }
  return base.href;
}

// Only the explicit build wrapper creates this file in its private source copy.
// Inherited environment flags are never sufficient to enable test packaging.
function readUnsignedTestBuildConfig() {
  const info = fs.lstatSync(filename, { throwIfNoEntry: false });
  if (!info) return undefined;
  if (!info.isFile() || info.size > 65536 || process.platform !== "darwin" ||
    !/^\.lazymind-unsigned-v2\.[A-Za-z0-9]+$/.test(path.basename(path.resolve(__dirname, "../..")))) {
    throw new Error("Invalid test build marker");
  }
  return validateUnsignedTestConfig(JSON.parse(fs.readFileSync(filename, "utf8")));
}

module.exports = { filename, unsignedTestBase, validateUnsignedTestConfig, readUnsignedTestBuildConfig };
