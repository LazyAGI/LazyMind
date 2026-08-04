import { describe, expect, it, vi, beforeEach } from "vitest";

const apiMocks = vi.hoisted(() => ({
  login: vi.fn(),
  register: vi.fn(),
  me: vi.fn(),
  getUser: vi.fn(),
  updateMe: vi.fn(),
  changePassword: vi.fn(),
}));

const authMocks = vi.hoisted(() => ({
  getAccessToken: vi.fn(() => "token-abc"),
  getRefreshToken: vi.fn(() => "refresh-abc"),
  setUserInfo: vi.fn(),
  updateUserInfo: vi.fn(),
}));

vi.mock("@/api/generated/auth-client", () => ({
  Configuration: class {},
  AuthApi: class {
    meApiAuthserviceAuthMeGet = apiMocks.me;
    loginApiAuthserviceAuthLoginPost = apiMocks.login;
    registerApiAuthserviceAuthRegisterPost = apiMocks.register;
    updateMeApiAuthserviceAuthMePatch = apiMocks.updateMe;
    changePasswordApiAuthserviceAuthChangePasswordPost = apiMocks.changePassword;
  },
  UserApi: class {
    getUserApiAuthserviceUserUserIdGet = apiMocks.getUser;
  },
  RoleApi: class {},
  GroupApi: class {},
}));

vi.mock("@/api/generated/authservice-client", () => ({
  Configuration: class {},
  UsersApiFactory: () => ({}),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: {},
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getAccessToken: authMocks.getAccessToken,
    getRefreshToken: authMocks.getRefreshToken,
    setUserInfo: authMocks.setUserInfo,
    updateUserInfo: authMocks.updateUserInfo,
  },
}));

vi.mock("@/runtime/apiBase", () => ({
  getApiBaseUrl: () => "http://localhost",
  authServiceApiUrl: (path: string) => `http://localhost/api/authservice/${path}`,
}));

vi.mock("@/utils/developerMode", () => ({
  syncDeveloperModeFromServer: vi.fn(),
}));

const logoutPost = vi.hoisted(() => vi.fn());
vi.mock("axios", () => ({
  default: {
    create: vi.fn(() => ({ post: logoutPost })),
  },
}));

import {
  changeCurrentUserPassword,
  fetchCurrentUser,
  fetchCurrentUserDetail,
  loginByPassword,
  logoutFromServer,
  registerByPassword,
  storeLoginSession,
  unwrapLoginResponse,
  updateCurrentUserProfile,
} from "./request";
import { syncDeveloperModeFromServer } from "@/utils/developerMode";

beforeEach(() => {
  Object.values(apiMocks).forEach((mockFn) => mockFn.mockReset());
  Object.values(authMocks).forEach((mockFn) => mockFn.mockReset());
  authMocks.getAccessToken.mockReturnValue("token-abc");
  authMocks.getRefreshToken.mockReturnValue("refresh-abc");
  vi.mocked(syncDeveloperModeFromServer).mockReset();
});

describe("unwrapLoginResponse", () => {
  it("returns the response directly when it already has an access_token", () => {
    const response = { access_token: "t1", refresh_token: "r1", role: "user" };
    expect(unwrapLoginResponse(response as any)).toEqual(response);
  });

  it("unwraps a nested data envelope", () => {
    const inner = { access_token: "t1", refresh_token: "r1", role: "user" };
    expect(unwrapLoginResponse({ data: inner } as any)).toEqual(inner);
  });

  it("throws when no access_token can be found", () => {
    expect(() => unwrapLoginResponse({} as any)).toThrow();
  });
});

describe("loginByPassword / registerByPassword", () => {
  it("calls the login endpoint with the given payload", async () => {
    apiMocks.login.mockResolvedValue({ data: { access_token: "t1" } });
    await loginByPassword({ username: "u1", password: "p1" } as any);
    expect(apiMocks.login).toHaveBeenCalledWith({ loginBody: { username: "u1", password: "p1" } });
  });

  it("calls the register endpoint with the given payload", async () => {
    apiMocks.register.mockResolvedValue({ data: {} });
    await registerByPassword({ username: "u1", password: "p1" } as any);
    expect(apiMocks.register).toHaveBeenCalledWith({
      registerBody: { username: "u1", password: "p1" },
    });
  });
});

