import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  availableModelTaskTypes,
  getModelTaskDescriptionKey,
  getModelTaskLabelKey,
  getModelTaskType,
} from "./modelTaskTypes";

const mappings = [
  ["LLM_CHAT", "chat"],
  ["LLM_SELF_EVOLUTION", "chat"],
  ["llm", "chat"],
  ["VLM", "chat"],
  ["vlm", "chat"],
  ["EMBEDDING", "embedding"],
  ["MULTIMODAL_EMBEDDING", "embedding"],
  ["embed", "embedding"],
  ["multimodal_embedding", "embedding"],
  ["cross_modal_embed", "embedding"],
  ["embed_image", "embedding"],
  ["embed_main", "embedding"],
  ["RERANK", "rerank"],
  ["rerank", "rerank"],
  ["ASR", "asr"],
  ["asr", "asr"],
  ["stt", "asr"],
  ["TTS", "tts"],
  ["tts", "tts"],
  ["TEXT_TO_IMAGE", "image-generation"],
  ["text2image", "image-generation"],
  ["IMAGE_EDITING", "image-edit"],
  ["image_editing", "image-edit"],
  ["TEXT_TO_VIDEO", "video-generation"],
  ["text2video", "video-generation"],
] as const;

describe("model task types", () => {
  it.each(mappings)(
    "maps the declared type %s to task %s",
    (value, expected) => {
      expect(getModelTaskType(value)).toBe(expected);
    },
  );

  it("normalizes casing and surrounding whitespace without guessing from names or modalities", () => {
    expect(getModelTaskType(" VLM ")).toBe("chat");
    for (const value of [
      undefined,
      "",
      "gpt-4o",
      "image-text-to-text",
      "audio",
      "embedding-model",
      "my-llm",
      "realtime",
      "document",
      "moderation",
    ])
      expect(getModelTaskType(value)).toBe("unknown");
  });

  it("only offers the eight configured task types, keeping planned integrations out of selection", () => {
    expect(availableModelTaskTypes).toEqual([
      "chat",
      "embedding",
      "rerank",
      "asr",
      "tts",
      "image-generation",
      "image-edit",
      "video-generation",
    ]);
    expect(getModelTaskLabelKey("image-edit")).toBe(
      "modelProvider.taskType.image-edit",
    );
    expect(getModelTaskDescriptionKey("unknown")).toBe(
      "modelProvider.taskTypeDescription.unknown",
    );
  });

  it("ships all task logos with their own solid backgrounds and white glyphs", () => {
    const assetRoot = resolve(
      dirname(fileURLToPath(import.meta.url)),
      "../../../public/model-type-icons",
    );
    for (const type of [
      ...availableModelTaskTypes,
      "realtime",
      "document",
      "moderation",
      "unknown",
    ]) {
      const svg = readFileSync(resolve(assetRoot, `${type}.svg`), "utf8");
      expect(svg).toContain('viewBox="0 0 24 24"');
      expect(svg).toMatch(/<rect[^>]*fill="#[0-9A-Fa-f]{6}"/);
      expect(svg).toContain('stroke="#FFFFFF"');
      expect(svg).not.toMatch(/<script|<image|href=/i);
    }
  });
});
