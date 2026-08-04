import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import axios from "axios";

vi.mock("@/modules/signin/utils/request", () => ({
  logoutFromServer: vi.fn().mockResolvedValue(undefined),
}));

const STORAGE_KEY = "lazymind:user";

function encodeBase64Url(json: Record<string, unknown>) {
  const base64 = btoa(JSON.stringify(json));
  return base64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function buildJwt(payload: Record<string, unknown>) {
  const header = encodeBase64Url({ alg: "none" });
  const body = encodeBase64Url(payload);
  return `${header}.${body}.sig`;
}

describe("AgentAppsAuth", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("getUserInfo returns null when nothing is stored", async () => {
    const { AgentAppsAuth } = await import("./auth");
    expect(AgentAppsAuth.getUserInfo()).toBeNull();
  });

  it("getUserInfo returns null and swallows errors for malformed JSON", async () => {
    localStorage.setItem(STORAGE_KEY, "{not-json");
    const { AgentAppsAuth } = await import("./auth");
    expect(AgentAppsAuth.getUserInfo()).toBeNull();
  });

  it("resolves and backfills userId from a JWT sub claim when missing", async () => {
    const token = buildJwt({ sub: "user-42" });
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ token, username: "alice" }));
    const { AgentAppsAuth } = await import("./auth");
    const info = AgentAppsAuth.getUserInfo();
    expect(info?.userId).toBe("user-42");
    // Backfilled value should be persisted back to storage.
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) || "{}");
    expect(stored.userId).toBe("user-42");
  });

  it("falls back to username when no userId or JWT claim is available", async () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ token: "not-a-jwt", username: "bob" }),
    );
    const { AgentAppsAuth } = await import("./auth");
    expect(AgentAppsAuth.getUserInfo()?.userId).toBe("bob");
  });

  it("getAccessToken / getRefreshToken / isLoggedIn reflect stored state", async () => {
    const { AgentAppsAuth } = await import("./auth");
    expect(AgentAppsAuth.getAccessToken()).toBe("");
    expect(AgentAppsAuth.getRefreshToken()).toBe("");
    expect(AgentAppsAuth.isLoggedIn()).toBe(false);

    AgentAppsAuth.setUserInfo({
      token: "tok-1",
      username: "carol",
      refreshToken: "refresh-1",
    });
    expect(AgentAppsAuth.getAccessToken()).toBe("tok-1");
    expect(AgentAppsAuth.getRefreshToken()).toBe("refresh-1");
    expect(AgentAppsAuth.isLoggedIn()).toBe(true);
  });

  it("getAuthHeaders includes Authorization, X-User-Id, and X-Tenant-ID when present", async () => {
    const { AgentAppsAuth } = await import("./auth");
    AgentAppsAuth.setUserInfo({
      token: "tok-2",
      username: "dave",
      userId: "user-7",
      tenantId: "tenant-9",
    });
    expect(AgentAppsAuth.getAuthHeaders()).toEqual({
      authorization: "Bearer tok-2",
      "X-User-Id": "user-7",
      "X-Tenant-ID": "tenant-9",
    });
  });

  it("getAuthHeaders returns an empty object when logged out", async () => {
    const { AgentAppsAuth } = await import("./auth");
    expect(AgentAppsAuth.getAuthHeaders()).toEqual({});
  });

  it("setUserInfo dispatches the user-change event and updateUserInfo patches existing info", async () => {
    const { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } = await import("./auth");
    const listener = vi.fn();
    window.addEventListener(AUTH_USER_CHANGE_EVENT, listener);

    AgentAppsAuth.setUserInfo({ token: "tok-3", username: "erin" });
    expect(listener).toHaveBeenCalledTimes(1);

    AgentAppsAuth.updateUserInfo({ displayName: "Erin Doe" });
    expect(AgentAppsAuth.getUserInfo()?.displayName).toBe("Erin Doe");
    expect(listener).toHaveBeenCalledTimes(2);

    window.removeEventListener(AUTH_USER_CHANGE_EVENT, listener);
  });

  it("updateUserInfo is a no-op when there is no current user", async () => {
    const { AgentAppsAuth } = await import("./auth");
    AgentAppsAuth.updateUserInfo({ displayName: "Ghost" });
    expect(AgentAppsAuth.getUserInfo()).toBeNull();
  });

  it("clearUserInfo clears storage and notifies listeners", async () => {
    const { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } = await import("./auth");
    AgentAppsAuth.setUserInfo({ token: "tok-4", username: "frank" });
    const listener = vi.fn();
    window.addEventListener(AUTH_USER_CHANGE_EVENT, listener);

    AgentAppsAuth.clearUserInfo();
    expect(AgentAppsAuth.getUserInfo()).toBeNull();
    expect(listener).toHaveBeenCalledTimes(1);

    window.removeEventListener(AUTH_USER_CHANGE_EVENT, listener);
  });

  it("getLoginUrl builds a login path relative to window origin and BASENAME", async () => {
    const { AgentAppsAuth } = await import("./auth");
    expect(AgentAppsAuth.getLoginUrl()).toBe(`${window.location.origin}/login`);

    (window as Window & { BASENAME?: string }).BASENAME = "/app";
    expect(AgentAppsAuth.getLoginUrl()).toBe(`${window.location.origin}/app/login`);
    delete (window as Window & { BASENAME?: string }).BASENAME;
  });

  it("refreshAccessToken throws when there is no refresh token", async () => {
    const { AgentAppsAuth } = await import("./auth");
    await expect(AgentAppsAuth.refreshAccessToken()).rejects.toThrow(
      "No refresh token available",
    );
  });

  it("refreshAccessToken posts to the refresh endpoint and updates stored tokens", async () => {
    const { AgentAppsAuth } = await import("./auth");
    AgentAppsAuth.setUserInfo({
      token: "old-token",
      username: "grace",
      refreshToken: "old-refresh",
    });

    const postSpy = vi.fn().mockResolvedValue({
      data: {
        data: {
          access_token: "new-token",
          refresh_token: "new-refresh",
        },
      },
    });
    vi.spyOn(axios, "create").mockReturnValue({ post: postSpy } as unknown as ReturnType<
      typeof axios.create
    >);

    const token = await AgentAppsAuth.refreshAccessToken();
    expect(token).toBe("new-token");
    expect(postSpy).toHaveBeenCalledWith(
      expect.stringContaining("/api/authservice/auth/refresh"),
      { refresh_token: "old-refresh" },
    );
    expect(AgentAppsAuth.getUserInfo()?.token).toBe("new-token");
    expect(AgentAppsAuth.getUserInfo()?.refreshToken).toBe("new-refresh");
  });

  it("refreshAccessToken throws when the response has no access_token", async () => {
    const { AgentAppsAuth } = await import("./auth");
    AgentAppsAuth.setUserInfo({
      token: "old-token",
      username: "henry",
      refreshToken: "old-refresh",
    });

    vi.spyOn(axios, "create").mockReturnValue({
      post: vi.fn().mockResolvedValue({ data: {} }),
    } as unknown as ReturnType<typeof axios.create>);

    await expect(AgentAppsAuth.refreshAccessToken()).rejects.toThrow();
  });
});
