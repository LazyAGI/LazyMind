import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ adapter: vi.fn() }));
vi.mock("@/components/request", async () => {
  const { default: axios } = await import("axios");
  return {
    BASE_URL: "http://localhost:9000",
    axiosInstance: axios.create({ adapter: mocks.adapter }),
  };
});

import { dataSourceProviderConnectionsApi } from "./clients";

beforeEach(() => {
  mocks.adapter.mockReset().mockImplementation(async (config) => ({
    data: { session_id: "session", status: "WAITING_USER" },
    status: 200, statusText: "OK", headers: {}, config,
  }));
});

describe("generated Provider Connection transport", () => {
  it("serializes the provider as JSON through the shared client", async () => {
    const response = await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsSessionsPost({
      providerConnectionCreateRequest: { provider: "feishu" },
    });
    const config = mocks.adapter.mock.calls[0][0];
    expect(config.url).toBe("http://localhost:9000/api/core/provider-connections/sessions");
    expect(config.method).toBe("post");
    expect(JSON.parse(config.data)).toEqual({ provider: "feishu" });
    expect(config.headers.get("Content-Type")).toBe("application/json");
    expect(response.data.session_id).toBe("session");
  });

  it("encodes path parameters for polling and reauthorization", async () => {
    await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsSessionsSessionIdGet({ sessionId: "a/b" });
    await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsAuthConnectionIdReauthorizePost({ authConnectionId: "a/b" });
    expect(mocks.adapter.mock.calls[0][0].url).toBe("http://localhost:9000/api/core/provider-connections/sessions/a%2Fb");
    expect(mocks.adapter.mock.calls[1][0]).toMatchObject({
      method: "post", url: "http://localhost:9000/api/core/provider-connections/a%2Fb:reauthorize",
    });
    expect(mocks.adapter.mock.calls[1][0].data).toBeUndefined();
  });

  it("preserves silent error options during connection reconciliation", async () => {
    const options = { silentError: true, timeout: 1000 };
    await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsGet(options);
    expect(mocks.adapter.mock.calls[0][0]).toMatchObject({ method: "get", silentError: true });
  });
});
