import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const runtimeFeaturesMock = { localLikeAutoLogin: false, hideLocalUserControls: false };

vi.mock("./features", () => ({
  runtimeFeatures: runtimeFeaturesMock,
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getUserInfo: vi.fn(),
    setUserInfo: vi.fn(),
  },
}));

import { AgentAppsAuth } from "@/components/auth";

describe("isLocalSessionEnabled / shouldHideLocalUserControls", () => {
  afterEach(() => {
    runtimeFeaturesMock.localLikeAutoLogin = false;
    runtimeFeaturesMock.hideLocalUserControls = false;
  });

  it("mirror the underlying runtime feature flags", async () => {
    const { isLocalSessionEnabled, shouldHideLocalUserControls } = await import(
      "./localSession"
    );
    expect(isLocalSessionEnabled()).toBe(false);
    expect(shouldHideLocalUserControls()).toBe(false);

    runtimeFeaturesMock.localLikeAutoLogin = true;
    runtimeFeaturesMock.hideLocalUserControls = true;
    expect(isLocalSessionEnabled()).toBe(true);
    expect(shouldHideLocalUserControls()).toBe(true);
  });
});

describe("ensureLocalSession", () => {
  beforeEach(() => {
    vi.mocked(AgentAppsAuth.getUserInfo).mockReset();
    vi.mocked(AgentAppsAuth.setUserInfo).mockReset();
    vi.restoreAllMocks();
  });

  it("returns the current session directly when local session is disabled", async () => {
    runtimeFeaturesMock.localLikeAutoLogin = false;
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({
      token: "tok",
      username: "alice",
    } as never);
    const { ensureLocalSession } = await import("./localSession");
    const result = await ensureLocalSession();
    expect(result?.token).toBe("tok");
  });

  it("returns the cached session when already initialized and not forced", async () => {
    runtimeFeaturesMock.localLikeAutoLogin = true;
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({
      token: "cached-tok",
      username: "bob",
    } as never);
    vi.stubGlobal("fetch", vi.fn());

    vi.resetModules();
    const module = await import("./localSession");
    // Force localSessionInitialized=true via a successful first request.
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      json: async () => ({ data: { token: "server-tok", username: "bob" } }),
    } as Response);
    await module.ensureLocalSession({ force: true });

    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({
      token: "server-tok",
      username: "bob",
    } as never);
    const result = await module.ensureLocalSession();
    expect(result?.token).toBe("server-tok");
    // Only the forced call should have hit fetch.
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("requests a new admin session and stores it when force is set", async () => {
    runtimeFeaturesMock.localLikeAutoLogin = true;
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue(null);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ data: { token: "new-tok", username: "carol" } }),
      } as Response),
    );

    vi.resetModules();
    const module = await import("./localSession");
    await module.ensureLocalSession({ force: true });
    expect(fetch).toHaveBeenCalledWith(
      expect.stringContaining("/_local/admin-session?force=true"),
      expect.objectContaining({ method: "POST" }),
    );
    expect(AgentAppsAuth.setUserInfo).toHaveBeenCalledWith({
      token: "new-tok",
      username: "carol",
    });
  });

  it("throws when the admin session response has no token", async () => {
    runtimeFeaturesMock.localLikeAutoLogin = true;
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue(null);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ data: {} }),
      } as Response),
    );

    vi.resetModules();
    const module = await import("./localSession");
    await expect(module.ensureLocalSession({ force: true })).rejects.toThrow();
  });
});

describe("restoreLocalSessionAndGetToken", () => {
  beforeEach(() => {
    vi.mocked(AgentAppsAuth.getUserInfo).mockReset();
    vi.mocked(AgentAppsAuth.setUserInfo).mockReset();
    vi.restoreAllMocks();
  });

  it("returns the token from a forced session refresh", async () => {
    runtimeFeaturesMock.localLikeAutoLogin = true;
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ data: { token: "restored-tok", username: "dave" } }),
      } as Response),
    );

    vi.resetModules();
    const module = await import("./localSession");
    // After ensuring, the mock will return the newly set info.
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({
      token: "restored-tok",
      username: "dave",
    } as never);
    const token = await module.restoreLocalSessionAndGetToken();
    expect(token).toBe("restored-tok");
  });

  it("throws when the resulting session has no token", async () => {
    runtimeFeaturesMock.localLikeAutoLogin = false;
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({
      token: "",
      username: "eve",
    } as never);

    vi.resetModules();
    const module = await import("./localSession");
    await expect(module.restoreLocalSessionAndGetToken()).rejects.toThrow();
  });
});
