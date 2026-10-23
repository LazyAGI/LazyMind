import { describe, expect, it } from "vitest";
import { lookupUpstreamModel, modelModalities } from "./upstreamCatalog";

describe("upstream model catalog", () => {
  it("keeps a modality logo color for every input and output kind", () => {
    expect(modelModalities).toEqual(["text", "image", "audio", "video", "file"]);
  });

  it("reads Claude input and output modalities from the bundled catalog", () => {
    const model = lookupUpstreamModel("Claude", "claude-opus-4-7");
    expect(model?.input).toEqual(expect.arrayContaining(["text", "image"]));
    expect(model?.output).toContain("text");
  });

  it("matches a model name without depending on provider-name case", () => {
    const model = lookupUpstreamModel("openai", "GPT-4o");
    expect(model?.name.toLowerCase()).toBe("gpt-4o");
    expect(model?.input.length).toBeGreaterThan(0);
    expect(model?.output.length).toBeGreaterThan(0);
  });

  it("leaves an unknown custom model unmarked", () => {
    expect(lookupUpstreamModel("OpenAI", "not-a-real-model")).toBeUndefined();
  });
});
