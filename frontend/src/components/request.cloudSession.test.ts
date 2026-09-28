import { AxiosError, AxiosHeaders, type InternalAxiosRequestConfig } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ logout: vi.fn(), refresh: vi.fn(), restore: vi.fn(), ensure: vi.fn() }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { logout: mocks.logout, refreshAccessToken: mocks.refresh, getAuthHeaders: () => ({}), getUserInfo: () => ({ id: "local-owner" }), isLoggedIn: () => true } }));
vi.mock("@/runtime/localSession", () => ({ isLocalSessionEnabled: () => true, localSessionInitialized: () => true, restoreLocalSessionAndGetToken: mocks.restore, ensureLocalSession: mocks.ensure }));
const requestUiMocks = vi.hoisted(() => ({ error: vi.fn(), warning: vi.fn() }));
vi.mock("antd", () => ({ message: requestUiMocks }));
import { handleError, setTransientRequestErrorsSuppressed } from "./request";

function unauthorized(code: number, url = "/api/core/cloud/skills/cloud-id/tree") {
  const config = { url, method: "get", headers: new AxiosHeaders(), _retry: true } as InternalAxiosRequestConfig;
  return new AxiosError("fixture request rejected", "ERR_BAD_REQUEST", config, undefined, {
    status: 401, statusText: "Unauthorized", headers: {}, config,
    data: { code, message: "请重新登录 Cloud", request_id: "fixture-request" },
  });
}

describe("Cloud and local authentication boundaries", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setTransientRequestErrorsSuppressed(false);
    mocks.restore.mockRejectedValue(new Error("fixture local restoration unavailable"));
  });

  it("does not restore, replay or log out the local session for the dedicated Cloud session code", async () => {
    const error = unauthorized(2002920);
    await expect(handleError(error)).rejects.toBe(error);
    expect(mocks.restore).not.toHaveBeenCalled(); expect(mocks.refresh).not.toHaveBeenCalled(); expect(mocks.logout).not.toHaveBeenCalled();
  });

  it("continues local authentication recovery for a real local 401 on a cloud URL", async () => {
    const error = unauthorized(2000104);
    await expect(handleError(error)).rejects.toBe(error);
    expect(mocks.restore).toHaveBeenCalledTimes(1); expect(mocks.logout).toHaveBeenCalledTimes(1);
  });

  it("does not use a human-readable Cloud message to classify a local authentication error", async () => {
    const error = unauthorized(2000104, "/api/core/skills");
    await expect(handleError(error)).rejects.toBe(error);
    expect(mocks.restore).toHaveBeenCalledTimes(1);
  });

  it("suppresses only transient runtime errors while an Obsidian transition is active", async () => {
    const config = { url: "/api/core/cloud/skills/cloud-id/tree", method: "get", headers: new AxiosHeaders() } as InternalAxiosRequestConfig;
    const networkError = new AxiosError("fixture request rejected", "ERR_NETWORK", config, {});

    await expect(handleError(networkError)).rejects.toBe(networkError);
    expect(requestUiMocks.error).toHaveBeenCalledTimes(1);

    requestUiMocks.error.mockClear();
    setTransientRequestErrorsSuppressed(true);
    await expect(handleError(networkError)).rejects.toBe(networkError);
    expect(requestUiMocks.error).not.toHaveBeenCalled();

    const unavailableError = new AxiosError("fixture runtime unavailable", "ERR_NETWORK", config);
    await expect(handleError(unavailableError)).rejects.toBe(unavailableError);
    expect(requestUiMocks.error).not.toHaveBeenCalled();

    const badRequest = new AxiosError("fixture request rejected", "ERR_BAD_REQUEST", config, undefined, {
      status: 400,
      statusText: "Bad Request",
      headers: {},
      config,
      data: {},
    });
    await expect(handleError(badRequest)).rejects.toBe(badRequest);
    expect(requestUiMocks.error).toHaveBeenCalledTimes(1);

    setTransientRequestErrorsSuppressed(false);
  });
});
