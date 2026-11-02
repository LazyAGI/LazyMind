import { describe, expect, it } from "vitest";
import { getProviderProfileContent } from "./providerProfileContent";
import { getSupplierBrandKey, resolveSupplierService } from "./supplierServices";

const leaves = [
  ["Claude", "https://www.anthropic.com/"],
  ["DeepSeek", "https://www.deepseek.com/"],
  ["Doubao", "https://www.volcengine.com/product/ark"],
  ["DoubaoCodingPlan", "https://www.volcengine.com/product/ark"],
  ["DoubaoAgentPlan", "https://www.volcengine.com/product/ark"],
  ["SenseNova", "https://www.sensenova.cn/"],
  ["SenseNovaTokenPlan", "https://www.sensenova.cn/"],
  ["Qwen", "https://www.aliyun.com/product/bailian"],
  ["QwenCodingPlan", "https://www.aliyun.com/product/bailian"],
  ["QwenTokenPlan", "https://www.aliyun.com/product/bailian"],
  ["QwenTokenPlanTeam", "https://www.aliyun.com/product/bailian"],
  ["GLM", "https://www.bigmodel.cn/"],
  ["GLMCodingPlan", "https://www.bigmodel.cn/"],
  ["Kimi", "https://www.moonshot.cn/"],
  ["KimiCode", "https://www.moonshot.cn/"],
  ["Minimax", "https://www.minimax.cn/"],
  ["MiniMaxMPlan", "https://www.minimax.cn/"],
  ["OpenAI", "https://openai.com/"],
  ["OpenRouter", "https://openrouter.ai/"],
  ["SiliconFlow", "https://www.siliconflow.cn/"],
  ["vLLM", "https://vllm.ai/"],
  ["LMDeploy", "https://lmdeploy.readthedocs.io/en/latest/"],
] as const;

function profileFor(name: string, language: string) {
  const provider = { name };
  const service = resolveSupplierService(provider);
  return getProviderProfileContent(service?.brandKey || getSupplierBrandKey(provider) || "", service?.serviceKey, language);
}

describe("provider profile content", () => {
  describe.each(["zh-CN", "en-US"])("%s", (language) => {
    it.each(leaves)("introduces %s and links its official website independently of the API address", (name, websiteUrl) => {
      const profile = profileFor(name, language);
      expect(profile).toBeDefined();
      expect(profile!.introduction.trim().length).toBeGreaterThan(40);
      expect(profile!.websiteUrl).toBe(websiteUrl);
      expect(profile!.highlights).toHaveLength(2);
      expect(profile!.highlights.every((highlight) => highlight.trim().length > 3)).toBe(true);
      expect(profile!.serviceSummary.trim().length).toBeGreaterThan(15);
      expect(profile!.serviceSummary).not.toBe(profile!.introduction);
      if (language === "zh-CN") {
        expect(profile!.introduction.length).toBeGreaterThanOrEqual(45);
        expect(profile!.introduction.length).toBeLessThanOrEqual(65);
        expect(profile!.serviceSummary.length).toBeGreaterThanOrEqual(18);
        expect(profile!.serviceSummary.length).toBeLessThanOrEqual(30);
      }
      const text = [profile!.introduction, profile!.serviceSummary, ...profile!.highlights].join(" ");
      expect(/[\u4e00-\u9fff]/.test(text)).toBe(language === "zh-CN");
    });

    it("distinguishes every product leaf rather than repeating its company description", () => {
      const introductions = leaves.map(([name]) => profileFor(name, language)?.introduction);
      expect(new Set(introductions).size).toBe(22);
      const summaries = leaves.map(([name]) => profileFor(name, language)?.serviceSummary);
      expect(new Set(summaries).size).toBe(22);
    });
  });

  it.each([
    ["DoubaoCodingPlan", "Doubao"],
    ["DoubaoAgentPlan", "Doubao"],
    ["SenseNovaTokenPlan", "SenseNova"],
    ["QwenCodingPlan", "Qwen"],
    ["QwenTokenPlan", "Qwen"],
    ["QwenTokenPlanTeam", "Qwen"],
    ["GLMCodingPlan", "GLM"],
    ["KimiCode", "Kimi"],
    ["MiniMaxMPlan", "Minimax"],
  ])("describes %s as a subscription product distinct from %s Standard API", (plan, standard) => {
    const product = profileFor(plan, "en-US")!;
    const platform = profileFor(standard, "en-US")!;
    expect(product.introduction).toMatch(/subscription/i);
    expect(product.introduction).not.toBe(platform.introduction);
    expect(product.highlights).not.toEqual(platform.highlights);
    expect(product.websiteUrl).toBe(platform.websiteUrl);
  });

  it.each(["vLLM", "LMDeploy"])("introduces %s as a deployment project rather than a hosted vendor", (name) => {
    const profile = profileFor(name, "en-US")!;
    expect(profile.introduction).toMatch(/open.source/i);
    expect(profile.serviceSummary).toMatch(/self.hosted/i);
  });

  it("distinguishes a model router from model developers", () => {
    const profile = profileFor("OpenRouter", "en-US")!;
    expect(profile.introduction).toMatch(/routing/i);
    expect(profile.highlights.join(" ")).toMatch(/providers/i);
  });

  it("keeps the differences that determine which subscription to choose", () => {
    expect(profileFor("DoubaoCodingPlan", "en-US")!.serviceSummary).toMatch(/coding tools/i);
    expect(profileFor("DoubaoAgentPlan", "en-US")!.serviceSummary).toMatch(/Harness/i);
    expect(profileFor("QwenTokenPlan", "en-US")!.serviceSummary).toMatch(/Credits/i);
    expect(profileFor("QwenTokenPlanTeam", "en-US")!.serviceSummary).toMatch(/seats/i);
    expect(profileFor("SenseNovaTokenPlan", "en-US")!.highlights.join(" ")).toMatch(/Anthropic/i);
    expect(profileFor("MiniMaxMPlan", "en-US")!.highlights.join(" ")).toMatch(/Dify/i);
  });

  it.each([
    ["Unknown", undefined], ["constructor", undefined], ["__proto__", undefined], ["toString", undefined],
    ["Qwen", "constructor"], ["Qwen", "__proto__"], ["Qwen", "future_plan"], ["Minimax", "coding"],
  ])("does not return inherited or unrelated content for %s / %s", (brandKey, serviceKey) => {
    expect(getProviderProfileContent(brandKey, serviceKey)).toBeUndefined();
  });

  it("supports Chinese locale variants and defaults other languages to English", () => {
    const chinese = getProviderProfileContent("Qwen", "token_team", "zh-CN");
    const english = getProviderProfileContent("Qwen", "token_team", "en-US");
    expect(chinese).not.toEqual(english);
    expect(getProviderProfileContent("Qwen", "token_team", "zh_TW")).toEqual(chinese);
    expect(getProviderProfileContent("Qwen", "token_team", "fr-FR")).toEqual(english);
    expect(getProviderProfileContent("Qwen", "token_team")).toEqual(english);
  });
});
