/**
 * Minimal auth for LazyMind: getUserInfo from storage, logout redirect to /login.
 * Compatible with AuthServiceApi login (token stored after username/password login).
 */
import axios from "axios";
import { authServiceApiUrl, coreApiUrl } from "@/runtime/apiBase";
import i18n from "@/i18n";
import { clearLocalAssistantSession } from "@/runtime/assistantSession";

const STORAGE_KEY = "lazymind:user";
export const AUTH_USER_CHANGE_EVENT = "lazymind:user-change";
export const AUTH_LOGOUT_EVENT = "lazymind:logout";

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
  sessionId?: string;
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

    // Refresh results never replace the active-session pointer. A late write is
    // confined to its original session even when another tab logs in concurrently.
    const refreshed = localStorage.getItem(refreshStorageKey(parsed));
    const entry = refreshed ? JSON.parse(refreshed) : null;
    const credentials = entry?.endpoint === authServiceApiUrl("auth/refresh")
      && matchesSession(entry.base, parsed) ? entry.credentials : {};
    return { ...parsed, ...credentials, userId: resolvedUserId };
  } catch {
    return null;
  }
}

function refreshStorageKey(info: UserInfo) {
  return `lazymind:refresh:${info.sessionId || info.token}`;
}

function tenantFor(info?: Partial<UserInfo> | null) {
  return info?.tenantId || info?.tenant_id || info?.tenantKey || info?.tenant_key || "";
}

function principalFor(info?: Partial<UserInfo> | null) {
  return JSON.stringify([resolveUserId(info) || "", tenantFor(info)]);
}

function identityFor(info: UserInfo | null) {
  return JSON.stringify([info?.sessionId || info?.token || "", resolveUserId(info) || "",
    tenantFor(info), authServiceApiUrl("auth/refresh")]);
}

function matchesSession(base: string | undefined, info: UserInfo) {
  try {
    return !!base && identityFor(JSON.parse(base)) === identityFor(info);
  } catch {
    return false;
  }
}

// Profile edits preserve the base session generation. Token rotation is stored
// separately so a late refresh cannot replace a newly logged-in account.
function sessionIdentity() {
  const raw = localStorage.getItem(STORAGE_KEY);
  let info: UserInfo | null = null;
  try { info = raw ? JSON.parse(raw) : null; } catch { /* Invalid storage is logged out. */ }
  return identityFor(info);
}
const refreshes = new Map<string, Promise<string>>();

function notifyUserInfoChange() {
  window.dispatchEvent(new Event(AUTH_USER_CHANGE_EVENT));
}

