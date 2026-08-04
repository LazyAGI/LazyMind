import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({
  get: vi.fn(),
  patch: vi.fn(),
}));

vi.mock("@/api/generated/core-client", () => ({
  Configuration: class {},
  UserApiFactory: () => ({
    apiCoreUserUiPreferencesGet: apiMocks.get,
    apiCoreUserUiPreferencesPatch: apiMocks.patch,
  }),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: {},
  BASE_URL: "http://localhost",
}));

import { fetchUserUiPreferences, patchUserUiPreferences } from "./uiPreferencesApi";

beforeEach(() => {
  apiMocks.get.mockReset();
  apiMocks.patch.mockReset();
});

describe("fetchUserUiPreferences", () => {
  it("unwraps a plain response payload", async () => {
    apiMocks.get.mockResolvedValue({ data: { theme: "dark" } });
    const result = await fetchUserUiPreferences();
    expect(result).toEqual({ theme: "dark" });
  });

  it("unwraps a response wrapped in a data envelope", async () => {
    apiMocks.get.mockResolvedValue({ data: { data: { theme: "light" } } });
    const result = await fetchUserUiPreferences();
    expect(result).toEqual({ theme: "light" });
  });

  it("forwards options through to the underlying api call", async () => {
    apiMocks.get.mockResolvedValue({ data: { theme: "dark" } });
    const options = { headers: { "X-Test": "1" } } as never;
    await fetchUserUiPreferences(options);
    expect(apiMocks.get).toHaveBeenCalledWith(options);
  });
});

describe("patchUserUiPreferences", () => {
  it("sends the patch body and unwraps the response", async () => {
    apiMocks.patch.mockResolvedValue({ data: { theme: "dark" } });
    const result = await patchUserUiPreferences({ theme: "dark" } as never);
    expect(apiMocks.patch).toHaveBeenCalledWith({
      userUIPreferencesPatchOpenAPIRequest: { theme: "dark" },
    });
    expect(result).toEqual({ theme: "dark" });
  });

  it("unwraps a response wrapped in a data envelope", async () => {
    apiMocks.patch.mockResolvedValue({ data: { data: { theme: "light" } } });
    const result = await patchUserUiPreferences({ theme: "light" } as never);
    expect(result).toEqual({ theme: "light" });
  });
});
