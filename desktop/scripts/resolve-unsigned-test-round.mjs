import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { isDeepStrictEqual } from "node:util";
import unsigned from "./unsigned-update-build.cjs";
import { prepareUpdateBuildConfig } from "./prepare-update-build-config.mjs";

const runID = /^[1-9][0-9]{0,19}$/;

function roundConfig(template, base, id) {
  const digest = createHash("sha256").update(JSON.stringify([template.trustDomain, base, id])).digest("hex");
  return { ...template, policyURL: new URL(`round-${id}/policy.json`, base).href, trustDomain: `round-${id}-${digest}` };
}

function roundLocation(template, base, config) {
  if (config.policyURL === template.policyURL && config.trustDomain === template.trustDomain) {
    return { baseURL: base }; // Legacy assets use the configured base, including when the policy is nested.
  }
  const relative = config.policyURL.startsWith(base) && config.policyURL.slice(base.length);
  const match = /^round-([1-9][0-9]{0,19})\/policy\.json$/.exec(relative);
  if (!match || config.trustDomain === template.trustDomain) throw new Error("Source policy is outside the configured test round");
  return { id: match[1], baseURL: base + `round-${match[1]}/` };
}

function readBaseline(directory) {
  const records = fs.readdirSync(directory, { withFileTypes: true }).filter(entry => entry.isDirectory())
    .map(entry => path.join(directory, entry.name, "release.json"))
    .filter(file => fs.lstatSync(file, { throwIfNoEntry: false })?.isFile());
  if (records.length !== 1 || fs.statSync(records[0]).size > 1024 * 1024) {
    throw new Error("Expected one macOS test release record in the source build artifact");
  }
  const { config, release } = JSON.parse(fs.readFileSync(records[0], "utf8"));
  if (config.platform !== "darwin" || config.arch !== "arm64" || config.releaseId !== path.basename(path.dirname(records[0])) ||
      ["releaseId", "version", "platform", "arch", "signingIdentity", "updaterProtocol"].some(key => config[key] !== release[key])) {
    throw new Error("Source release identity does not match the macOS test artifact");
  }
  return { config: unsigned.validateUnsignedTestConfig(config), release };
}

function resolveBuild() {
  const template = unsigned.validateUnsignedTestConfig(JSON.parse(process.env.LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON));
  const base = unsigned.unsignedTestBase(process.env.LAZYMIND_UPDATE_BASE_URL, template);
  const run = process.env.GITHUB_RUN_ID;
  if (!runID.test(run)) throw new Error("Invalid GitHub run ID");
  if (!process.env.TEST_SOURCE_RUN) return { config: roundConfig(template, base, run), baseURL: base + `round-${run}/` };
  const locator = /^published:(legacy|[1-9][0-9]{0,19})$/.exec(process.env.TEST_SOURCE_RUN);
  if (locator) {
    const id = locator[1];
    return id === "legacy" ? { config: template, baseURL: base }
      : { config: roundConfig(template, base, id), baseURL: base + `round-${id}/` };
  }
  if (!runID.test(process.env.TEST_SOURCE_RUN)) throw new Error("Invalid source run or public baseline locator");

  // The native verification job independently checks this record against signed public history.
  const { config, release } = readBaseline(path.join(process.env.RUNNER_TEMP, "unsigned-test-source"));
  const { releaseId, version, platform, arch, ...identity } = config;
  const { id, baseURL } = roundLocation(template, base, identity);
  const expected = id ? roundConfig(template, base, id) : template;
  if (!isDeepStrictEqual(identity, expected)) throw new Error("Source release has a different test trust configuration");
  for (const [kind, file] of [["package", "package.zip"], ["feed", "feed.json"], ["components", "components.json"]]) {
    if (release[kind]?.url !== baseURL + `releases/${releaseId}/${file}`) throw new Error("Source release files belong to a different test round");
  }
  if (!/^\d+\.\d+\.\d+$/.test(version) || process.env.TEST_VERSION.localeCompare(version, "en", { numeric: true }) <= 0) {
    throw new Error("Test version must be newer than the source release");
  }
  return { config: identity, baseURL };
}

if (process.argv[2] === "--component-prefix") {
  const template = unsigned.validateUnsignedTestConfig(JSON.parse(process.env.LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON));
  const config = unsigned.validateUnsignedTestConfig(JSON.parse(fs.readFileSync(process.argv[3], "utf8")));
  const base = unsigned.unsignedTestBase(process.env.TEST_BASE_URL, template);
  const { baseURL } = roundLocation(template, base, config);
  const repository = `https://modelscope.cn/datasets/${process.env.UPDATE_REPO}/resolve/master/`;
  if (!baseURL.startsWith(repository)) throw new Error("Signed test policy does not belong to the publication dataset");
  process.stdout.write(baseURL.slice(repository.length) + "RAG");
} else if (process.argv[2] === "--target-config") {
  const { config } = resolveBuild();
  const encoded = prepareUpdateBuildConfig({ configuration: config, packageVersion: process.env.TEST_VERSION,
    platform: "darwin", arch: "arm64", signingMode: "adhoc", isolatedTestBuild: true });
  process.stdout.write(Buffer.from(encoded, "base64"));
} else {
  process.stdout.write(JSON.stringify(resolveBuild()));
}
