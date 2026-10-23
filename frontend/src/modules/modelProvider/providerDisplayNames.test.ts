import { describe, expect, it } from "vitest";
import {
  getProviderDisplayName,
  getProviderSearchText,
} from "./providerDisplayNames";

describe("supplier display names", () => {
  it.each([
    ["DeepSeek", "深度求索", "DeepSeek"],
    ["Doubao", "火山引擎", "Volcengine"],
    ["GLM", "智谱", "GLM"],
    ["Kimi", "月之暗面", "Kimi"],
    ["Qwen", "通义千问", "Qwen"],
    ["SenseNova", "商汤日日新", "SenseNova"],
    ["SiliconFlow", "硅基流动", "SiliconFlow"],
    ["Claude", "Claude", "Claude"],
    ["OpenAI", "OpenAI", "OpenAI"],
    ["OpenRouter", "OpenRouter", "OpenRouter"],
    ["Minimax", "MiniMax", "MiniMax"],
    ["VLLM", "vLLM", "vLLM"],
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

  it("recognizes exact catalog aliases and uses a technical identity only when no name exists", () => {
    expect(getProviderDisplayName({ name: "  deepseek  " }, "zh-CN")).toBe("深度求索");
    expect(getProviderDisplayName({ name: "Moonshot" }, "zh-CN")).toBe("月之暗面");
    expect(getProviderDisplayName({ name: "", protocol_name: "SiliconFlow" }, "zh-CN")).toBe("硅基流动");
    expect(getProviderDisplayName({ name: "", source: "Qwen" }, "en-US")).toBe("Qwen");
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
});
