import { describe, expect, it, vi } from "vitest";
import { getProviderInformation } from "./providerInformation";
import { programmingScopeLink } from "./supplierNavigation";
import * as supplierNavigation from "./supplierNavigation";

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

const resourceKinds = ["docs", "credentials", "models", "pricing", "scope", "product", "deployment", "faq"];

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
      expect(guide.faqs.length).toBeGreaterThanOrEqual(1);
      expect(guide.faqs.length).toBeLessThanOrEqual(2);
      expect(guide.faqs.every((faq) => faq.question.trim() && faq.answer.trim())).toBe(true);
      expect(guide.links.length).toBeGreaterThan(0);
      expect(guide.links.some(({ url }) => {
        const host = new URL(url).hostname;
        return host === officialDomain || host.endsWith(`.${officialDomain}`);
      })).toBe(true);
      for (const { label, url, kind: resourceKind } of guide.links) {
        const source = new URL(url);
        expect(label.trim()).not.toBe("");
        expect(resourceKinds).toContain(resourceKind);
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

    it.each([
      ["SenseNova", "https://console.sensecore.cn/micro/help/docs/model-as-a-service/nova/overview/Authorization", "credentials"],
      ["SenseNova", "https://console.sensecore.cn/micro/help/docs/model-as-a-service/nova/pricing/", "pricing"],
      ["QwenCodingPlan", "https://bailian.console.aliyun.com/cn-beijing/subscription/coding-plan", "credentials"],
      ["QwenTokenPlan", "https://bailian.console.aliyun.com/cn-beijing/subscription/overview", "credentials"],
      ["QwenTokenPlanTeam", "https://bailian.console.aliyun.com/cn-beijing/subscription/uac-admin/organization/members/list", "credentials"],
      ["GLM", "https://docs.bigmodel.cn/cn/guide/start/pricing", "pricing"],
      ["GLMCodingPlan", "https://bigmodel.cn/apikey/coding-plan-per", "credentials"],
      ["GLMCodingPlan", "https://bigmodel.cn/apikey/coding-plan-team", "credentials"],
      ["DeepSeek", "https://api-docs.deepseek.com/quick_start/pricing/", "pricing"],
      ["DeepSeek", "https://platform.deepseek.com/api_keys", "credentials"],
      ["OpenAI", "https://developers.openai.com/api/docs/pricing", "pricing"],
      ["OpenAI", "https://platform.openai.com/api-keys", "credentials"],
      ["MiniMaxMPlan", "https://platform.minimax.cn/console/plan", "credentials"],
    ])("keeps the purpose of %s resource %s", (name, url, kind) => {
      expect(getProviderInformation({ name }, language).links.find((link) => link.url === url)?.kind).toBe(kind);
    });

    it("keeps model catalogs and product, scope, FAQ, and deployment resources distinct", () => {
      const resource = (name: string, url: string) => getProviderInformation({ name }, language).links.find((link) => link.url === url);
      expect(resource("Qwen", "https://help.aliyun.com/zh/model-studio/models")?.kind).toBe("models");
      expect(resource("OpenRouter", "https://openrouter.ai/models")?.kind).toBe("models");
      expect(resource("SenseNovaTokenPlan", "https://www.sensenova.cn/token-plan")?.kind).toBe("product");
      expect(resource("QwenCodingPlan", "https://help.aliyun.com/zh/model-studio/more-tools")?.kind).toBe("scope");
      expect(resource("MiniMaxMPlan", "https://platform.minimax.io/docs/m-plan/faq")?.kind).toBe("faq");
      expect(resource("vLLM", "https://docs.vllm.ai/en/stable/serving/online_serving/openai_compatible_server/")?.kind).toBe("deployment");
      expect(resource("LMDeploy", "https://lmdeploy.readthedocs.io/en/latest/llm/api_server.html")?.kind).toBe("deployment");
    });
  });

  it("appends a scope resource with its complete label when the scope URL is not already listed", () => {
    const scopeUrl = "https://help.aliyun.com/zh/model-studio/more-tools";
    const scopeLink = vi.spyOn(supplierNavigation, "programmingScopeLink").mockReturnValue(scopeUrl);
    try {
      for (const [language, label] of [["zh-CN", "使用范围与工具接入"], ["en-US", "Usage scope and tool integration"]]) {
        const guide = getProviderInformation({ name: "OpenAI" }, language);
        expect(guide.links[guide.links.length - 1]).toEqual({ label, url: scopeUrl, kind: "scope" });
        expect(guide.links.filter(({ url }) => url === scopeUrl)).toHaveLength(1);
      }
      expect(getProviderInformation({ name: "OpenAI", isCustom: true }).links).toEqual([]);
      expect(getProviderInformation({ name: "Unknown" }).links).toEqual([]);
    } finally {
      scopeLink.mockRestore();
    }
  });

  it("preserves complete localized labels and document order when several resources have the same kind", () => {
    const urls = [
      "https://console.sensecore.cn/micro/help/docs/model-as-a-service/nova/overview/compatible-mode/",
      "https://console.sensecore.cn/micro/help/en/docs/model-as-a-service/nova/",
    ];
    expect(getProviderInformation({ name: "SenseNova" }, "zh-CN").links.slice(0, 2)).toEqual([
      { label: "经典 API 兼容模式", url: urls[0], kind: "docs" },
      { label: "大装置日日新服务入门", url: urls[1], kind: "docs" },
    ]);
    expect(getProviderInformation({ name: "SenseNova" }, "en-US").links.slice(0, 2)).toEqual([
      { label: "Classic API compatibility guide", url: urls[0], kind: "docs" },
      { label: "SenseCore SenseNova service setup", url: urls[1], kind: "docs" },
    ]);
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

  it("provides a real supplier profile with a website independent of its saved API address", () => {
    const supplier = { name: "OpenAI", baseUrl: "https://private-gateway.example.test/v1/" };
    const guide = getProviderInformation(supplier, "zh-CN");
    expect(guide.profile?.websiteUrl).toBe("https://openai.com/");
    expect(guide.profile?.introduction).toMatch(/OpenAI/);
    expect(guide.profile?.highlights).toHaveLength(2);
    expect(guide.profile?.introduction).not.toBe(guide.summary);
    expect(JSON.stringify(guide.profile)).not.toContain("private-gateway");
  });

  it("keeps supplier profiles isolated from custom names and unknown service identities", () => {
    for (const [name] of leaves) {
      expect(getProviderInformation({ name, isCustom: true }).profile).toBeUndefined();
    }
    expect(getProviderInformation({ name: "Qwen", brandKey: "Qwen", serviceKey: "future_plan" }).profile).toBeUndefined();
    expect(getProviderInformation({ name: "Private provider", protocolName: "OpenAI" }).profile).toBeUndefined();
  });
});
