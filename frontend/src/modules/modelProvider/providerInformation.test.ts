import { describe, expect, it } from "vitest";
import { getProviderInformation } from "./providerInformation";
import { programmingScopeLink } from "./supplierNavigation";

const leaves = [
  ["Doubao", "cloud", "volcengine.com"],
  ["DoubaoCodingPlan", "plan", "volcengine.com"],
  ["DoubaoAgentPlan", "plan", "volcengine.com"],
  ["SenseNova", "cloud", "sensecore.cn"],
  ["SenseNovaTokenPlan", "plan", "sensenova.cn"],
  ["Qwen", "cloud", "aliyun.com"],
  ["QwenCodingPlan", "plan", "aliyun.com"],
  ["QwenTokenPlan", "plan", "aliyun.com"],
  ["QwenTokenPlanTeam", "plan", "aliyun.com"],
  ["GLM", "cloud", "bigmodel.cn"],
  ["GLMCodingPlan", "plan", "bigmodel.cn"],
  ["Kimi", "cloud", "kimi.com"],
  ["KimiCode", "plan", "kimi.com"],
  ["Minimax", "cloud", "minimax.io"],
  ["MiniMaxMPlan", "plan", "minimax.io"],
  ["Claude", "cloud", "claude.com"],
  ["DeepSeek", "cloud", "deepseek.com"],
  ["OpenAI", "cloud", "openai.com"],
  ["OpenRouter", "cloud", "openrouter.ai"],
  ["SiliconFlow", "cloud", "siliconflow.cn"],
  ["vLLM", "self-hosted", "vllm.ai"],
  ["LMDeploy", "self-hosted", "readthedocs.io"],
] as const;

const officialHosts = new Set([
  "docs.volcengine.com", "console.sensecore.cn", "platform.sensenova.cn", "www.sensenova.cn",
  "help.aliyun.com", "docs.bigmodel.cn", "platform.kimi.com", "www.kimi.com",
  "platform.minimax.io", "platform.minimax.cn", "platform.claude.com", "api-docs.deepseek.com",
  "developers.openai.com", "openrouter.ai", "docs.siliconflow.cn", "docs.vllm.ai",
  "lmdeploy.readthedocs.io",
  "ark.volcengine.com", "bailian.console.aliyun.com", "bigmodel.cn", "platform.openai.com",
  "cloud.siliconflow.cn", "platform.deepseek.com",
]);

describe("provider information", () => {
  describe.each(["zh-CN", "en-US"])("%s", (language) => {
    it.each(leaves)("provides an actionable guide for %s", (name, kind, officialDomain) => {
      const guide = getProviderInformation({ name }, language);
      expect(guide.kind).toBe(kind);
      for (const text of [guide.summary, guide.audience, guide.credential, guide.endpointNote]) {
        expect(text.trim().length).toBeGreaterThan(10);
      }
      expect(guide.steps).toHaveLength(3);
      expect(guide.steps.every((step) => step.title.trim() && step.body.trim())).toBe(true);
      expect(guide.faqs.length).toBeGreaterThanOrEqual(2);
      expect(guide.faqs.length).toBeLessThanOrEqual(3);
      expect(guide.faqs.every((faq) => faq.question.trim() && faq.answer.trim())).toBe(true);
      expect(guide.links.length).toBeGreaterThan(0);
      expect(guide.links.some(({ url }) => {
        const host = new URL(url).hostname;
        return host === officialDomain || host.endsWith(`.${officialDomain}`);
      })).toBe(true);
      for (const { label, url } of guide.links) {
        const source = new URL(url);
        expect(label.trim()).not.toBe("");
        expect(source.protocol).toBe("https:");
        expect(source.username).toBe("");
        expect(source.password).toBe("");
        expect(officialHosts.has(source.hostname) || (
          source.hostname === "github.com" && source.pathname.startsWith("/OpenSenseNova/")
        )).toBe(true);
      }
      expect(guide.verifiedAt).toBe("2026-10-23");
      if (kind === "plan") expect(guide.scope?.text.trim()).toBeTruthy();
      const scopeUrl = programmingScopeLink({ name });
      if (scopeUrl) expect(guide.links.map(({ url }) => url)).toContain(scopeUrl);
    });

    it("keeps every leaf's introduction specific to its service", () => {
      expect(new Set(leaves.map(([name]) => getProviderInformation({ name }, language).summary)).size).toBe(22);
    });
  });

  it("uses immutable service metadata after the display name and request adapter change", () => {
    const provider = Object.freeze({ name: "Renamed", brand_key: "Qwen", service_key: "token_team", protocol_name: "Claude" });
    expect(getProviderInformation(provider, "en-US")).toEqual(getProviderInformation({ name: "QwenTokenPlanTeam" }, "en-US"));
    expect(getProviderInformation({ name: "DoubaoCodingPlan", protocolName: "OpenAI" }, "zh-CN"))
      .toEqual(getProviderInformation({ name: "DoubaoCodingPlan", protocolName: "Claude" }, "zh-CN"));
    expect(getProviderInformation({ name: "Qwen", brandKey: "MiniMax", serviceKey: "m_plan" }))
      .toEqual(getProviderInformation({ name: "MiniMaxMPlan" }));
  });

  it.each(["isCustom", "is_custom"] as const)("keeps %s template collisions generic and free of inherited official links", (customFlag) => {
    for (const [name] of leaves) {
      const guide = getProviderInformation({ name, [customFlag]: true, brandKey: "Qwen", serviceKey: "coding", protocolName: "OpenAI" });
      expect(guide.kind).toBe("custom");
      expect(guide.links).toEqual([]);
      expect(guide.scope).toBeUndefined();
      expect(guide.verifiedAt).toBeUndefined();
    }
  });

  it.each([
    { name: "AIhubmix", protocolName: "OpenAI" },
    { name: "My DeepSeek", source: "DeepSeek" },
    { name: "", source: "Qwen", protocolName: "OpenAI" },
    { name: "Qwen", brandKey: "Qwen", serviceKey: "future_plan" },
    { name: "OpenAI", service_key: "future_plan" },
    { name: "OpenAI", brandKey: "Unknown", serviceKey: "standard" },
    { name: "Unknown", brandKey: "constructor" },
    { name: "Unknown", brand_key: "__proto__" },
    { name: "Unknown", brandKey: "toString" },
  ])("does not infer vendor documentation for an unknown identity: $name", (provider) => {
    const guide = getProviderInformation(provider);
    expect(guide.kind).toBe("custom");
    expect(guide.links).toEqual([]);
    expect(guide.steps).toHaveLength(3);
  });

  it("localizes Chinese variants and defaults other languages to English", () => {
    const provider = { name: "vLLM" };
    const english = getProviderInformation(provider, "en-US");
    const chinese = getProviderInformation(provider, "zh-CN");
    expect(chinese.summary).toMatch(/[\u4e00-\u9fff]/);
    expect(english.summary).not.toMatch(/[\u4e00-\u9fff]/);
    expect(chinese.summary).not.toBe(english.summary);
    expect(getProviderInformation(provider, "zh_TW")).toEqual(chinese);
    expect(getProviderInformation(provider)).toEqual(english);
    expect(getProviderInformation(provider, "fr-FR")).toEqual(english);
  });
});
