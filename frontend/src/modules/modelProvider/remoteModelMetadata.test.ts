import { beforeEach, describe, expect, it, vi } from "vitest";
import { resolveRemoteModelMetadata } from "./remoteModelMetadata";

const lookup = vi.hoisted(() => vi.fn());
vi.mock("./upstreamCatalog", () => ({ lookupUpstreamModel: lookup }));

describe("official model discovery evidence", () => {
  beforeEach(() => lookup.mockReturnValue({ model_type: "llm", input: ["text"], output: ["text"] }));

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
      id: "plain", name: "plain", model_type: "", added: false,
    }).modelType).toBe("llm");
  });
});
