import { describe, expect, it } from "vitest";
import { orderSuppliers, reorderSuppliers } from "./supplierOrdering";

const suppliers = [
  { id: "openai", name: "OpenAI" },
  { id: "volcengine", name: "Doubao" },
  { id: "custom-z", name: "Z proxy", isCustom: true },
  { id: "sense", name: "SenseNova" },
  { id: "claude", name: "Claude" },
  { id: "custom-a", name: "A proxy", isCustom: true },
  { id: "deepseek", name: "DeepSeek" },
];
const ids = (items: typeof suppliers) => items.map((item) => item.id);

describe("supplier sidebar order", () => {
  it("uses SenseNova first only for the initial order, then alphabetical brands", () => {
    expect(ids(orderSuppliers(suppliers))).toEqual([
      "sense", "custom-a", "claude", "deepseek", "openai", "volcengine", "custom-z",
    ]);
    expect(suppliers[0].id).toBe("openai");
  });

  it("honors saved order across built-in and custom suppliers", () => {
    expect(ids(orderSuppliers(suppliers, ["openai", "sense", "custom-z", "deepseek", "custom-a", "claude"]))).toEqual([
      "openai", "sense", "custom-z", "deepseek", "custom-a", "claude", "volcengine",
    ]);
    const custom = { id: "custom-sense", name: "SenseNova", isCustom: true };
    expect(orderSuppliers([suppliers[0], suppliers[3], custom], [custom.id])[0]).toBe(custom);
  });

  it("reorders visible suppliers while preserving filtered-out positions", () => {
    const ordered = orderSuppliers(suppliers);
    const moved = reorderSuppliers(ordered, ["claude", "openai"], "openai", "claude");
    expect(ids(moved)).toEqual([
      "sense", "custom-a", "openai", "deepseek", "claude", "volcengine", "custom-z",
    ]);
    expect(ids(reorderSuppliers(ordered, ids(ordered), "openai", "custom-a"))[1]).toBe("openai");
    expect(ids(reorderSuppliers(ordered, ids(ordered), "sense", "claude"))[2]).toBe("sense");
    expect(reorderSuppliers(ordered, ["claude"], "openai", "claude")).toBe(ordered);
  });

  it("projects legacy leaf ranks onto a whole brand using its earliest saved member", () => {
    const blocks = [
      { id: "brand:Doubao", name: "Doubao", orderAliases: ["ark-standard", "ark-coding"] },
      { id: "openai", name: "OpenAI", orderAliases: ["openai"] },
      { id: "deepseek", name: "DeepSeek", orderAliases: ["deepseek"] },
    ];
    expect(orderSuppliers(blocks, ["openai", "ark-coding", "deepseek", "ark-standard"]).map((item) => item.id))
      .toEqual(["openai", "brand:Doubao", "deepseek"]);
  });
});
