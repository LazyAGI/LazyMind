import { describe, expect, it } from "vitest";
import { canDiscoverWithoutApiKey } from "./providerAuthentication";

describe("keyless model discovery", () => {
  const group = { baseUrl: "http://localhost:8000/v1/", apiKeyConfigured: false, keys: [] };
  it.each(["source", "protocolName", "name"] as const)("recognizes a built-in vLLM by %s", (key) => {
    expect(canDiscoverWithoutApiKey({ isCustom: false, baseUrl: group.baseUrl, [key]: "vLLM" }, group)).toBe(true);
  });
  it("keeps disabled saved keys and invalid URLs out of keyless mode", () => {
    const provider = { isCustom: false, baseUrl: group.baseUrl, name: "vLLM" };
    expect(canDiscoverWithoutApiKey(provider, { ...group, keys: [{ id: "old", masked: "****test", enabled: false }] })).toBe(false);
    expect(canDiscoverWithoutApiKey({ ...provider, baseUrl: "file:///tmp/model" })).toBe(false);
    expect(canDiscoverWithoutApiKey({ ...provider, name: "OpenAI" }, group)).toBe(false);
  });
});
