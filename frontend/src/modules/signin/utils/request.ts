import axios from "axios";
import {
  AuthApi,
  UserApi,
  RoleApi,
  GroupApi,
  Configuration,
  type LoginBody,
  type LoginResponse,
  type MeResponse,
  type RegisterBody,
  type UserDetailResponse,
} from "@/api/generated/auth-client";
import {
  Configuration as AuthServiceConfiguration,
  UsersApiFactory,
} from "@/api/generated/authservice-client";
import { axiosInstance } from "@/components/request";
import { AgentAppsAuth } from "@/components/auth";
import { authServiceApiUrl, getApiBaseUrl } from "@/runtime/apiBase";

const baseUrl = getApiBaseUrl();
const authServiceBaseUrl = authServiceApiUrl("v1");

const commonBaseOptions = {
  headers: { "Content-Type": "application/json" },
};

function getConfiguration(accessToken?: string) {
  return new Configuration({
    basePath: baseUrl,
    baseOptions: commonBaseOptions,
    accessToken: accessToken || AgentAppsAuth.getAccessToken(),
  });
}

export function createAuthApi(accessToken?: string) {
  return new AuthApi(getConfiguration(accessToken), baseUrl, axiosInstance);
}

export function createUserApi(accessToken?: string) {
  return new UserApi(getConfiguration(accessToken), baseUrl, axiosInstance);
}

export function createRoleApi(accessToken?: string) {
  return new RoleApi(getConfiguration(accessToken), baseUrl, axiosInstance);
}

export function createGroupApi(accessToken?: string) {
  return new GroupApi(getConfiguration(accessToken), baseUrl, axiosInstance);
}

export function createUsersServiceApi() {
  return UsersApiFactory(
    new AuthServiceConfiguration(),
    authServiceBaseUrl,
    axiosInstance,
  );
}

export async function loginByPassword(payload: LoginBody) {
  return createAuthApi().loginApiAuthserviceAuthLoginPost({
    loginBody: payload,
  });
}

export async function registerByPassword(payload: RegisterBody) {
  return createAuthApi().registerApiAuthserviceAuthRegisterPost({
    registerBody: payload,
  });
}

export async function storeLoginSession(
  loginResponse: LoginResponse,
  fallbackUsername?: string,
) {
  AgentAppsAuth.setUserInfo({
    token: loginResponse.access_token,
    refreshToken: loginResponse.refresh_token,
    username: fallbackUsername || "",
    role: loginResponse.role,
    timestamp: Date.now(),
  });

  try {
    await fetchCurrentUser();
  } catch {
  }

  return null;
}

type WrappedApiResponse<T> = {
  code?: number;
  message?: string;
  data?: T;
};

export type CurrentUserResponse = MeResponse & {
  dynamic?: boolean;
  chat_unlike_switch?: boolean;
};

function unwrapApiResponse<T>(responseData: T | WrappedApiResponse<T>): T {
  return ((responseData as WrappedApiResponse<T>)?.data ||
    responseData) as T;
}

export function unwrapLoginResponse(
  responseData: LoginResponse | WrappedApiResponse<LoginResponse>,
): LoginResponse {
  const normalized = unwrapApiResponse(responseData);

  if (!normalized?.access_token) {
    throw new Error("登录成功，但返回结果中缺少 access_token");
  }

  return normalized;
}

export async function fetchCurrentUser(): Promise<CurrentUserResponse> {
  const response = await createAuthApi().meApiAuthserviceAuthMeGet();
  const currentUser = unwrapApiResponse(
    response.data as any,
  ) as CurrentUserResponse;

  AgentAppsAuth.updateUserInfo({
    userId: currentUser.user_id,
    username: currentUser.username,
    email: currentUser.email || undefined,
    displayName: currentUser.display_name || undefined,
    role: currentUser.role,
    dynamic: currentUser.dynamic === true,
    chatUnlikeSwitch: currentUser.chat_unlike_switch === true,
  });

  return currentUser;
}

export async function fetchCurrentUserDetail(): Promise<UserDetailResponse> {
  const currentUser = await fetchCurrentUser();
  const response = await createUserApi().getUserApiAuthserviceUserUserIdGet({
    userId: currentUser.user_id,
  });
  const userDetail = unwrapApiResponse(response.data as any);

  AgentAppsAuth.updateUserInfo({
    userId: userDetail.user_id,
    username: userDetail.username,
    email: userDetail.email || undefined,
    displayName: userDetail.display_name || undefined,
    phone: userDetail.phone || undefined,
    role: userDetail.role_name,
  });

  return userDetail;
}

export async function updateCurrentUserProfile(
  payload: {
    display_name?: string;
    email?: string;
    phone?: string;
    remark?: string;
  }
) {
  return createAuthApi().updateMeApiAuthserviceAuthMePatch({
    updateMeBody: payload,
  });
}

export async function changeCurrentUserPassword(
  oldPassword: string,
  newPassword: string,
) {
  return createAuthApi().changePasswordApiAuthserviceAuthChangePasswordPost({
    changePasswordBody: {
      old_password: oldPassword,
      new_password: newPassword,
    },
  });
}

export async function logoutFromServer() {
  const refreshToken = AgentAppsAuth.getRefreshToken();
  const accessToken = AgentAppsAuth.getAccessToken();

  if (!refreshToken) {
    return;
  }

  try {
    const logoutUrl = authServiceApiUrl("auth/logout");

    const logoutAxios = axios.create({
      timeout: 10000,
      headers: {
        "Content-Type": "application/json",
        "Authorization": `Bearer ${accessToken}`,
      },
    });

    await logoutAxios.post(logoutUrl, {
      refresh_token: refreshToken,
    });
  } catch (error) {
    console.error("Logout API call failed:", error);
  }
}
