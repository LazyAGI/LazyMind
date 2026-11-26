// Validate the Windows test channel both before the build and at the publication boundary.
import fs from "node:fs";
import path from "node:path";
import { isDeepStrictEqual } from "node:util";
import { prepareUpdateBuildConfig } from "./prepare-update-build-config.mjs";

const template = JSON.parse(process.env.LAZYMIND_WINDOWS_TEST_UPDATE_CONFIG_JSON || "null");
const repo = process.env.UPDATE_REPO || "";
if (!/^[A-Za-z0-9][A-Za-z0-9._-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/.test(repo)) throw new Error("Invalid Windows test dataset");
const dataset = `https://modelscope.cn/datasets/${repo}/resolve/master/`;
const policy = template?.policyURL;
if (!policy?.startsWith(dataset) || !/^updates\/windows-test\/(?:[A-Za-z0-9][A-Za-z0-9._-]*\/)+policy\.json$/.test(policy.slice(dataset.length))) {
  throw new Error("Windows test policy must be inside the configured dataset's updates/windows-test/<round>/ folder");
}
const base = policy.slice(0, -"policy.json".length);
if (template.mode !== "official" || template.audience !== "official" || template.channel !== "canary" ||
    template.updaterProtocol !== 2 || !/^windows-test-[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(template.trustDomain) ||
    typeof template.signingIdentity !== "string" || !template.signingIdentity.trim() ||
    !template.allowedOrigins?.includes("https://modelscope.cn")) throw new Error("Invalid Windows test signing identity or channel");
for (const key of ["version", "releaseId", "platform", "arch"]) {
  if (key in template) throw new Error("The Windows test build supplies " + key);
}
// Key IDs and Base64 padding bits do not change the Ed25519 public key.
const testKeys = Object.values(template.publicKeys || {}).map(key => Buffer.from(key, "base64"));
const official = JSON.parse(process.env.LAZYMIND_WINDOWS_UPDATE_CONFIG_JSON || "null");
if (official && (official.policyURL === policy || official.trustDomain === template.trustDomain ||
    official.signingIdentity?.toLowerCase() === template.signingIdentity.toLowerCase() ||
    Object.values(official.publicKeys || {}).some(key => testKeys.some(testKey => testKey.equals(Buffer.from(key, "base64")))))) {
  throw new Error("Use separate Windows test and official signers, policy keys, trust domains and policy URLs");
}
function config(version) {
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version) || version.split(".").some(part => Number(part) > 65535)) {
    throw new Error("test_version must be a Windows package version, e.g. 0.5.301");
  }
  return JSON.parse(Buffer.from(prepareUpdateBuildConfig({ configuration: template, packageVersion: version,
    platform: "windows", arch: "amd64", signingMode: "authenticode" }), "base64"));
}

function checkRelease(directory) {
  const entries = fs.readdirSync(directory, { withFileTypes: true });
  if (entries.length !== 1 || !entries[0].isDirectory()) throw new Error("Expected exactly one Windows test release");
  const dir = path.join(directory, entries[0].name);
  if (fs.readdirSync(dir, { withFileTypes: true }).some(item => !item.isFile())) throw new Error("Unexpected nested Windows release data");
  const record = JSON.parse(fs.readFileSync(path.join(dir, "release.json"), "utf8"));
  const expected = config(record.config.version);
  if (!isDeepStrictEqual(record.config, expected) || expected.releaseId !== entries[0].name ||
      ["releaseId", "version", "platform", "arch", "signingIdentity", "updaterProtocol"].some(k => record.release[k] !== expected[k])) {
    throw new Error("Artifact does not match the configured test signer and policy");
  }
  for (const [kind, file] of [["package", "package.exe"], ["feed", "feed.json"], ["components", "components.json"]]) {
    if (record.release[kind]?.url !== base + "releases/" + expected.releaseId + "/" + file) throw new Error("Unexpected Windows test " + kind + " URL");
  }
}

switch (process.argv[2]) {
  case "configure": {
    const version = process.env.TEST_VERSION;
    config(version);
    const pkg = "desktop/electron/package.json";
    const data = JSON.parse(fs.readFileSync(pkg, "utf8"));
    fs.writeFileSync(pkg, JSON.stringify({ ...data, version }, null, 2) + "\n");
    fs.appendFileSync(process.env.GITHUB_ENV, `LAZYMIND_UPDATE_BUILD_CONFIG_JSON=${JSON.stringify(template)}\nLAZYMIND_UPDATE_BASE_URL=${base}\nLAZYMIND_PYTHON_COMPONENT_BASE_URL=${base}RAG/\n`);
    fs.appendFileSync(process.env.GITHUB_OUTPUT, `release_tag=\npackage_version=${version}\nartifact_version=${version}\n`);
    break;
  }
  case "base-url": config("1.0.0"); console.log(base); break;
  case "component-prefix": console.log(base.slice(dataset.length) + "RAG"); break;
  case "check-release": checkRelease(process.argv[3]); break;
  default: throw new Error("Unknown Windows test channel operation");
}
