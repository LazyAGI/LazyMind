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
  it("keeps custom suppliers first, SenseNova next, then English supplier brands alphabetically", () => {
    expect(ids(orderSuppliers(suppliers))).toEqual([
      "custom-a", "custom-z", "sense", "claude", "deepseek", "openai", "volcengine",
    ]);
    expect(suppliers[0].id).toBe("openai");
  });

  it("applies saved order within its section without moving the pinned supplier or custom suppliers", () => {
    expect(ids(orderSuppliers(suppliers, ["openai", "sense", "custom-z", "deepseek", "custom-a", "claude"]))).toEqual([
      "custom-z", "custom-a", "sense", "openai", "deepseek", "claude", "volcengine",
    ]);
    const custom = { id: "custom-sense", name: "SenseNova", isCustom: true };
    expect(orderSuppliers([suppliers[0], suppliers[3], custom])[0]).toBe(custom);
  });

  it("reorders visible suppliers while preserving hidden positions and pinned sections", () => {
    const ordered = orderSuppliers(suppliers);
    const moved = reorderSuppliers(ordered, ["claude", "openai"], "openai", "claude");
    expect(ids(moved)).toEqual([
      "custom-a", "custom-z", "sense", "openai", "deepseek", "claude", "volcengine",
    ]);
    expect(reorderSuppliers(ordered, ids(ordered), "openai", "custom-a")).toBe(ordered);
    expect(reorderSuppliers(ordered, ids(ordered), "sense", "claude")).toBe(ordered);
    expect(reorderSuppliers(ordered, ["claude"], "openai", "claude")).toBe(ordered);
  });
});
