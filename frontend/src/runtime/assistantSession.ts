export const LOCAL_ASSISTANT_BRIDGE = `${(import.meta.env.VITE_LAZYMIND_ASSISTANT_BRIDGE_URL || "http://127.0.0.1:19091").replace(/\/$/, "")}/v1`;
let discoveredBridge: string | undefined;
let bridgeDiscovery: Promise<string> | undefined;
const CLIENT_PLATFORM_HEADER = "X-LazyMind-Client-Platform";
export const ASSISTANT_BRIDGE_PLATFORM_MISMATCH = "platform_mismatch";

export class AssistantBridgeRequestError extends Error {
  constructor(
    message: string,
    public readonly code = "",
    public readonly clientPlatform = "",
    public readonly bridgePlatform = "",
  ) {
    super(message);
    this.name = "AssistantBridgeRequestError";
  }
}

export function isAssistantBridgePlatformMismatch(error: unknown): boolean {
  return error instanceof AssistantBridgeRequestError && error.code === ASSISTANT_BRIDGE_PLATFORM_MISMATCH;
}

export function browserClientPlatform(): "windows" | "darwin" | "linux" | "" {
  if (typeof navigator === "undefined") return "";
  const userAgentData = (navigator as Navigator & {
    userAgentData?: { platform?: string };
  }).userAgentData;
  const value = `${userAgentData?.platform || navigator.platform || ""} ${navigator.userAgent || ""}`;
  if (/windows|win32|win64/i.test(value)) return "windows";
  if (/macintosh|macintel|mac os/i.test(value)) return "darwin";
  if (/linux/i.test(value)) return "linux";
  return "";
}

// Discover before sending a mutation. A failed POST is never automatically replayed.
async function discoverAssistantBridge(): Promise<string> {
  if (import.meta.env.VITE_LAZYMIND_ASSISTANT_BRIDGE_URL) return LOCAL_ASSISTANT_BRIDGE;
  if (discoveredBridge) return discoveredBridge;
  if (bridgeDiscovery) return bridgeDiscovery;
  bridgeDiscovery = (async () => {
    const platform = browserClientPlatform();
    const candidates = Array.from({ length: 10 }, (_, index) => `http://127.0.0.1:${19091 + index}/v1`);
    const results = await Promise.all(candidates.map(async (base) => {
      const controller = new AbortController();
      const timeout = window.setTimeout(() => controller.abort(), 1200);
      try {
        const response = await fetch(`${base}/health`, { signal: controller.signal });
        if (!response.ok) return undefined;
        const health = await response.json() as { service?: string; version?: string; platform?: string; isolated?: boolean };
        if (health.service !== "lazymind-assistant-bridge" || health.version !== "v9" || health.isolated !== false) return undefined;
        if (platform && health.platform !== platform) return undefined;
        return base;
      } catch { return undefined; }
      finally { window.clearTimeout(timeout); }
    }));
    const base = results.find((value) => value !== undefined);
    if (!base) throw new AssistantBridgeRequestError("未找到兼容的本机助理桥接服务。请启动当前版本的 Assistant Bridge 后重试；已有服务不会被停止。", "bridge_unavailable");
    discoveredBridge = base;
    return base;
  })();
  try { return await bridgeDiscovery; }
  finally { bridgeDiscovery = undefined; }
}

interface SessionUser {
  token: string;
  refreshToken?: string;
  username?: string;
  role?: string;
  tenantId?: string;
  tenant_id?: string;
}

export interface LocalAssistantSession {
  server_url: string;
  username?: string;
  access_token: string;
  refresh_token: string;
  role?: string;
  tenant_id?: string;
}

export interface LocalAssistantSessionClear {
  expected_session: LocalAssistantSession;
  allow_renewed?: boolean;
}

interface DesktopSessionBridge {
  assistantSessionSet?: (session: LocalAssistantSession) => Promise<unknown> | unknown;
  assistantSessionClear?: (request: LocalAssistantSessionClear) => Promise<unknown> | unknown;
}

function getDesktopSessionBridge(): DesktopSessionBridge | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as Window & { lazymindDesktop?: DesktopSessionBridge }).lazymindDesktop;
}

export async function assistantBridgeFetch(
  path: string,
  init: RequestInit | undefined,
  timeoutMs: number,
): Promise<Response> {
  const bridge = await discoverAssistantBridge();
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    const headers = new Headers(init?.headers);
    const platform = browserClientPlatform();
    if (platform) headers.set(CLIENT_PLATFORM_HEADER, platform);
    return await fetch(`${bridge}${path}`, {
      ...init,
      headers,
      signal: controller.signal,
    });
  } catch (error) {
    discoveredBridge = undefined;
    throw error;
  } finally {
    window.clearTimeout(timeout);
  }
}

export async function assistantBridgeJSON<T>(
  path: string,
  init: RequestInit | undefined,
  timeoutMs: number,
): Promise<T> {
  const response = await assistantBridgeFetch(path, init, timeoutMs);
  const payload = await response.json().catch(() => ({})) as T & {
    code?: string;
    error?: string;
    client_platform?: string;
    bridge_platform?: string;
  };
  if (!response.ok) {
    throw new AssistantBridgeRequestError(
      payload.error || `Assistant Bridge returned HTTP ${response.status}`,
      payload.code,
      payload.client_platform,
      payload.bridge_platform,
    );
  }
  return payload;
}

export async function syncLocalAssistantSession(
  user: SessionUser | null,
  serverURL: string,
  timeoutMs: number,
): Promise<void> {
  if (!user?.token || !user.refreshToken) {
    // Missing browser state does not identify which connector login to clear.
    // Explicit logout supplies a matching snapshot through the clear operation.
    return;
  }
  const session: LocalAssistantSession = {
    server_url: serverURL,
    username: user.username,
    access_token: user.token,
    refresh_token: user.refreshToken,
    role: user.role,
    tenant_id: user.tenantId || user.tenant_id,
  };
  const desktopBridge = getDesktopSessionBridge();
  if (desktopBridge?.assistantSessionSet) {
    await desktopBridge.assistantSessionSet(session);
    return;
  }
  await assistantBridgeJSON(
    "/session",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(session),
    },
    timeoutMs,
  );
}

export async function clearLocalAssistantSession(
  timeoutMs = 15_000,
  expected?: LocalAssistantSession,
  allowRenewed = false,
): Promise<void> {
  if (typeof window === "undefined" || !expected) return;
  const request: LocalAssistantSessionClear = { expected_session: expected, allow_renewed: allowRenewed };
  const desktopBridge = getDesktopSessionBridge();
  if (desktopBridge?.assistantSessionClear) {
    await desktopBridge.assistantSessionClear(request);
    return;
  }
  await assistantBridgeJSON("/session", {
    method: "DELETE", headers: { "Content-Type": "application/json" }, body: JSON.stringify(request),
  }, timeoutMs);
}
