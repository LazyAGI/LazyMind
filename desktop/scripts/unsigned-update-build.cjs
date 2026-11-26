const fs = require("node:fs");
const path = require("node:path");

const filename = path.join(__dirname, ".unsigned-update-test.local.json");
const policyPath = /^\/datasets\/[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+\/resolve\/master\/updates\/unsigned-test\/(?:[A-Za-z0-9._-]+\/)*[A-Za-z0-9_-]+\.json$/;
const artifactPath = /^(\/datasets\/[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+\/resolve\/master\/updates\/unsigned-test\/)(?:[A-Za-z0-9._-]+\/)*$/;

function validateUnsignedTestTemplate(config) {
  if (config?.mode !== "unsigned-test" || config.audience === "official" || config.channel !== "personal-test" ||
    config.signingIdentity !== "adhoc" || config.updaterProtocol !== 2) throw new Error("Invalid isolated v2 update configuration");
  const policy = new URL(config.policyURL);
  if (policy.origin !== "https://modelscope.cn" || policy.username || policy.password || policy.search || policy.hash ||
    !policyPath.test(policy.pathname)) throw new Error("Test policy must use the isolated ModelScope update prefix");
  return config;
}

function validateUnsignedTestConfig(config) {
  validateUnsignedTestTemplate(config);
  const round = /\/in-place\/round-([1-9][0-9]{0,19})\/policy\.json$/.exec(new URL(config.policyURL).pathname);
  if (!round || !new RegExp(`^inplace-${round[1]}-[0-9a-f]{64}$`).test(config.trustDomain)) {
    throw new Error("Expected an in-place test policy and matching trust domain. Start a new round.");
  }
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

function inPlaceArtifactBase(url, config) {
  const base = unsignedTestBase(url, config);
  if (new URL(".", config.policyURL).href !== base) throw new Error("In-place artifacts must use their signed policy directory");
  return base;
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

module.exports = { filename, unsignedTestBase, inPlaceArtifactBase, validateUnsignedTestTemplate, validateUnsignedTestConfig, readUnsignedTestBuildConfig };
