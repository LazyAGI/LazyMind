import { describe, expect, it } from "vitest";
import { editableModelSettings } from "./modelSettings";

describe("editableModelSettings", () => {
  it("round-trips untouched legacy support declarations", () => {
    expect(editableModelSettings("LLM_CHAT", {
      feature_support: { vision: false },
    })).toEqual({ feature_support: { vision: false } });
  });

  it("removes contradictory support only for an explicitly enabled feature", () => {
    expect(editableModelSettings("LLM_CHAT", {
      feature_support: { vision: false },
      feature_enabled: { vision: true },
    }, {}, false, new Set(["vision"]))).toEqual({
      feature_enabled: { vision: true },
    });
  });

  it("preserves unrelated support and enabled flags", () => {
    expect(editableModelSettings("LLM_CHAT", {
      feature_support: { vision: true, reasoning: false },
      feature_enabled: { tool_calling: false },
    }, {}, false, new Set(["tool_calling"]))).toEqual({
      feature_support: { vision: true, reasoning: false },
      feature_enabled: { tool_calling: false },
    });
  });
});
