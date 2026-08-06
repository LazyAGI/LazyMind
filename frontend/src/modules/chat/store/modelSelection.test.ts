import { describe, expect, it } from "vitest";
import {
  parseModelSelectionFromModels,
  useModelSelectionStore,
} from "./modelSelection";

describe("useModelSelectionStore", () => {
  it("getModelSelection always returns value_engineering regardless of conversation", () => {
    const store = useModelSelectionStore.getState();
    expect(store.getModelSelection("conv-1")).toBe("value_engineering");
    expect(store.getModelSelection("")).toBe("value_engineering");
  });

  it("setModelSelection/resetForNewChat/clearModelSelection are safe no-ops", () => {
    const store = useModelSelectionStore.getState();
    expect(() => store.setModelSelection("conv-1", "value_engineering")).not.toThrow();
    expect(() => store.resetForNewChat()).not.toThrow();
    expect(() => store.clearModelSelection("conv-1")).not.toThrow();
  });
});

describe("parseModelSelectionFromModels", () => {
  it("always returns value_engineering regardless of input", () => {
    expect(parseModelSelectionFromModels()).toBe("value_engineering");
    expect(parseModelSelectionFromModels(["deepseek"])).toBe("value_engineering");
    expect(parseModelSelectionFromModels([])).toBe("value_engineering");
  });
});
