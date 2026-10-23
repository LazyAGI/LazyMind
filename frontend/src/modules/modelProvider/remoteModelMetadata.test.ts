import { beforeEach, describe, expect, it, vi } from "vitest";
import { resolveRemoteModelMetadata } from "./remoteModelMetadata";

const lookup = vi.hoisted(() => vi.fn());
vi.mock("./upstreamCatalog", () => ({ lookupUpstreamModel: lookup }));

describe("official model discovery evidence", () => {
  beforeEach(() => lookup.mockReturnValue({ model_type: "llm", input: ["text"], output: ["text"] }));

  it("preserves explicit feature support over catalog evidence without inventing enabled flags", () => {
    lookup.mockReturnValue({ model_type: "llm", input: ["text"], output: ["text"], feature_support: { tool_calling: true, reasoning: true } });
    expect(resolveRemoteModelMetadata("OpenAI", {
      id: "plain", name: "plain", model_type: "llm", added: false,
      model_settings: { feature_support: { tool_calling: false } },
    }).modelSettings).toEqual({ feature_support: { tool_calling: false } });
  });

  it("fills feature support for a legacy ID-only discovery response", () => {
    lookup.mockReturnValue({ model_type: "llm", input: ["text"], output: ["text"], feature_support: { tool_calling: true, reasoning: false } });
    expect(resolveRemoteModelMetadata("OpenAI", { id: "plain", name: "plain", added: false }).modelSettings).toEqual({ feature_support: { tool_calling: true, reasoning: false } });
  });

  it("does not attach chat-only feature settings to Core's unclassified result", () => {
    lookup.mockReturnValue({ model_type: "llm", input: ["text"], output: ["text"], feature_support: { tool_calling: true } });
    expect(resolveRemoteModelMetadata("OpenAI", { id: "plain", name: "plain", model_type: "", added: false }).modelSettings).toBeUndefined();
  });

  it.each([["embedding"], ["chat", "reranker"]])("does not replace unresolved official types %j with a catalog guess", (...subTypes) => {
    expect(resolveRemoteModelMetadata("SiliconFlow", {
      id: "ambiguous", name: "ambiguous", model_type: "", added: false, sub_types: subTypes,
    }).modelType).toBe("");
  });

  it("retains a type resolved from additional embedding evidence by Core", () => {
    expect(resolveRemoteModelMetadata("SiliconFlow", {
      id: "joint", name: "joint", model_type: "multimodal_embedding", added: false, sub_types: ["embedding"],
    }).modelType).toBe("multimodal_embedding");
  });

  it("still fills an ID-only discovery response without official classification evidence", () => {
    expect(resolveRemoteModelMetadata("SiliconFlow", {
      id: "plain", name: "plain", added: false,
    }).modelType).toBe("llm");
  });

  it("preserves Core's explicit unclassified result even when the catalog recognizes the ID", () => {
    expect(resolveRemoteModelMetadata("vLLM", {
      id: "qwen38-27b", name: "qwen38-27b", model_type: "", added: false,
    }).modelType).toBe("");
  });

  it.each(["unknown", "future-task", "audio"])("stores unsupported declared task %s as unclassified without borrowing a catalog type", (modelType) => {
    expect(resolveRemoteModelMetadata("vLLM", {
      id: "private", name: "private", model_type: modelType, added: false,
    }).modelType).toBe("");
  });
});
