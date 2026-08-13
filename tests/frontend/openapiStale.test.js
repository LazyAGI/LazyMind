import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";

import {
  GENERATED_TYPESCRIPT_FILES,
  inspectGeneratedClientOutput,
} from "../../frontend/scripts/openapi/generated-client-utils.mjs";
import {
  getOpenApiStatus,
  hashFile,
} from "../../frontend/scripts/openapi/openapi-manifest.mjs";

const temporaryDirectories = [];

afterEach(() => {
  for (const directory of temporaryDirectories.splice(0)) {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

function createFixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "openapi-stale-"));
  temporaryDirectories.push(root);
  const input = path.join(root, "core.yaml");
  const output = path.join(root, "core-client");
  fs.mkdirSync(output);
  fs.writeFileSync(input, "openapi: 3.0.0\n");
  for (const filename of GENERATED_TYPESCRIPT_FILES) {
    fs.writeFileSync(path.join(output, filename), `// ${filename}\n`);
  }

  const api = { name: "core", input, output };
  const cacheEntry = {
    specHash: hashFile(input),
    outputHash: inspectGeneratedClientOutput(output).hash,
  };
  return { api, cacheEntry };
}

describe("OpenAPI generated output staleness", () => {
  it("accepts matching spec and generated output hashes", () => {
    const { api, cacheEntry } = createFixture();

    expect(getOpenApiStatus(api, cacheEntry)).toMatchObject({
      stale: false,
      reasons: [],
      strictOutput: true,
    });
  });

  it("reports spec_changed when the authoritative spec changes", () => {
    const { api, cacheEntry } = createFixture();
    fs.appendFileSync(api.input, "info: {}\n");

    expect(getOpenApiStatus(api, cacheEntry).reasons).toEqual([
      "spec_changed",
    ]);
  });

  it("reports output_changed when a public generated file changes", () => {
    const { api, cacheEntry } = createFixture();
    fs.appendFileSync(path.join(api.output, "api.ts"), "// drift\n");

    expect(getOpenApiStatus(api, cacheEntry).reasons).toEqual([
      "output_changed",
    ]);
  });

  it("reports missing_output when a public generated file is absent", () => {
    const { api, cacheEntry } = createFixture();
    fs.rmSync(path.join(api.output, "index.ts"));

    expect(getOpenApiStatus(api, cacheEntry)).toMatchObject({
      stale: true,
      reasons: ["missing_output"],
      missingOutputFiles: ["index.ts"],
    });
  });

  it("keeps legacy string cache entries spec-only compatible", () => {
    const { api, cacheEntry } = createFixture();
    fs.rmSync(api.output, { recursive: true });

    expect(getOpenApiStatus(api, cacheEntry.specHash)).toMatchObject({
      stale: false,
      reasons: [],
      strictOutput: false,
    });
  });
});
