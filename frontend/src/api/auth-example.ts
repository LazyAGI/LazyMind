import { AuthApi, UserApi, RoleApi, Configuration } from '@/api/generated/auth-client';
import { axiosInstance } from '@/components/request';

const BASE_PATH = import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8000';

// Helper: create a Configuration with optional access token
function createConfig(token?: string): Configuration {
  return new Configuration({
    basePath: BASE_PATH,
    ...(token ? { accessToken: token } : {}),
  });
}

// Shared unauthenticated API instances
const createAuthApi = (token?: string) =>
  new AuthApi(createConfig(token), BASE_PATH, axiosInstance);
const createUserApi = (token: string) =>
  new UserApi(createConfig(token), BASE_PATH, axiosInstance);
const createRoleApi = (token: string) =>
  new RoleApi(createConfig(token), BASE_PATH, axiosInstance);
const authApi = createAuthApi();

export async function registerUser(username: string, password: string, email?: string) {
  try {
    const response = await authApi.registerApiAuthserviceAuthRegisterPost({
      registerBody: {
        username,
        password,
        confirm_password: password,
        email,
      },
    });
    return response.data;
  } catch (error) {
    console.error('注册失败:', error);
    throw error;
  }
}

export async function loginUser(username: string, password: string) {
  try {
    const response = await authApi.loginApiAuthserviceAuthLoginPost({
      loginBody: { username, password },
    });
    return response.data;
  } catch (error) {
    console.error('登录失败:', error);
    throw error;
  }
}

export async function getCurrentUser(token: string) {
  try {
    const response = await createAuthApi(token).meApiAuthserviceAuthMeGet();
    return response.data;
  } catch (error) {
    console.error('获取用户信息失败:', error);
    throw error;
  }
}

export async function refreshToken(token: string) {
  try {
    const response = await authApi.refreshApiAuthserviceAuthRefreshPost({
      refreshBody: { refresh_token: token },
    });
    return response.data;
  } catch (error) {
    console.error('刷新 Token 失败:', error);
    throw error;
  }
}

export async function changePassword(token: string, oldPassword: string, newPassword: string) {
  try {
    const response = await createAuthApi(token).changePasswordApiAuthserviceAuthChangePasswordPost({
      changePasswordBody: {
        old_password: oldPassword,
        new_password: newPassword,
      },
    });
    return response.data;
  } catch (error) {
    console.error('修改密码失败:', error);
    throw error;
  }
}

export async function logoutUser(token: string, refreshToken?: string) {
  try {
    const response = await createAuthApi(token).logoutApiAuthserviceAuthLogoutPost({
      logoutBody: { refresh_token: refreshToken },
    });
    return response.data;
  } catch (error) {
    console.error('登出失败:', error);
    throw error;
  }
}

export async function getUserList(token: string, page = 1, pageSize = 20, search?: string) {
  try {
    const response = await createUserApi(token).listUsersApiAuthserviceUserGet({
      page,
      pageSize,
      search,
    });
    return response.data;
  } catch (error) {
    console.error('获取用户列表失败:', error);
    throw error;
  }
}

export async function createUser(
  token: string,
  username: string,
  password: string,
  email?: string,
  roleId?: string,
) {
  try {
    const response = await createUserApi(token).createUserApiAuthserviceUserPost({
      createUserBody: {
        username,
        password,
        email,
        role_id: roleId,
      },
    });
    return response.data;
  } catch (error) {
    console.error('创建用户失败:', error);
    throw error;
  }
}

export async function getRoleList(token: string) {
  try {
    const response = await createRoleApi(token).listRolesApiAuthserviceRoleGet();
    return response.data;
  } catch (error) {
    console.error('获取角色列表失败:', error);
    throw error;
  }
}
