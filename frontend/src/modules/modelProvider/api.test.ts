import { describe, expect, it, vi } from "vitest";
import { unwrapModelProviderData, withModelProviderJsonOptions } from "./api";

vi.mock("@/components/request", () => ({
  BASE_URL: "",
  axiosInstance: {},
}));

vi.mock("@/api/generated/core-client", () => ({
  Configuration: class {},
  DefaultApiFactory: () => ({}),
  ModelProvidersApiFactory: () => ({}),
}));

describe("withModelProviderJsonOptions", () => {
  it("adds a JSON content-type header while preserving existing options", () => {
    const result = withModelProviderJsonOptions({ timeout: 5000, headers: { "X-Foo": "bar" } });
    expect(result).toEqual({
      timeout: 5000,
      headers: { "Content-Type": "application/json", "X-Foo": "bar" },
    });
  });

  it("defaults to an empty options object when none is provided", () => {
    const result = withModelProviderJsonOptions();
    expect(result.headers).toEqual({ "Content-Type": "application/json" });
  });

  it("lets caller-provided headers override the default content type", () => {
    const result = withModelProviderJsonOptions({
      headers: { "Content-Type": "multipart/form-data" },
    });
    expect(result.headers).toEqual({ "Content-Type": "multipart/form-data" });
  });
});

describe("unwrapModelProviderData", () => {
  it("unwraps a payload with a data field", () => {
    expect(unwrapModelProviderData({ data: { id: 1 } })).toEqual({ id: 1 });
  });

  it("returns the payload unchanged when there is no data field", () => {
    expect(unwrapModelProviderData({ id: 1 })).toEqual({ id: 1 });
  });

  it("returns primitives as-is", () => {
    expect(unwrapModelProviderData("plain-string")).toBe("plain-string");
  });
});
