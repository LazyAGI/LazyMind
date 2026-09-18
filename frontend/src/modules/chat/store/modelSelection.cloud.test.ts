import { describe, expect, it } from "vitest";

import { toChatModelSelectionRequest } from "./modelSelection";

describe("Cloud chat model selection serialization", () => {
  it("preserves the source discriminator for a fixed Cloud model", () => {
    expect(toChatModelSelectionRequest({
      mode: "fixed",
      source: "cloud",
      model_id: "lazymind-text-default",
      version: 0,
    } as never)).toEqual({
      mode: "fixed",
      source: "cloud",
      model_id: "lazymind-text-default",
    });
  });

  it("keeps Auto source-free so the server owns its safe candidate set", () => {
    expect(toChatModelSelectionRequest({
      mode: "auto",
      source: "cloud",
      version: 0,
    } as never)).toEqual({ mode: "auto" });
  });
});
