import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { load } from "js-yaml";
import { describe, expect, it } from "vitest";
import { getProviderProtocols, providerProtocolDeclarations } from "./providerProtocols";

const expected = {
  Doubao: ["openai-chat", "openai-responses", "anthropic"],
  DoubaoCodingPlan: ["openai-chat", "anthropic"], DoubaoAgentPlan: ["openai-chat", "anthropic"],
  SenseNova: ["openai-chat", "native"], SenseNovaTokenPlan: ["openai-chat", "openai-responses", "anthropic"],
  Qwen: ["openai-chat", "anthropic", "native"], QwenCodingPlan: ["openai-chat", "anthropic"],
  QwenTokenPlan: ["openai-chat", "anthropic"], QwenTokenPlanTeam: ["openai-chat", "anthropic"],
  GLM: ["openai-chat", "native"], GLMCodingPlan: ["openai-chat", "openai-responses", "anthropic"],
  Kimi: ["openai-chat", "openai-responses", "anthropic"], KimiCode: ["openai-chat", "openai-responses", "anthropic"],
  Minimax: ["openai-chat", "openai-responses", "anthropic", "native"], MiniMaxMPlan: ["openai-chat", "anthropic"],
  Claude: ["anthropic", "openai-chat"], DeepSeek: ["openai-chat", "openai-responses", "anthropic"],
  OpenAI: ["openai-chat", "openai-responses"], OpenRouter: ["openai-chat", "openai-responses", "anthropic"],
  SiliconFlow: ["openai-chat", "anthropic"], vLLM: ["openai-chat", "openai-responses", "anthropic", "native"],
  LMDeploy: ["openai-chat", "anthropic"],
};

describe("documented provider protocols", () => {
  it("explicitly covers every model catalog service and all 13 brands", () => {
    const catalog = load(readFileSync(resolve(process.cwd(), "../backend/core/config/model_catalog.yaml"), "utf8")) as {
      model_providers: { suppliers: { name: string }[] };
    };
    expect(Object.keys(expected).sort()).toEqual(catalog.model_providers.suppliers.map(({ name }) => name).sort());
    expect(Object.keys(providerProtocolDeclarations)).toHaveLength(22);
    expect(new Set(Object.keys(providerProtocolDeclarations).map((key) => key.split(":")[0])).size).toBe(13);
  });

  it.each(Object.entries(expected))("declares %s independently of the saved runtime adapter", (name, protocols) => {
    for (const protocolName of [undefined, "OpenAI", "Claude", "Gemini", "NotImplemented"]) {
      const result = getProviderProtocols({ name, protocolName, source: "unknown" });
      expect(result.status).toBe("documented");
      expect(result.protocols.map(({ id }) => id)).toEqual(protocols);
      expect(result.verifiedAt).toBe("2026-10-26");
      expect(result.sources.length).toBeGreaterThan(0);
      expect(result.sources.every(({ label, url }) => label && new URL(url).protocol === "https:")).toBe(true);
    }
  });

  it("uses immutable service identity after rename", () => {
    expect(getProviderProtocols({ name: "My service", brand_key: "Qwen", service_key: "token_team", protocol_name: "Gemini" }))
      .toEqual(getProviderProtocols({ name: "QwenTokenPlanTeam" }));
    expect(getProviderProtocols({ name: "OpenAI", brandKey: "Minimax", serviceKey: "m_plan" }))
      .toEqual(getProviderProtocols({ name: "MiniMaxMPlan" }));
  });

  it.each(["isCustom", "is_custom"] as const)("isolates all custom catalog-name collisions through %s", (flag) => {
    for (const name of Object.keys(expected)) {
      const result = getProviderProtocols({ name, [flag]: true, brandKey: "Qwen", serviceKey: "coding" });
      expect(result.status).toBe("unknown");
      expect(result.protocols).toEqual([]);
      expect(result.sources).toEqual([]);
      expect(result.verifiedAt).toBeUndefined();
    }
  });

  it.each([
    { name: "Private Gateway", protocolName: "OpenAI" }, { name: "", source: "Qwen" },
    { name: "Qwen", serviceKey: "future" }, { name: "OpenAI", service_key: "future" },
    { name: "OpenAI", brandKey: "Unknown" }, { name: "Unknown", brandKey: "__proto__" },
    { name: "Unknown", brandKey: "constructor" },
  ])("does not guess protocols for $name", (identity) => {
    expect(getProviderProtocols(identity)).toMatchObject({ status: "unknown", protocols: [], sources: [] });
  });

  it("keeps protocol metadata disconnected from runtime capability and endpoint selection", () => {
    const source = readFileSync(resolve(process.cwd(), "src/modules/modelProvider/providerProtocols.ts"), "utf8");
    expect(source).not.toMatch(/from ["'][^"']*(?:capabilit|requestConfig|runtime|modelTypes)/i);
    expect(getProviderProtocols({ name: "SenseNovaTokenPlan" }).protocols.map(({ id }) => id)).toContain("anthropic");
    expect(getProviderProtocols({ name: "QwenCodingPlan" }).protocols.map(({ id }) => id)).not.toContain("openai-responses");
    expect(Object.keys(expected).some((name) => getProviderProtocols({ name }).protocols.some(({ id }) => id === "gemini"))).toBe(false);
  });

  it("localizes labels and notes without losing protocol IDs", () => {
    const english = getProviderProtocols({ name: "Qwen" });
    const chinese = getProviderProtocols({ name: "Qwen" }, "zh_TW");
    expect(chinese.note).toMatch(/[\u4e00-\u9fff]/);
    expect(chinese.protocols.map(({ id }) => id)).toEqual(english.protocols.map(({ id }) => id));
    expect(chinese.protocols.at(-1)?.label).toContain("DashScope");
  });
});
