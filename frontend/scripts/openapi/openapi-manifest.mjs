import fs from "fs";
import path from "path";
import { createHash } from "crypto";

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

export function hashFile(filePath) {
  if (!fs.existsSync(filePath)) return "";
  const content = fs.readFileSync(filePath);
  return createHash("sha256").update(content).digest("hex");
}
