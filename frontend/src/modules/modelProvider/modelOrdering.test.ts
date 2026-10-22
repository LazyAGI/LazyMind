import { describe, expect, it } from "vitest";
import { sortCustomModelsFirst } from "./modelOrdering";

describe("custom model ordering", () => {
  it("pins user models while preserving the relative order in each section", () => {
    const models = [
      { id: "catalog-a", builtIn: true },
      { id: "manual-a", builtIn: false },
      { id: "catalog-b", builtIn: true },
      { id: "manual-b", builtIn: false },
    ];
    expect(sortCustomModelsFirst(models).map((model) => model.id)).toEqual([
      "manual-a", "manual-b", "catalog-a", "catalog-b",
    ]);
    expect(models[0].id).toBe("catalog-a");
  });
});
