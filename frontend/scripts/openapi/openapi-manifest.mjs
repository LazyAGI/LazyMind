import path from "path";
import {
  hashFile,
  inspectGeneratedClientOutput,
} from "./generated-client-utils.mjs";

export { hashFile } from "./generated-client-utils.mjs";

export function getOpenApiApis(cwdPath = process.cwd()) {
  const outputDirname = path.resolve(cwdPath, "src/api/generated");
  const localSpecsDir = path.resolve(cwdPath, "scripts/openapi/specs");

  return [
    {
      name: "auth",
      input: path.resolve(localSpecsDir, "auth-openapi.yaml"),
      output: path.resolve(outputDirname, "auth-client"),
    },
    {
      name: "core",
      input: path.resolve(localSpecsDir, "core.yaml"),
      output: path.resolve(outputDirname, "core-client"),
    },
    {
      name: "scan",
      input: path.resolve(localSpecsDir, "scan.yaml"),
      output: path.resolve(outputDirname, "scan-client"),
    },
    {
      name: "channel-gateway",
      input: path.resolve(localSpecsDir, "channel-gateway.yaml"),
      output: path.resolve(outputDirname, "channel-gateway-client"),
    },
  ];
}

export function getOpenApiCacheFilePath(cwdPath = process.cwd()) {
  return path.resolve(cwdPath, "scripts/openapi/.openapi-cache.json");
}

export function normalizeOpenApiCacheEntry(entry) {
  if (typeof entry === "string") {
    return { specHash: entry, outputHash: "", strictOutput: false };
  }

  if (entry && typeof entry === "object" && !Array.isArray(entry)) {
    return {
      specHash: typeof entry.specHash === "string" ? entry.specHash : "",
      outputHash: typeof entry.outputHash === "string" ? entry.outputHash : "",
      strictOutput: true,
    };
  }

  return { specHash: "", outputHash: "", strictOutput: false };
}

export function getOpenApiStatus(api, cacheEntry) {
  const cached = normalizeOpenApiCacheEntry(cacheEntry);
  const currentSpecHash = hashFile(api.input);
  const output = inspectGeneratedClientOutput(api.output);
  const reasons = [];

  if (!currentSpecHash) {
    reasons.push("missing_spec");
  } else if (currentSpecHash !== cached.specHash) {
    reasons.push("spec_changed");
  }

  if (cached.strictOutput) {
    if (output.missingFiles.length > 0) {
      reasons.push("missing_output");
    } else if (output.hash !== cached.outputHash) {
      reasons.push("output_changed");
    }
  }

  return {
    name: api.name,
    input: api.input,
    output: api.output,
    exists: Boolean(currentSpecHash),
    currentHash: currentSpecHash,
    cachedHash: cached.specHash,
    currentOutputHash: output.hash,
    cachedOutputHash: cached.outputHash,
    missingOutputFiles: output.missingFiles,
    strictOutput: cached.strictOutput,
    reasons,
    stale: reasons.length > 0,
  };
}
