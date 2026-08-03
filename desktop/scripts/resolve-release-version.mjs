#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export function normalizeReleaseTag(tag) {
  const trimmed = String(tag || "").trim();
  const match = trimmed.match(
    /^v?(\d+)\.(\d+)\.(\d+)(?:(a|b|rc)(\d+)|-(alpha|beta|rc)\.(\d+))?$/i,
  );
  if (!match) {
    throw new Error(
      `Unsupported release tag ${JSON.stringify(trimmed)}; expected vMAJOR.MINOR.PATCH, vMAJOR.MINOR.PATCHaN, bN, rcN, or a standard alpha/beta/rc suffix`,
    );
  }

  const base = `${match[1]}.${match[2]}.${match[3]}`;
  const compactChannel = match[4]?.toLowerCase();
  const channel = match[6]?.toLowerCase()
    || ({ a: "alpha", b: "beta", rc: "rc" })[compactChannel];
  const sequence = match[5] || match[7];
  const packageVersion = channel ? `${base}-${channel}.${sequence}` : base;
  return {
    releaseTag: `v${trimmed.replace(/^v/i, "")}`,
    packageVersion,
    artifactVersion: packageVersion,
  };
}

function parseArguments(argv) {
  const result = {};
  for (let index = 0; index < argv.length; index += 1) {
    const name = argv[index];
    if (!["--tag", "--package", "--github-output"].includes(name)) {
      throw new Error(`Unknown argument ${name}`);
    }
    index += 1;
    if (index >= argv.length || !argv[index]) {
      throw new Error(`${name} requires a value`);
    }
    result[name.slice(2)] = argv[index];
  }
  if (!result.tag || !result.package) {
    throw new Error("Usage: resolve-release-version.mjs --tag <tag> --package <package.json> [--github-output <path>]");
  }
  return result;
}

function main(argv) {
  const options = parseArguments(argv);
  const metadata = normalizeReleaseTag(options.tag);
  const packagePath = path.resolve(options.package);
  const packageJson = JSON.parse(fs.readFileSync(packagePath, "utf8"));
  packageJson.version = metadata.packageVersion;
  fs.writeFileSync(packagePath, `${JSON.stringify(packageJson, null, 2)}\n`);

  const output = [
    `release_tag=${metadata.releaseTag}`,
    `package_version=${metadata.packageVersion}`,
    `artifact_version=${metadata.artifactVersion}`,
  ].join("\n");
  if (options["github-output"]) {
    fs.appendFileSync(options["github-output"], `${output}\n`);
  }
  process.stdout.write(`${output}\n`);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  main(process.argv.slice(2));
}
