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
  const record = (name: string, model_type = "llm") => ({
    name, model_type, input: ["text"], output: ["text"], context: 1000, max_output: 100,
  });

  it("does not declare a feature supported when matching sources disagree", () => {
    const index = createUpstreamCatalogIndex({ generated_at: "fixture", providers: {}, recognition_models: [
      { ...record("vendor-a/model-v1"), feature_support: { tool_calling: true, reasoning: true } },
      { ...record("vendor-b/model-v1"), feature_support: { tool_calling: false, reasoning: true } },
    ] });
    expect(index.lookupModel("Custom", "gateway/model-v1")?.feature_support).toEqual({ reasoning: true });
  });

  it("recognizes IDs from the full recognition catalog without adding provider entries", () => {
    const index = createUpstreamCatalogIndex({
      generated_at: "fixture", providers: {},
      recognition_models: [record("vendor-a/vector-v2", "embed")],
    });
    expect(index.lookupModel("Custom", " VENDOR-A/VECTOR-V2 ")?.model_type).toBe("embed");
    expect(index.lookupModel("Custom", "another-gateway/vector-v2")?.model_type).toBe("embed");
  });

  it("accepts matching nonempty types across sources and keeps provider-scoped metadata first", () => {
    const scoped = record("shared", "tts");
    const index = createUpstreamCatalogIndex({
      generated_at: "fixture", providers: { Local: [scoped] },
      recognition_models: [record("shared", "tts"), record(" SHARED ", " TTS "), record("shared", "")],
    });
    expect(index.lookupModel("LOCAL", "shared")).toBe(scoped);
    expect(index.lookupModel("Custom", "shared")?.model_type?.trim().toLowerCase()).toBe("tts");
  });

  it("does not choose an arbitrary type for conflicting exact or basename matches", () => {
    const index = createUpstreamCatalogIndex({
      generated_at: "fixture", providers: { Local: [record("same", "embed"), record("same", "tts")] },
      recognition_models: [record("same", "embed"), record("vendor-a/shared", "llm"), record("vendor-b/shared", "embed")],
    });
    expect(index.lookupModel("Local", "same")).toBeUndefined();
    expect(index.lookupModel("Custom", "same")).toBeUndefined();
    expect(index.lookupModel("Custom", "gateway/shared")).toBeUndefined();
    expect(index.lookupModel("Custom", "vendor-a/shared")?.model_type).toBe("llm");
  });

  it("recognizes canonical and legacy aliases as the same type across catalog sources", () => {
    const index = createUpstreamCatalogIndex({
      generated_at: "fixture", providers: {},
      recognition_models: [
        record("vendor-a/vector", "multimodal_embedding"),
        record("vendor-b/vector", "cross_modal_embed"),
        record("vendor-c/vector", " EMBED_IMAGE "),
        record("ranker", "rerank"),
        record("ranker", "reranker"),
      ],
    });
    expect(index.lookupModel("Custom", "gateway/vector")?.model_type).toBe("multimodal_embedding");
    expect(index.lookupModel("Custom", "ranker")?.model_type).toBe("rerank");
  });

  it("only removes path prefixes and never strips versions, variants or suffixes", () => {
    const index = createUpstreamCatalogIndex({
      generated_at: "fixture", providers: {},
      recognition_models: [record("vendor/model-v1:free", "embed"), record("vendor/model-v2", "tts")],
    });
    expect(index.lookupModel("Custom", "gateway/model-v1:free")?.model_type).toBe("embed");
    for (const name of ["model-v1", "model-v1:paid", "model", "model-v3", "prefix-model-v2"])
      expect(index.lookupModel("Custom", name)).toBeUndefined();
  });

  it("recognizes separator variants after exact IDs without borrowing catalog token limits", () => {
    const index = createUpstreamCatalogIndex({
      generated_at: "fixture", providers: {},
      recognition_models: [record("Qwen/Qwen3.8-27B"), record("vendor/bge_reranker-v2.m3", "rerank")],
    });
    const model = index.lookupModel("vLLM", "deployment/qwen38_27b");
    expect(model?.model_type).toBe("llm");
    expect(model?.context).toBe(0);
    expect(model?.max_output).toBe(0);
    expect(index.lookupModel("Custom", "BGE-RERANKER_v2m3")?.model_type).toBe("rerank");
    for (const name of ["qwen38-8b", "qwen36-27b", "prefix-qwen38-27b", "qwen38-27b:free"])
      expect(index.lookupModel("Custom", name)).toBeUndefined();
  });

  it("keeps explicit unknowns and conflicting separator matches unresolved", () => {
    const index = createUpstreamCatalogIndex({
      generated_at: "fixture", providers: { Local: [record("Qwen38-27B", "")] },
      recognition_models: [
        record("Qwen/Qwen3.8-27B"),
        record("vendor/ranker-v2", "rerank"), record("other/ranker_v2", "embed"),
      ],
    });
    expect(index.lookupModel("Local", "Qwen38-27B")?.model_type).toBe("");
    expect(index.lookupModel("Custom", "rankerv2")).toBeUndefined();
  });

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
