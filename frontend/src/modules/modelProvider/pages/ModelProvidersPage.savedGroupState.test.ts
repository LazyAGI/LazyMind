import { describe, expect, it } from "vitest";

import { resolveSavedProviderGroupVerified } from "./ModelProvidersPage";

describe("saved provider group verification state", () => {
  it("preserves an authoritative unverified response", () => {
    expect(resolveSavedProviderGroupVerified({
      is_verified: false,
      check: { success: true },
    })).toBe(false);
  });

  it("preserves an authoritative verified response", () => {
    expect(resolveSavedProviderGroupVerified({
      is_verified: true,
      check: { success: false },
    })).toBe(true);
  });

  it("falls back to the legacy check result when is_verified is absent", () => {
    expect(resolveSavedProviderGroupVerified({ check: { success: true } })).toBe(true);
    expect(resolveSavedProviderGroupVerified({ check: { success: false } })).toBe(false);
    expect(resolveSavedProviderGroupVerified({})).toBe(false);
  });
});
