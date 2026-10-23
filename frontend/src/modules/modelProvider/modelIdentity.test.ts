import { describe, expect, it } from "vitest";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { getModelIdentity } from "./modelIdentity";

describe("model identity independent of supplier protocol", () => {
  it.each([
    ["gpt-4o", "OpenAI", "openai"],
    ["o3-mini", "OpenAI", "openai"],
    ["o1-preview", "OpenAI", "openai"],
    ["ft:gpt-4o-mini:team:model:123", "OpenAI", "openai"],
    ["openrouter/deepseek/deepseek-r1", "DeepSeek", "deepseek"],
    ["coding-glm-5.2-free", "GLM", "glm"],
    ["claude-sonnet-4-5", "Anthropic", "anthropic"],
    ["Qwen/Qwen3-235B-A22B-Instruct", "Qwen", "qwen"],
    ["doubao-seed-2-0-lite-260428", "ByteDance", "bytedance"],
    ["ByteDance-Seed/Seed-OSS-36B-Instruct", "ByteDance", "bytedance"],
    ["BAAI/bge-large-en-v1.5", "BAAI", "baai"],
    ["bge-m3", "BAAI", "baai"],
    ["hy3-preview", "Hunyuan", "hunyuan"],
    ["hy4-preview", "Hunyuan", "hunyuan"],
    ["tencent/Hunyuan-A13B-Instruct", "Hunyuan", "hunyuan"],
    ["meta-llama/llama-4-scout", "Meta", "meta"],
    ["mistralai/mistral-small-3.1", "Mistral", "mistral"],
    ["google/gemini-2.5-pro", "Google", "gemini"],
    ["microsoft/Phi-4-mini-instruct", "Microsoft", "microsoft"],
    ["moonshotai/kimi-k2", "Kimi", "kimi"],
    ["MiniMax-M2", "Minimax", "minimax"],
  ])(
    "identifies %s from its family rather than its gateway",
    (name, vendor, asset) => {
      expect(getModelIdentity(name, "OpenAI")).toEqual({
        vendor,
        logoUrl: `/provider-icons/${asset}.svg`,
      });
    },
  );

  it.each(["OpenAI", "Doubao", "AIhubmix"])(
    "uses a neutral icon for unknown models behind %s",
    (supplier) => {
      expect(getModelIdentity("my-private-model", supplier)).toEqual({
        vendor: supplier,
        logoUrl: "/provider-icons/custom.svg",
      });
    },
  );

  it("does not mistake a gateway prefix for the model author", () => {
    expect(getModelIdentity("openai/claude-sonnet-4").vendor).toBe("Anthropic");
    expect(getModelIdentity("doubao/gpt-4o").vendor).toBe("OpenAI");
    expect(getModelIdentity("openai/my-private-model").logoUrl).toBe(
      "/provider-icons/custom.svg",
    );
  });

  it("does not use loose short-token matches for unrelated model names", () => {
    expect(getModelIdentity("philosophy-helper").vendor).toBe("Custom");
    expect(getModelIdentity("some-hyphen-model").vendor).toBe("Custom");
    expect(getModelIdentity("research-meta-analysis").vendor).toBe("Custom");
  });

  it("bundles every returned icon locally without a remote image dependency", () => {
    for (const name of [
      "gpt-4o",
      "deepseek-r1",
      "claude-4",
      "qwen3",
      "glm-5",
      "seed-oss",
      "bge-m3",
      "hy4-preview",
      "llama-4",
      "mistral-small",
      "gemini-2.5",
      "gemma-3",
      "phi-4",
      "kimi-k2",
      "minimax-m2",
      "sensenova-v6",
      "private-model",
    ]) {
      const { logoUrl } = getModelIdentity(name);
      expect(logoUrl.startsWith("/provider-icons/")).toBe(true);
      const assetPath = resolve(dirname(fileURLToPath(import.meta.url)), "../../../public", logoUrl.slice(1));
      expect(existsSync(assetPath), assetPath).toBe(true);
    }
  });
});
