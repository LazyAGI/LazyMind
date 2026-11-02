import { describe, expect, it } from "vitest";
import { getSupplierBrandKey, getSupplierServiceLabel, resolveSupplierService } from "./supplierServices";

describe("supplier service identity", () => {
  it.each([
    ["Doubao", "Doubao", "standard", 0],
    ["DoubaoCodingPlan", "Doubao", "coding", 1],
    ["DoubaoAgentPlan", "Doubao", "agent", 2],
    ["SenseNova", "SenseNova", "standard", 0],
    ["SenseNovaTokenPlan", "SenseNova", "token", 1],
    ["Qwen", "Qwen", "standard", 0],
    ["QwenCodingPlan", "Qwen", "coding", 1],
    ["QwenTokenPlan", "Qwen", "token", 2],
    ["QwenTokenPlanTeam", "Qwen", "token_team", 3],
    ["GLM", "GLM", "standard", 0],
    ["GLMCodingPlan", "GLM", "coding", 1],
    ["Kimi", "Kimi", "standard", 0],
    ["KimiCode", "Kimi", "code", 1],
    ["Minimax", "Minimax", "standard", 0],
    ["MiniMaxMPlan", "Minimax", "m_plan", 1],
  ])("maps exact builtin name %s to a stable service identity", (name, brandKey, serviceKey, order) => {
    const supplier = { name: String(name), protocolName: "Claude" };
    expect(resolveSupplierService(supplier)).toEqual({ brandKey, serviceKey, order });
    expect(getSupplierBrandKey(supplier)).toBe(brandKey);
  });

  it.each(["isCustom", "is_custom"] as const)("never assigns a custom %s identity to its builtin template", (flag) => {
    for (const name of ["AIhubmix", "packyapi", "qwen自用", "DoubaoCodingPlan"]) {
      const supplier = { name, [flag]: true, brand_key: "Doubao", service_key: "coding", protocolName: "Doubao" };
      expect(getSupplierBrandKey(supplier)).toBeUndefined();
      expect(resolveSupplierService(supplier)).toBeUndefined();
      expect(getSupplierServiceLabel(supplier, "zh-CN")).toBe("");
    }
  });

  it("prefers explicit camel or snake metadata and accepts MiniMax's established casing", () => {
    expect(resolveSupplierService({ name: "Qwen", brandKey: "MiniMax", serviceKey: "m_plan" }))
      .toEqual({ brandKey: "Minimax", serviceKey: "m_plan", order: 1 });
    expect(resolveSupplierService({ name: "unrelated", brand_key: "Qwen", service_key: "token_team" }))
      .toEqual({ brandKey: "Qwen", serviceKey: "token_team", order: 3 });
  });

  it("does not infer a service from compatibility adapters or a partial brand-like name", () => {
    for (const name of ["AIhubmix", "My Doubao", "QwenCodingPlan proxy"]) {
      expect(resolveSupplierService({ name, protocolName: "Doubao" })).toBeUndefined();
      expect(getSupplierBrandKey({ name, source: "Doubao" })).toBeUndefined();
    }
    expect(getSupplierBrandKey({ name: "", source: "Qwen" })).toBe("Qwen");
    expect(resolveSupplierService({ name: "", source: "Qwen" })).toBeUndefined();
  });

  it("leaves unknown explicit services unresolved instead of falling back to the standard leaf", () => {
    expect(resolveSupplierService({ name: "Qwen", brandKey: "Qwen", serviceKey: "future_product" })).toBeUndefined();
    expect(resolveSupplierService({ name: "Qwen", brandKey: "OtherBrand", serviceKey: "standard" })).toBeUndefined();
  });

  it("localizes service labels without changing technical identity", () => {
    const supplier = { name: "QwenTokenPlanTeam", brandKey: "Qwen", serviceKey: "token_team" };
    expect(getSupplierServiceLabel(supplier, "zh-CN")).toBe("Token Plan团队版");
    expect(getSupplierServiceLabel(supplier, "en-US")).toBe("Token Plan Team");
    expect(supplier).toEqual({ name: "QwenTokenPlanTeam", brandKey: "Qwen", serviceKey: "token_team" });
  });
});
