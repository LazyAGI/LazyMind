/**
 * Minimal auth for LazyMind: getUserInfo from storage, logout redirect to /login.
 * Compatible with AuthServiceApi login (token stored after username/password login).
 */
import axios from "axios";
import { authServiceApiUrl } from "@/runtime/apiBase";

const STORAGE_KEY = "lazymind:user";
export const AUTH_USER_CHANGE_EVENT = "lazymind:user-change";

function decodeBase64Url(value: string) {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
  return atob(padded);
}

function decodeJwtPayload(token?: string): Record<string, unknown> | null {
  if (!token) return null;
  const parts = token.split(".");
  if (parts.length < 2) return null;

  try {
    const decoded = decodeBase64Url(parts[1]);
    return JSON.parse(decoded) as Record<string, unknown>;
  } catch {
    return null;
  }
}

function resolveUserId(userInfo?: Partial<UserInfo> | null) {
  if (userInfo?.userId) {
    return userInfo.userId;
  }

  const payload = decodeJwtPayload(userInfo?.token);
  const candidate = payload?.sub || payload?.user_id || payload?.uid;
  if (typeof candidate === "string") {
    return candidate;
  }

  return userInfo?.username || undefined;
}

export interface UserInfo {
  token: string;
  username: string;
  userId?: string;
  role?: string;
  email?: string;
  displayName?: string;
  phone?: string;
  clientId?: string;
  tenantId?: string;
  tenant_id?: string;
  tenantKey?: string;
  tenant_key?: string;
  loginType?: string;
  idToken?: string;
  refreshToken?: string;
  dynamic?: boolean;
  chatUnlikeSwitch?: boolean;
  timestamp?: number;
}

function getStored(): UserInfo | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as UserInfo;
    const resolvedUserId = resolveUserId(parsed);

    if (resolvedUserId && parsed.userId !== resolvedUserId) {
      const normalized = { ...parsed, userId: resolvedUserId };
      localStorage.setItem(STORAGE_KEY, JSON.stringify(normalized));
      return normalized;
    }

    return parsed;
  } catch {
    return null;
  }
}

function notifyUserInfoChange() {
  window.dispatchEvent(new Event(AUTH_USER_CHANGE_EVENT));
}

export const AgentAppsAuth = {
  getUserInfo(): UserInfo | null {
    return getStored();
  },

  getAccessToken(): string {
    return getStored()?.token || "";
  },

  getRefreshToken(): string {
    return getStored()?.refreshToken || "";
  },

  isLoggedIn(): boolean {
    return Boolean(getStored()?.token);
  },

  clearUserInfo() {
    localStorage.clear();
    notifyUserInfoChange();
  },

  getAuthHeaders(): Record<string, string> {
    const userInfo = this.getUserInfo();
    const headers: Record<string, string> = {};

    if (userInfo?.token) {
      headers.authorization = `Bearer ${userInfo.token}`;
    }

    if (userInfo?.userId) {
      headers["X-User-Id"] = userInfo.userId;
    }

    const tenantId =
      userInfo?.tenantId ||
      userInfo?.tenant_id ||
      userInfo?.tenantKey ||
      userInfo?.tenant_key;
    if (tenantId) {
      headers["X-Tenant-ID"] = tenantId;
    }

    return headers;
  },

  getLoginUrl(): string {
    return `${window.location.origin}${window.BASENAME || ""}/login`;
  },

  async logout(redirectUrl?: string) {
    try {
      const { logoutFromServer } = await import("@/modules/signin/utils/request");
      await logoutFromServer();
    } catch (error) {
      console.error("Logout from server failed:", error);
    }
    
    this.clearUserInfo();
    const target = redirectUrl || this.getLoginUrl();
    window.location.href = target;
  },

  setUserInfo(info: UserInfo) {
    const normalized = {
      ...info,
      userId: resolveUserId(info),
    };
    localStorage.setItem(STORAGE_KEY, JSON.stringify(normalized));
    notifyUserInfoChange();
  },

  updateUserInfo(patch: Partial<UserInfo>) {
    const current = getStored();
    if (!current) return;
    this.setUserInfo({
      ...current,
      ...patch,
    });
  },

  async refreshAccessToken(): Promise<string> {
    const refreshToken = this.getRefreshToken();
    
    if (!refreshToken) {
      throw new Error("No refresh token available");
    }

    const refreshUrl = authServiceApiUrl("auth/refresh");
    
    const refreshAxios = axios.create({
      timeout: 10000,
      headers: { "Content-Type": "application/json" },
    });
    
    const response = await refreshAxios.post(
      refreshUrl,
      { refresh_token: refreshToken }
    );

    const responseData = response.data;
    const loginData = responseData.data || responseData;
    
    if (!loginData.access_token) {
      throw new Error("刷新失败，未获取到新的 access_token");
    }

    this.updateUserInfo({
      token: loginData.access_token,
      refreshToken: loginData.refresh_token,
      timestamp: Date.now(),
    });

    return loginData.access_token;
  },
};
