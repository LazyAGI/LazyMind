#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";
import { isDeepStrictEqual } from "node:util";

const fields = new Set(["trustDomain", "audience", "channel", "mode", "releaseId", "version", "platform", "arch", "signingIdentity", "updaterProtocol", "policyURL", "allowedOrigins", "publicKeys"]);

export function prepareUpdateBuildConfig({ configuration, packageVersion, platform, arch, signingMode, allowUnsignedTest = false }) {
  if (configuration === undefined) return "";
  if (!configuration || Array.isArray(configuration) || typeof configuration !== "object") throw new Error("Invalid public update configuration");
  for (const key of Object.keys(configuration)) if (!fields.has(key)) throw new Error(`Unknown public update configuration field: ${key}`);
  for (const [field, value] of Object.entries({ version: packageVersion, platform, arch })) {
    if (configuration[field] !== undefined && configuration[field] !== value) throw new Error(`Update ${field} mismatch with the actual package`);
  }
  if (!packageVersion || !["darwin/arm64", "darwin/amd64", "windows/amd64"].includes(`${platform}/${arch}`)) throw new Error("Invalid update build target");
  if (configuration.mode === "official") {
    if (configuration.audience !== "official") throw new Error("Official update audience must be official");
    const required = platform === "darwin" ? "developer-id" : "authenticode";
    if (signingMode !== required) throw new Error(`Official updates require ${required} signing`);
    const prerelease = packageVersion.split("+")[0].split("-")[1];
    if (!["stable", "beta", "canary"].includes(configuration.channel)
      || (configuration.channel === "stable" && prerelease)
      || (configuration.channel === "beta" && prerelease && !/^(beta|rc)(\.|$)/.test(prerelease))) throw new Error("Package version does not match its update channel");
  } else if (configuration.mode === "unsigned-test") {
    if (allowUnsignedTest !== true) throw new Error("Unsigned update testing requires explicit enablement");
    if (!configuration.audience || configuration.audience === "official") throw new Error("Unsigned tests require an isolated audience");
    if (configuration.channel !== "personal-test") throw new Error("Unsigned tests require the personal-test channel");
    if (platform === "darwin" && !["adhoc", "developer-id"].includes(signingMode)) throw new Error("macOS test updates still require ad-hoc signing");
  } else throw new Error("Invalid update mode");
  if (!configuration.publicKeys || Array.isArray(configuration.publicKeys) || typeof configuration.publicKeys !== "object" || !Object.keys(configuration.publicKeys).length) throw new Error("Update public keys are required");
  for (const value of Object.values(configuration.publicKeys)) {
    if (typeof value !== "string" || !/^[A-Za-z0-9+/]{43}=$/.test(value) || Buffer.from(value, "base64").length !== 32) throw new Error("Only raw Ed25519 public keys may be embedded");
  }
  // The compiled helper's config command performs the authoritative full
  // validation before the build proceeds to packaging or signing.
  return Buffer.from(JSON.stringify({ ...configuration, version: packageVersion, platform, arch })).toString("base64");
}

export function verifyStagedUpdateConfig(encoded, staged) {
  const expected = encoded ? { status: "enabled", config: JSON.parse(Buffer.from(encoded, "base64").toString("utf8")) }
    : { status: "disabled", reason: "not_configured" };
  if (!isDeepStrictEqual(expected, staged)) throw new Error("Staged update helper configuration mismatch; run a full build before resuming");
}

function main(args) {
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    if (!["--platform", "--arch", "--signing-mode", "--package", "--verify-helper"].includes(args[index]) || !args[index + 1]) throw new Error("Invalid update build arguments");
    options[args[index]] = args[index + 1];
  }
  let configuration, packageJson;
  try {
    packageJson = JSON.parse(fs.readFileSync(options["--package"], "utf8"));
    if (process.env.LAZYMIND_DESKTOP_UPDATE_CONFIG) configuration = JSON.parse(fs.readFileSync(process.env.LAZYMIND_DESKTOP_UPDATE_CONFIG, "utf8"));
  } catch { throw new Error("Unable to read package or public update configuration"); }
  const encoded = prepareUpdateBuildConfig({ configuration, packageVersion: packageJson.version,
    platform: options["--platform"], arch: options["--arch"], signingMode: options["--signing-mode"],
    allowUnsignedTest: process.env.LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST === "true" });
  if (options["--verify-helper"]) {
    let staged;
    try {
      staged = JSON.parse(execFileSync(options["--verify-helper"], ["config"], {
        encoding: "utf8", timeout: 35000, maxBuffer: 2 * 1024 * 1024, windowsHide: true, stdio: ["ignore", "pipe", "pipe"],
      }));
    } catch { throw new Error("Unable to validate staged update helper; run a full build before resuming"); }
    verifyStagedUpdateConfig(encoded, staged);
  } else process.stdout.write(`${encoded}\n`);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) main(process.argv.slice(2));
