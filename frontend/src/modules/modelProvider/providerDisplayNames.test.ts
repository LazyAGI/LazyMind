import { describe, expect, it } from "vitest";
import {
  getProviderBrandDisplayName,
  getProviderBrandSearchText,
  getProviderDisplayName,
  getProviderServiceDisplayName,
  getProviderSearchText,
} from "./providerDisplayNames";

describe("supplier display names", () => {
  it.each([
    ["DeepSeek", "深度求索", "DeepSeek"],
    ["Doubao", "火山引擎 · 标准API", "Volcengine · Standard API"],
    ["GLM", "智谱 · 标准API", "GLM · Standard API"],
    ["Kimi", "月之暗面 · Kimi开放平台", "Kimi · Kimi Open Platform"],
    ["Qwen", "阿里云百炼 · 标准API", "Alibaba Cloud Model Studio · Standard API"],
    ["SenseNova", "商汤日日新 · 经典API", "SenseNova · Classic API"],
    ["SiliconFlow", "硅基流动", "SiliconFlow"],
    ["Claude", "Claude", "Claude"],
    ["OpenAI", "OpenAI", "OpenAI"],
    ["OpenRouter", "OpenRouter", "OpenRouter"],
    ["Minimax", "MiniMax · 标准API", "MiniMax · Standard API"],
    ["VLLM", "vLLM", "vLLM"],
    ["lmdeploy", "LMDeploy", "LMDeploy"],
  ])("localizes the built-in %s supplier without changing its identity", (name, chinese, english) => {
    const supplier = { name, id: `stored-${name}`, protocolName: name };
    expect(getProviderDisplayName(supplier, "zh-CN")).toBe(chinese);
    expect(getProviderDisplayName(supplier, "en-US")).toBe(english);
    expect(supplier.name).toBe(name);
    expect(supplier.id).toBe(`stored-${name}`);
  });

  it.each(["zh", "zh-CN", "zh_CN", "ZH-cn"])(
    "uses the Chinese brand for locale %s",
    (language) => {
      expect(getProviderDisplayName({ name: "DeepSeek" }, language)).toBe("深度求索");
    },
  );

  it("keeps custom and migrated supplier names exactly as saved", () => {
    for (const name of ["DeepSeek", "OpenAI", "qwen 自用", "  我的模型平台  "]) {
      expect(getProviderDisplayName({ name, isCustom: true, protocolName: "OpenAI" }, "zh-CN")).toBe(name);
      expect(getProviderDisplayName({ name, is_custom: true, protocol_name: "OpenAI" }, "en-US")).toBe(name);
    }
  });

  it("keeps an unfamiliar supplier name even when it uses a known protocol", () => {
    expect(getProviderDisplayName({ name: "AIhubmix", protocolName: "OpenAI" }, "zh-CN")).toBe("AIhubmix");
    expect(getProviderDisplayName({ name: "DeepSeek 代理", source: "DeepSeek" }, "zh-CN")).toBe("DeepSeek 代理");
    expect(getProviderDisplayName({ name: "my-openrouter-proxy" }, "zh-CN")).toBe("my-openrouter-proxy");
  });

  it("prefers the supplier's catalog name over its selected request protocol", () => {
    expect(getProviderDisplayName({ name: "DeepSeek", protocolName: "Claude", source: "OpenAI" }, "zh-CN")).toBe("深度求索");
  });

  it("recognizes exact catalog aliases and retains the brand-only fallback for an empty name", () => {
    expect(getProviderDisplayName({ name: "  deepseek  " }, "zh-CN")).toBe("深度求索");
    expect(getProviderDisplayName({ name: "Moonshot" }, "zh-CN")).toBe("月之暗面");
    expect(getProviderDisplayName({ name: "", protocol_name: "SiliconFlow" }, "zh-CN")).toBe("硅基流动");
    expect(getProviderDisplayName({ name: "", source: "Qwen" }, "en-US")).toBe("Alibaba Cloud Model Studio");
    expect(getProviderDisplayName({ name: "", protocolName: "OpenAI", isCustom: true }, "zh-CN")).toBe("");
  });

  it("lets searches find built-in suppliers using Chinese, English or legacy names in either locale", () => {
    for (const language of ["zh-CN", "en-US"]) {
      const text = getProviderSearchText({ name: "Kimi", protocolName: "OpenAI" }, language);
      expect(text).toContain("kimi");
      expect(text).toContain("月之暗面");
      expect(text).toContain("moonshot");
      expect(text).not.toContain("openai");
    }
  });

  it("does not make custom suppliers match the brand of their compatibility protocol", () => {
    const text = getProviderSearchText({ name: "自建代理", isCustom: true, protocolName: "DeepSeek" }, "zh-CN");
    expect(text).toBe("自建代理");
    expect(text).not.toContain("deepseek");
    expect(text).not.toContain("深度求索");
  });

  it("renames the supplier to Volcengine while retaining old Doubao search aliases", () => {
    for (const language of ["zh-CN", "en-US"]) {
      const text = getProviderSearchText({ name: "Doubao" }, language);
      for (const alias of ["doubao", "豆包", "volcengine", "火山方舟", "火山引擎"]) expect(text).toContain(alias);
    }
    expect(getProviderDisplayName({ name: "Doubao", isCustom: true }, "zh-CN")).toBe("Doubao");
  });

  it.each([
    ["DoubaoCodingPlan", "火山引擎 · Coding Plan"],
    ["DoubaoAgentPlan", "火山引擎 · Agent Plan"],
    ["SenseNovaTokenPlan", "商汤日日新 · Token Plan"],
    ["QwenCodingPlan", "阿里云百炼 · Coding Plan"],
    ["QwenTokenPlan", "阿里云百炼 · Token Plan"],
    ["QwenTokenPlanTeam", "阿里云百炼 · Token Plan团队版"],
    ["GLMCodingPlan", "智谱 · Coding Plan"],
    ["KimiCode", "月之暗面 · Kimi Code"],
    ["MiniMaxMPlan", "MiniMax · M Plan"],
  ])("resolves legacy builtin service %s by its exact stored name", (name, label) => {
    expect(getProviderDisplayName({ name, protocolName: "OpenAI" }, "zh-CN")).toBe(label);
  });

  it("prefers explicit service metadata to a conflicting stored name and protocol", () => {
    const supplier = { name: "Doubao", brandKey: "Qwen", serviceKey: "token_team", protocolName: "Claude" };
    expect(getProviderBrandDisplayName(supplier, "zh-CN")).toBe("阿里云百炼");
    expect(getProviderServiceDisplayName(supplier, "zh-CN")).toBe("Token Plan团队版");
    expect(getProviderDisplayName(supplier, "en-US")).toBe("Alibaba Cloud Model Studio · Token Plan Team");
  });

  it("keeps custom identities independent even when explicit metadata and templates name builtin brands", () => {
    for (const name of ["AIhubmix", "packyapi", "qwen自用", "QwenTokenPlan"]) {
      const supplier = { name, isCustom: true, brandKey: "Doubao", serviceKey: "coding", protocolName: "Doubao" };
      expect(getProviderDisplayName(supplier, "zh-CN")).toBe(name);
      expect(getProviderBrandDisplayName(supplier, "zh-CN")).toBe(name);
      expect(getProviderServiceDisplayName(supplier, "zh-CN")).toBe("");
      expect(getProviderSearchText(supplier, "zh-CN")).toBe(name.toLowerCase());
    }
  });

  it("searches both brand languages, previous aliases and only this service's names", () => {
    const supplier = { name: "QwenTokenPlanTeam", brand_key: "Qwen", service_key: "token_team" };
    const text = getProviderSearchText(supplier, "en-US");
    for (const alias of ["阿里云百炼", "通义千问", "qwen", "alibaba cloud model studio", "token plan团队版", "token plan team"]) expect(text).toContain(alias);
    expect(text).not.toContain("coding");
    expect(text).not.toContain("openai");
  });

  it("separates brand search aliases from individual service search words", () => {
    const supplier = { name: "DoubaoCodingPlan", brandKey: "Doubao", serviceKey: "coding" };
    const text = getProviderBrandSearchText(supplier, "zh-CN");
    for (const alias of ["火山引擎", "doubao", "volcengine", "火山方舟"]) expect(text).toContain(alias);
    expect(text).not.toContain("coding");
    expect(text).not.toContain("plan");
    expect(getProviderSearchText(supplier, "zh-CN")).toContain("coding plan");
  });
});
