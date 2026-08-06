import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const registerMock = vi.hoisted(() => vi.fn());
const loginMock = vi.hoisted(() => vi.fn());
const meMock = vi.hoisted(() => vi.fn());
const refreshMock = vi.hoisted(() => vi.fn());
const changePasswordMock = vi.hoisted(() => vi.fn());
const logoutMock = vi.hoisted(() => vi.fn());
const listUsersMock = vi.hoisted(() => vi.fn());
const createUserMock = vi.hoisted(() => vi.fn());
const listRolesMock = vi.hoisted(() => vi.fn());

vi.mock("@/api/generated/auth-client", () => ({
  Configuration: class {
    constructor(public options: unknown) {}
  },
  AuthApi: class {
    registerApiAuthserviceAuthRegisterPost = registerMock;
    loginApiAuthserviceAuthLoginPost = loginMock;
    meApiAuthserviceAuthMeGet = meMock;
    refreshApiAuthserviceAuthRefreshPost = refreshMock;
    changePasswordApiAuthserviceAuthChangePasswordPost = changePasswordMock;
    logoutApiAuthserviceAuthLogoutPost = logoutMock;
  },
  UserApi: class {
    listUsersApiAuthserviceUserGet = listUsersMock;
    createUserApiAuthserviceUserPost = createUserMock;
  },
  RoleApi: class {
    listRolesApiAuthserviceRoleGet = listRolesMock;
  },
}));

vi.mock("@/components/request", () => ({
  axiosInstance: {},
}));

import {
  changePassword,
  createUser,
  getCurrentUser,
  getRoleList,
  getUserList,
  loginUser,
  logoutUser,
  refreshToken,
  registerUser,
} from "./auth-example";

describe("auth-example", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(console, "error").mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("registerUser posts the registration payload and returns response data", async () => {
    registerMock.mockResolvedValue({ data: { id: "u1" } });
    const result = await registerUser("alice", "secret", "alice@example.com");

    expect(registerMock).toHaveBeenCalledWith({
      registerBody: {
        username: "alice",
        password: "secret",
        confirm_password: "secret",
        email: "alice@example.com",
      },
    });
    expect(result).toEqual({ id: "u1" });
  });

  it("registerUser logs and rethrows on failure", async () => {
    const error = new Error("register failed");
    registerMock.mockRejectedValue(error);

    await expect(registerUser("alice", "secret")).rejects.toBe(error);
    expect(console.error).toHaveBeenCalled();
  });

  it("loginUser posts credentials and returns response data", async () => {
    loginMock.mockResolvedValue({ data: { access_token: "tok" } });
    const result = await loginUser("alice", "secret");

    expect(loginMock).toHaveBeenCalledWith({
      loginBody: { username: "alice", password: "secret" },
    });
    expect(result).toEqual({ access_token: "tok" });
  });

  it("getCurrentUser fetches the current user using the given token", async () => {
    meMock.mockResolvedValue({ data: { username: "alice" } });
    const result = await getCurrentUser("tok");

    expect(meMock).toHaveBeenCalled();
    expect(result).toEqual({ username: "alice" });
  });

  it("getCurrentUser logs and rethrows on failure", async () => {
    const error = new Error("me failed");
    meMock.mockRejectedValue(error);

    await expect(getCurrentUser("tok")).rejects.toBe(error);
    expect(console.error).toHaveBeenCalled();
  });

  it("refreshToken posts the refresh token", async () => {
    refreshMock.mockResolvedValue({ data: { access_token: "new-tok" } });
    const result = await refreshToken("old-refresh-tok");

    expect(refreshMock).toHaveBeenCalledWith({
      refreshBody: { refresh_token: "old-refresh-tok" },
    });
    expect(result).toEqual({ access_token: "new-tok" });
  });

  it("changePassword posts old and new passwords", async () => {
    changePasswordMock.mockResolvedValue({ data: { ok: true } });
    const result = await changePassword("tok", "old", "new");

    expect(changePasswordMock).toHaveBeenCalledWith({
      changePasswordBody: { old_password: "old", new_password: "new" },
    });
    expect(result).toEqual({ ok: true });
  });

  it("logoutUser posts an optional refresh token", async () => {
    logoutMock.mockResolvedValue({ data: { ok: true } });
    await logoutUser("tok", "refresh-1");

    expect(logoutMock).toHaveBeenCalledWith({
      logoutBody: { refresh_token: "refresh-1" },
    });
  });

  it("getUserList forwards pagination and search params", async () => {
    listUsersMock.mockResolvedValue({ data: { items: [] } });
    const result = await getUserList("tok", 2, 50, "bob");

    expect(listUsersMock).toHaveBeenCalledWith({ page: 2, pageSize: 50, search: "bob" });
    expect(result).toEqual({ items: [] });
  });

  it("createUser posts the new user payload including optional fields", async () => {
    createUserMock.mockResolvedValue({ data: { id: "u2" } });
    const result = await createUser("tok", "bob", "secret", "bob@example.com", "role-1");

    expect(createUserMock).toHaveBeenCalledWith({
      createUserBody: {
        username: "bob",
        password: "secret",
        email: "bob@example.com",
        role_id: "role-1",
      },
    });
    expect(result).toEqual({ id: "u2" });
  });

  it("getRoleList logs and rethrows on failure", async () => {
    const error = new Error("roles failed");
    listRolesMock.mockRejectedValue(error);

    await expect(getRoleList("tok")).rejects.toBe(error);
    expect(console.error).toHaveBeenCalled();
  });

  it("getRoleList returns the role list on success", async () => {
    listRolesMock.mockResolvedValue({ data: { items: [{ id: "role-1" }] } });
    const result = await getRoleList("tok");

    expect(result).toEqual({ items: [{ id: "role-1" }] });
  });
});
