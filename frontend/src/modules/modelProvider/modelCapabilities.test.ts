import { describe, expect, it } from "vitest";
import {
  getSelectableCapabilities,
  isModelTypeSupportedByProtocol,
  supportsModelContext,
  normalizeRuntimeModelType,
} from "./modelCapabilities";

describe("supplier model capabilities", () => {
  it("only enables the OpenRouter adapters implemented by the runtime", () => {
    for (const type of ["llm", "vlm", "embed", "text2image", "text2video", "tts", "stt"])
      expect(isModelTypeSupportedByProtocol(type, "OpenRouter")).toBe(true);
    for (const type of ["rerank", "image_editing", "multimodal_embedding", "cross_modal_embed", "embed_image", ""])
      expect(isModelTypeSupportedByProtocol(type, "OpenRouter")).toBe(false);
    expect(getSelectableCapabilities(["LLM_CHAT", "EMBEDDING", "RERANK", "IMAGE_EDITING", "MULTIMODAL_EMBEDDING"], "OpenRouter"))
      .toEqual(["LLM_CHAT", "EMBEDDING"]);
  });
  it("normalizes declared aliases before sending the real runtime type", () => {
    expect(normalizeRuntimeModelType("ASR")).toBe("stt");
    expect(normalizeRuntimeModelType("embed_main")).toBe("embed");
    expect(normalizeRuntimeModelType("text_to_image")).toBe("text2image");
    expect(normalizeRuntimeModelType("vendor-mystery")).toBe("vendor-mystery");
  });
  it("keeps runtime subtypes distinct while merging the evolution role", () => {
    expect(getSelectableCapabilities([
      "LLM_CHAT", "LLM_SELF_EVOLUTION", "VLM", "EMBEDDING", "MULTIMODAL_EMBEDDING", "RERANK",
    ], "OpenAI")).toEqual(["LLM_CHAT", "VLM", "EMBEDDING", "MULTIMODAL_EMBEDDING", "RERANK"]);
  });
  it.each(["Claude", "anthropic", " ANTHROPIC "])("offers only dialogue subtypes for %s", (protocol) => {
    expect(getSelectableCapabilities(["LLM_CHAT", "VLM", "EMBEDDING", "RERANK", "TTS"], protocol))
      .toEqual(["LLM_CHAT", "VLM"]);
    expect(isModelTypeSupportedByProtocol("embed", protocol)).toBe(false);
    expect(isModelTypeSupportedByProtocol("vlm", protocol)).toBe(true);
  });
  it("does not silently classify unknown runtime types as chat", () => {
    expect(isModelTypeSupportedByProtocol("vendor-mystery", "OpenAI")).toBe(false);
    expect(isModelTypeSupportedByProtocol("", "OpenAI")).toBe(false);
    expect(isModelTypeSupportedByProtocol("multimodal_embedding", "OpenAI")).toBe(true);
  });
  it("allows explicit context configuration for text and visual dialogue only", () => {
    expect(supportsModelContext("LLM_CHAT")).toBe(true);
    expect(supportsModelContext("VLM")).toBe(true);
    expect(supportsModelContext("EMBEDDING")).toBe(false);
  });
});
