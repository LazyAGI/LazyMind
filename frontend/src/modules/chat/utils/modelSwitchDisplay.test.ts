import { describe, expect, it } from "vitest";
import { chatProviderDisplayName } from "./providerDisplay";
import { formatModelSwitchLabel } from "./modelSwitchDisplay";

describe("supplier display identity", () => {
  it.each([
    ["DoubaoCodingPlan", "Volcengine · Coding Plan"],
    ["DoubaoAgentPlan", "Volcengine · Agent Plan"],
    ["QwenCodingPlan", "Alibaba Cloud Model Studio · Coding Plan"],
    ["KimiCode", "Kimi · Kimi Code"],
  ])("uses %s instead of its protocol", (name, expected) => {
    const identity = { candidate_id: "own:m", source: "own" as const, model_id: "m", provider_group_id: "g", provider_name: "OpenAI", provider_display_name: name, model_name: "model" };
    expect(formatModelSwitchLabel(identity, 131072, "unknown")).toBe(`${expected} · model (128K)`);
  });
  it("preserves custom names even when they match built-in suppliers", () => {
    const identity = { candidate_id: "own:m", source: "own" as const, model_id: "m", provider_group_id: "g", provider_name: "OpenAI", provider_display_name: "Doubao", provider_is_custom: true, model_name: "model" };
    expect(formatModelSwitchLabel(identity, null, "unknown")).toBe("Doubao · model (unknown)");
  });
  it("retains legacy labels without display metadata", () => {
    expect(formatModelSwitchLabel({ candidate_id: "own:m", source: "own", model_id: "m", provider_group_id: "g", provider_name: "OpenAI" }, null, "unknown")).toBe("OpenAI · m (unknown)");
  });
});


it.each([
  ["Doubao", "火山引擎 · 标准API"], ["DoubaoAgentPlan", "火山引擎 · Agent Plan"],
  ["DoubaoCodingPlan", "火山引擎 · Coding Plan"], ["QwenTokenPlan", "阿里云百炼 · Token Plan"],
  ["QwenTokenPlanTeam", "阿里云百炼 · Token Plan团队版"], ["GLMCodingPlan", "智谱 · Coding Plan"],
  ["SenseNovaTokenPlan", "商汤日日新 · Token Plan"], ["MiniMaxMPlan", "MiniMax · M Plan"],
  ["KimiCode", "月之暗面 · Kimi Code"], ["DeepSeek", "深度求索"], ["SiliconFlow", "硅基流动"],
  ["OpenAI", "OpenAI"], ["Claude", "Claude"], ["OpenRouter", "OpenRouter"], ["vLLM", "vLLM"], ["LMDeploy", "LMDeploy"],
])("localizes supplier %s", (name, expected) => {
  expect(chatProviderDisplayName({ provider_name: "OpenAI", provider_display_name: name }, "zh-CN")).toBe(expected);
});
