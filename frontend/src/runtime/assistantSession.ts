import { isDesktopRuntime } from "./mode";

export const LOCAL_ASSISTANT_BRIDGE = "http://127.0.0.1:19091/v1";

interface SessionUser {
  token: string;
  refreshToken?: string;
  username?: string;
  role?: string;
  tenantId?: string;
  tenant_id?: string;
}

export async function assistantBridgeFetch(
  path: string,
  init: RequestInit | undefined,
  timeoutMs: number,
): Promise<Response> {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(`${LOCAL_ASSISTANT_BRIDGE}${path}`, { ...init, signal: controller.signal });
  } finally {
    window.clearTimeout(timeout);
  }
}

export async function syncLocalAssistantSession(
  user: SessionUser | null,
  serverURL: string,
  timeoutMs: number,
): Promise<void> {
  if (!user?.token || !user.refreshToken) {
    await clearLocalAssistantSession(timeoutMs);
    return;
  }
  const response = await assistantBridgeFetch(
    "/session",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        server_url: serverURL,
        username: user.username,
        access_token: user.token,
        refresh_token: user.refreshToken,
        role: user.role,
        tenant_id: user.tenantId || user.tenant_id,
      }),
    },
    timeoutMs,
  );
  if (!response.ok) throw new Error(`Assistant Bridge returned HTTP ${response.status}`);
}

export async function clearLocalAssistantSession(timeoutMs = 15_000): Promise<void> {
  if (isDesktopRuntime() || typeof window === "undefined") return;
  const response = await assistantBridgeFetch("/session", { method: "DELETE" }, timeoutMs);
  if (!response.ok) throw new Error(`Assistant Bridge returned HTTP ${response.status}`);
}
