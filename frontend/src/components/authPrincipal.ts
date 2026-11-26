import type { UserInfo } from "./auth";

export const AUTH_USER_STORAGE_KEY = "lazymind:user";

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

export function resolveUserId(userInfo?: Partial<UserInfo> | null) {
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

export function tenantFor(info?: Partial<UserInfo> | null) {
  return info?.tenantId || info?.tenant_id || info?.tenantKey || info?.tenant_key || "";
}

export function principalFor(info?: Partial<UserInfo> | null) {
  return JSON.stringify([resolveUserId(info) || "", tenantFor(info)]);
}
