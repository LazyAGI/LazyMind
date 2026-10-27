import { describe, expect, it } from "vitest";
import {
  getSelectableCapabilities,
  isModelTypeSupportedByProtocol,
  supportsModelContext,
  normalizeRuntimeModelType,
} from "./modelCapabilities";

describe("supplier model capabilities", () => {
  it.each(["DeepSeek", "Kimi", "Moonshot", "Claude", "Anthropic", "PPIO"])("only offers registered chat adapters for %s", (protocol) => {
    expect(getSelectableCapabilities(["LLM_CHAT", "VLM", "EMBEDDING", "RERANK", "TTS", "TEXT_TO_IMAGE"], protocol))
      .toEqual(["LLM_CHAT", "VLM"]);
  });
  it("exposes image and video generation adapters for OpenAI", () => {
    expect(getSelectableCapabilities(["EMBEDDING", "RERANK", "MULTIMODAL_EMBEDDING", "TEXT_TO_IMAGE", "TEXT_TO_VIDEO", "TTS", "ASR"], "OpenAI"))
      .toEqual(["EMBEDDING", "RERANK", "MULTIMODAL_EMBEDDING", "TEXT_TO_IMAGE", "TEXT_TO_VIDEO"]);
  });
  it.each(["vLLM", "LMDeploy"])("keeps compatible embedding/reranking but excludes media adapters for %s", (protocol) => {
    expect(getSelectableCapabilities(["EMBEDDING", "RERANK", "MULTIMODAL_EMBEDDING", "TEXT_TO_IMAGE", "TEXT_TO_VIDEO", "TTS", "ASR"], protocol))
      .toEqual(["EMBEDDING", "RERANK", "MULTIMODAL_EMBEDDING"]);
  });
  it.each(["vLLM", "LMDeploy"])("keeps local supplier identity restricted when its active protocol is OpenAI (%s)", (identity) => {
    expect(isModelTypeSupportedByProtocol("text2image", "OpenAI", identity)).toBe(false);
    expect(isModelTypeSupportedByProtocol("text2video", "OpenAI", identity)).toBe(false);
    expect(isModelTypeSupportedByProtocol("embed", "OpenAI", identity)).toBe(true);
  });
  it("rejects unknown supplier adapters", () => {
    expect(isModelTypeSupportedByProtocol("llm", "UnknownProvider")).toBe(false);
  });
  it("only enables the OpenRouter adapters implemented by the runtime", () => {
    for (const type of ["llm", "vlm", "embed", "text2image", "text2video", "tts", "stt"])
      expect(isModelTypeSupportedByProtocol(type, "OpenRouter")).toBe(true);
    for (const type of ["rerank", "image_editing", "multimodal_embedding", "cross_modal_embed", "embed_image", ""])
      expect(isModelTypeSupportedByProtocol(type, "OpenRouter")).toBe(false);
    expect(getSelectableCapabilities(["LLM_CHAT", "EMBEDDING", "RERANK", "IMAGE_EDITING", "MULTIMODAL_EMBEDDING"], "OpenRouter"))
      .toEqual(["LLM_CHAT", "EMBEDDING"]);
  });
  it("offers GLM hosted video and speech without inventing an image editing endpoint", () => {
    expect(getSelectableCapabilities(["TEXT_TO_IMAGE", "TEXT_TO_VIDEO", "TTS", "ASR", "IMAGE_EDITING"], "GLM"))
      .toEqual(["TEXT_TO_IMAGE", "TEXT_TO_VIDEO", "TTS", "ASR"]);
    expect(isModelTypeSupportedByProtocol("text2video", "Zhipu AI")).toBe(true);
    expect(isModelTypeSupportedByProtocol("image_editing", "GLM")).toBe(false);
  });
  it("normalizes declared aliases before sending the real runtime type", () => {
    expect(normalizeRuntimeModelType("ASR")).toBe("stt");
    expect(normalizeRuntimeModelType("embed_main")).toBe("embed");
    expect(normalizeRuntimeModelType("text_to_image")).toBe("text2image");
    expect(normalizeRuntimeModelType("cross_modal_embed")).toBe("multimodal_embedding");
    expect(normalizeRuntimeModelType(" EMBED_IMAGE ")).toBe("multimodal_embedding");
    expect(normalizeRuntimeModelType("reranker")).toBe("rerank");
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
