import { describe, expect, it } from "vitest";
import { getProviderLogoUrl } from "./providerBranding";

describe("model provider branding", () => {
  it.each([
    ["Qwen", "/provider-icons/qwen.svg"],
    ["SiliconFlow", "/provider-icons/siliconflow.svg"],
    ["SenseNova", "/provider-icons/sensenova.svg"],
    ["OpenAI", "/provider-icons/openai.svg"],
    ["Anthropic", "/provider-icons/anthropic.svg"],
    ["DeepSeek", "/provider-icons/deepseek.svg"],
    ["Doubao", "/provider-icons/doubao.svg"],
    ["GLM", "/provider-icons/glm.svg"],
    ["Kimi", "/provider-icons/kimi.svg"],
    ["MiniMax", "/provider-icons/minimax.svg"],
    ["OpenRouter", "/provider-icons/openrouter.svg"],
    ["vLLM", "/provider-icons/vllm.svg"],
    ["LMDeploy", "/provider-icons/lmdeploy.svg"],
  ])("maps %s to a bundled provider icon", (provider, expected) => {
    expect(getProviderLogoUrl(provider)).toBe(expected);
  });

  it("does not invent an icon for unknown providers", () => {
    expect(getProviderLogoUrl("Custom Provider")).toBeUndefined();
  });

  it.each(["OpenAI", "AIhubmix", "Doubao"])(
    "uses the same neutral icon for custom supplier %s",
    (name) => {
      expect(getProviderLogoUrl(name, true)).toBe("/provider-icons/custom.svg");
    },
  );
});