describe("storeLoginSession", () => {
  it("stores the session then fetches current user and syncs developer mode", async () => {
    apiMocks.me.mockResolvedValue({ data: { user_id: "u1", username: "u1" } });
    vi.mocked(syncDeveloperModeFromServer).mockResolvedValue(false);

    const result = await storeLoginSession(
      { access_token: "t1", refresh_token: "r1", role: "user" } as any,
      "fallback-name",
    );

    expect(authMocks.setUserInfo).toHaveBeenCalledWith(
      expect.objectContaining({ token: "t1", refreshToken: "r1", username: "fallback-name" }),
    );
    expect(apiMocks.me).toHaveBeenCalled();
    expect(syncDeveloperModeFromServer).toHaveBeenCalled();
    expect(result).toBeNull();
  });

  it("swallows errors from fetchCurrentUser/syncDeveloperModeFromServer", async () => {
    apiMocks.me.mockRejectedValue(new Error("network down"));
    await expect(
      storeLoginSession({ access_token: "t1", refresh_token: "r1", role: "user" } as any),
    ).resolves.toBeNull();
  });
});

describe("fetchCurrentUser", () => {
  it("unwraps the response and updates stored user info", async () => {
    apiMocks.me.mockResolvedValue({
      data: {
        user_id: "u1",
        username: "u1name",
        email: "u1@example.com",
        display_name: "U1",
        role: "admin",
        dynamic: true,
        chat_unlike_switch: false,
      },
    });
    const result = await fetchCurrentUser();
    expect(result.user_id).toBe("u1");
    expect(authMocks.updateUserInfo).toHaveBeenCalledWith(
      expect.objectContaining({ userId: "u1", username: "u1name", dynamic: true, chatUnlikeSwitch: false }),
    );
  });

  it("unwraps an enveloped response with a data field", async () => {
    apiMocks.me.mockResolvedValue({
      data: { data: { user_id: "u1", username: "u1name" } },
    });
    const result = await fetchCurrentUser();
    expect(result.user_id).toBe("u1");
  });
});

describe("fetchCurrentUserDetail", () => {
  it("fetches the current user then loads full detail by id", async () => {
    apiMocks.me.mockResolvedValue({ data: { user_id: "u1", username: "u1name" } });
    apiMocks.getUser.mockResolvedValue({
      data: { user_id: "u1", username: "u1name", role_name: "admin" },
    });
    const result = await fetchCurrentUserDetail();
    expect(apiMocks.getUser).toHaveBeenCalledWith({ userId: "u1" });
    expect(result.role_name).toBe("admin");
    expect(authMocks.updateUserInfo).toHaveBeenCalledWith(
      expect.objectContaining({ userId: "u1", role: "admin" }),
    );
  });
});

describe("updateCurrentUserProfile / changeCurrentUserPassword", () => {
  it("forwards the profile payload to the update endpoint", async () => {
    apiMocks.updateMe.mockResolvedValue({ data: {} });
    await updateCurrentUserProfile({ display_name: "new name" });
    expect(apiMocks.updateMe).toHaveBeenCalledWith({ updateMeBody: { display_name: "new name" } });
  });

  it("forwards old/new passwords to the change-password endpoint", async () => {
    apiMocks.changePassword.mockResolvedValue({ data: {} });
    await changeCurrentUserPassword("old-pw", "new-pw");
    expect(apiMocks.changePassword).toHaveBeenCalledWith({
      changePasswordBody: { old_password: "old-pw", new_password: "new-pw" },
    });
  });
});

describe("logoutFromServer", () => {
  it("does nothing when there is no refresh token", async () => {
    authMocks.getRefreshToken.mockReturnValue("");
    await logoutFromServer();
    expect(logoutPost).not.toHaveBeenCalled();
  });

  it("posts the refresh token to the logout endpoint when present", async () => {
    logoutPost.mockResolvedValue({});
    await logoutFromServer();
    expect(logoutPost).toHaveBeenCalledWith(
      expect.stringContaining("auth/logout"),
      { refresh_token: "refresh-abc" },
    );
  });

  it("swallows errors from the logout request", async () => {
    logoutPost.mockRejectedValue(new Error("network down"));
    await expect(logoutFromServer()).resolves.toBeUndefined();
  });
});
