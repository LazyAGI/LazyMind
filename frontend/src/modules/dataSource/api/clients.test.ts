import { describe, expect, it, vi } from "vitest";

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn() },
}));

describe("dataSource api/clients", () => {
  it("instantiates the datasets, model providers, cloud oauth and scan clients", async () => {
    const {
      dataSourceDatasetsApi,
      dataSourceModelProvidersApi,
      dataSourceCloudOauthApi,
      dataSourceScanApi,
    } = await import("./clients");

    expect(dataSourceDatasetsApi).toBeTruthy();
    expect(dataSourceModelProvidersApi).toBeTruthy();
    expect(dataSourceCloudOauthApi).toBeTruthy();
    expect(dataSourceScanApi).toBeTruthy();
  });

  it("exposes expected factory methods on the cloud oauth client", async () => {
    const { dataSourceCloudOauthApi } = await import("./clients");
    expect(typeof dataSourceCloudOauthApi.oauthAuthorizeUrlApiAuthserviceV1CloudProviderOauthAuthorizeUrlPost).toBe(
      "function",
    );
    expect(typeof dataSourceCloudOauthApi.oauthCallbackApiAuthserviceV1CloudProviderOauthCallbackPost).toBe(
      "function",
    );
  });

  it("exposes expected methods on the scan client instance", async () => {
    const { dataSourceScanApi } = await import("./clients");
    expect(typeof dataSourceScanApi.createSource).toBe("function");
  });
});
