import { describe, expect, it } from "vitest";
import { createUpstreamCatalogIndex } from "./upstreamCatalog";
import type { UpstreamCatalogFile } from "./upstreamCatalogIndex";

const fixture: UpstreamCatalogFile = {
  generated_at: "2026-01-01T00:00:00Z",
  providers: {
    "Fixture Provider": [
      {
        name: "fixture-model",
        input: ["text", "image", "audio", "video", "file"],
        output: ["text", "audio"],
        context: 32000,
        max_output: 4000,
      },
      { name: "shared-name", input: ["image"], output: ["text"], context: 1000, max_output: 100 },
    ],
    "Another Provider": [
      { name: "shared-name", input: ["text"], output: ["image"], context: 2000, max_output: 200 },
    ],
  },
};

describe("upstream model catalog", () => {
  it("leaves models unmarked when no generated snapshot is available", () => {
    const index = createUpstreamCatalogIndex();

    expect(index.lookupModel("Fixture Provider", "fixture-model")).toBeUndefined();
    expect(index.generatedAt).toBe("");
  });

  it("reads input and output modalities from a fixed snapshot", () => {
    const index = createUpstreamCatalogIndex(fixture);
    const model = index.lookupModel("Fixture Provider", "fixture-model");

    expect(model?.input).toEqual(["text", "image", "audio", "video", "file"]);
    expect(model?.output).toEqual(["text", "audio"]);
    expect(index.generatedAt).toBe("2026-01-01T00:00:00Z");
  });

  it("ignores case and surrounding whitespace in provider and model names", () => {
    const index = createUpstreamCatalogIndex(fixture);

    expect(index.lookupModel(" FIXTURE PROVIDER ", " FIXTURE-MODEL ")?.name).toBe("fixture-model");
  });

  it("prefers the named provider when multiple providers have the same model name", () => {
    const index = createUpstreamCatalogIndex(fixture);

    expect(index.lookupModel("Fixture Provider", "shared-name")?.output).toEqual(["text"]);
    expect(index.lookupModel("Another Provider", "shared-name")?.output).toEqual(["image"]);
  });

  it("uses a model from another provider only when its name is unambiguous", () => {
    const index = createUpstreamCatalogIndex(fixture);

    expect(index.lookupModel("Custom Provider", "fixture-model")?.input).toContain("file");
    expect(index.lookupModel("Custom Provider", "shared-name")).toBeUndefined();
    expect(index.lookupModel("Fixture Provider", "custom-model")).toBeUndefined();
  });
});