export const AgentAppsAuth = {
  getSessionIdentity: sessionIdentity,

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
    window.dispatchEvent(new Event(AUTH_LOGOUT_EVENT));
    const session = this.getUserInfo();
    const accessToken = session?.token;
    this.clearUserInfo();
    const clearedIdentity = sessionIdentity();
    // Begin clearing the old local session before any new login can occur.
    const localCleanup = clearLocalAssistantSession().catch(() => {});
    if (accessToken) {
      try {
        await fetch(coreApiUrl("browser/manage/devices"), {
          method: "DELETE",
          headers: { Authorization: `Bearer ${accessToken}` },
          credentials: "include",
        });
      } catch {
        // Browser pairing cleanup is best effort when the server is unavailable.
      }
    }
    try {
      const { logoutFromServer } = await import("@/modules/signin/utils/request");
      await logoutFromServer(session);
    } catch (error) {
      console.error("Logout from server failed:", error);
    }
    try {
      await localCleanup;
    } catch {
      // The local Assistant Bridge is optional outside local deployments.
    }
    
    if (sessionIdentity() !== clearedIdentity) return;
    const target = redirectUrl || this.getLoginUrl();
    window.location.href = target;
  },

  setUserInfo(info: UserInfo) {
    const normalized = {
      ...info,
      sessionId: Array.from(crypto.getRandomValues(new Uint8Array(16)), value => value.toString(16).padStart(2, "0")).join(""),
      userId: resolveUserId(info),
    };
    localStorage.setItem(STORAGE_KEY, JSON.stringify(normalized));
    notifyUserInfoChange();
  },

  replaceLocalSession(info: UserInfo) {
    const raw = localStorage.getItem(STORAGE_KEY);
    let base: UserInfo | null = null;
    try { base = raw ? JSON.parse(raw) : null; } catch { /* Replace invalid storage below. */ }
    if (!base || principalFor(base) !== principalFor(info)) {
      this.setUserInfo(info);
      return;
    }

    if (base.sessionId) {
      localStorage.removeItem(refreshStorageKey(base));
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        ...info,
        sessionId: base.sessionId,
        userId: resolveUserId(info),
      }));
    } else {
      // A legacy session uses its original token as the request generation. Keep
      // that base token stable and store renewed credentials in its overlay.
      const credentials: Partial<UserInfo> = { token: info.token };
      if (info.refreshToken !== undefined) credentials.refreshToken = info.refreshToken;
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        ...base,
        ...info,
        token: base.token,
        refreshToken: base.refreshToken,
        userId: resolveUserId(info),
      }));
      const nextBase = localStorage.getItem(STORAGE_KEY)!;
      localStorage.setItem(refreshStorageKey(base), JSON.stringify({
        base: nextBase,
        endpoint: authServiceApiUrl("auth/refresh"),
        credentials,
      }));
    }
    notifyUserInfoChange();
  },

  updateUserInfo(patch: Partial<UserInfo>) {
    const current = getStored();
    if (!current) return;
    const updated = { ...current, ...patch };
    if (identityFor(updated) !== identityFor(current)
      || updated.token !== current.token || updated.refreshToken !== current.refreshToken) {
      this.setUserInfo(updated);
      return;
    }
    // Do not copy rotated credentials into the base: legacy sessions use the
    // original token as their generation, and overlays must survive profile edits.
    const base = JSON.parse(localStorage.getItem(STORAGE_KEY)!);
    localStorage.setItem(STORAGE_KEY, JSON.stringify({
      ...base, ...patch, token: base.token, refreshToken: base.refreshToken, userId: resolveUserId(updated),
    }));
    notifyUserInfoChange();
  },

  refreshAccessToken(): Promise<string> {
    const identity = sessionIdentity();
    const pending = refreshes.get(identity);
    if (pending) return pending;
    const initial = getStored();
    const base = localStorage.getItem(STORAGE_KEY);
    const endpoint = authServiceApiUrl("auth/refresh");
    const alive = () => sessionIdentity() === identity;
    const stale = () => new Error("STALE_AUTH_SESSION");
    const run = async () => {
      if (!alive()) throw stale();
      const current = getStored();
      if (!current?.refreshToken || !initial) throw new Error("No refresh token available");
      // Another tab may have rotated this same session while we waited for its lock.
      if (current.token !== initial.token) return current.token;
      const refreshAxios = axios.create({ timeout: 10000, headers: { "Content-Type": "application/json" } });
      let response;
      try {
        response = await refreshAxios.post(endpoint, { refresh_token: current.refreshToken });
      } catch (error) {
        if (!alive()) throw stale();
        throw error;
      }
      if (!alive()) throw stale();
      // A same-account local recovery preserves the request generation but may
      // install newer credentials while this refresh is in flight.
      const latest = getStored();
      if (latest?.token !== initial.token) return latest?.token || "";
      const loginData = response.data.data || response.data;
      if (!loginData.access_token) throw new Error(i18n.t("errors.2000509"));
      const credentials = {
        token: loginData.access_token,
        refreshToken: loginData.refresh_token || current.refreshToken,
        timestamp: Date.now(),
      };
      // Legacy sessions keep their original base token as the storage generation.
      localStorage.setItem(refreshStorageKey(JSON.parse(base!)), JSON.stringify({ base, endpoint, credentials }));
      if (!alive()) throw stale();
      notifyUserInfoChange();
      return credentials.token;
    };
    // Web Locks serialize token rotation across tabs. On older browsers the
    // session-scoped commit still prevents cross-account credential contamination.
    const operation = (navigator.locks
      ? navigator.locks.request(`lazymind:auth-refresh:${initial?.sessionId || "legacy"}`, run)
      : run()).finally(() => { refreshes.delete(identity); });
    refreshes.set(identity, operation);
    return operation;
  },
};
