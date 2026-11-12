#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { normalizeReleaseTag } from "./resolve-release-version.mjs";

export function assertUniqueReleaseVersion(tag, releases) {
  const candidate = normalizeReleaseTag(tag);
  if (!Array.isArray(releases) || releases.some((release) => typeof release?.tag_name !== "string")) {
    throw new Error("Invalid release list; cannot establish version uniqueness");
  }
  for (const release of releases) {
    let existing;
    try {
      existing = normalizeReleaseTag(release.tag_name);
    } catch {
      // This repository may also publish unrelated server or component tags.
      continue;
    }
    if (existing.packageVersion === candidate.packageVersion && release.tag_name !== candidate.releaseTag) {
      throw new Error(`Package version ${candidate.packageVersion} already belongs to ${release.tag_name}`);
    }
  }
}

function main(args) {
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    if (!["--tag", "--repository"].includes(args[index]) || !args[index + 1]) {
      throw new Error("Usage: validate-release-identity.mjs --tag <tag> --repository <owner/repo>");
    }
    options[args[index]] = args[index + 1];
  }
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(options["--repository"] || "")) {
    throw new Error("A repository owner/name is required");
  }
  normalizeReleaseTag(options["--tag"]);
  const pages = JSON.parse(execFileSync("gh", ["api", "--paginate", "--slurp",
    `repos/${options["--repository"]}/releases?per_page=100`], { encoding: "utf8", maxBuffer: 10 * 1024 * 1024 }));
  if (!Array.isArray(pages) || pages.some((page) => !Array.isArray(page))) {
    throw new Error("Invalid paginated release list");
  }
  assertUniqueReleaseVersion(options["--tag"], pages.flat());
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  main(process.argv.slice(2));
}
