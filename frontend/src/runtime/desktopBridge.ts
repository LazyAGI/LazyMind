import { isDesktopRuntime } from "./mode";

export type DesktopBridgeUnavailableReason = "unavailable" | "failed";

export type DesktopBridgeResult =
  | { ok: true }
  | { ok: false; reason: DesktopBridgeUnavailableReason; error?: unknown };

export interface DesktopAdminSession {
  token: string;
  refreshToken?: string;
  username: string;
  userId?: string;
  role?: string;
  email?: string;
  displayName?: string;
  tenantId?: string;
  dynamic?: boolean;
  chatUnlikeSwitch?: boolean;
  timestamp?: number;
}

type DesktopBridgeCommand =
  | "openLogsDir"
  | "openDataDir"
  | "runtimeStatus"
  | "restartRuntime";

interface LazyMindDesktopBridge {
  openLogsDir?: () => Promise<void> | void;
  openDataDir?: () => Promise<void> | void;
  runtimeStatus?: () => Promise<unknown> | unknown;
  restartRuntime?: () => Promise<unknown> | unknown;
  resetRuntime?: (scope?: "kb" | "all") => Promise<unknown> | unknown;
  selectFolder?: () => Promise<string | null> | string | null;
  exportDiagnostics?: () => Promise<string> | string;
  desktopAdminSession?: () => Promise<DesktopAdminSession> | DesktopAdminSession;
}

function getDesktopBridge(): LazyMindDesktopBridge | undefined {
  if (!isDesktopRuntime() || typeof window === "undefined") {
    return undefined;
  }

  return (window as Window & { lazymindDesktop?: LazyMindDesktopBridge })
    .lazymindDesktop;
}

async function callDesktopBridge(
  method: DesktopBridgeCommand,
): Promise<DesktopBridgeResult> {
  const bridge = getDesktopBridge();
  const handler = bridge?.[method];

  if (!handler) {
    return { ok: false, reason: "unavailable" };
  }

  try {
    await handler.call(bridge);
    return { ok: true };
  } catch (error) {
    return { ok: false, reason: "failed", error };
  }
}

export function openLogsDir(): Promise<DesktopBridgeResult> {
  return callDesktopBridge("openLogsDir");
}

export function openDataDir(): Promise<DesktopBridgeResult> {
  return callDesktopBridge("openDataDir");
}

export function runtimeStatus(): Promise<DesktopBridgeResult> {
  return callDesktopBridge("runtimeStatus");
}

export function restartRuntime(): Promise<DesktopBridgeResult> {
  return callDesktopBridge("restartRuntime");
}

export function resetRuntime(scope?: "kb" | "all"): Promise<DesktopBridgeResult> {
  const bridge = getDesktopBridge();
  if (!bridge?.resetRuntime) {
    return Promise.resolve({ ok: false, reason: "unavailable" });
  }
  return Promise.resolve()
    .then(() => bridge.resetRuntime?.(scope))
    .then(() => ({ ok: true as const }))
    .catch((error) => ({ ok: false as const, reason: "failed" as const, error }));
}

export function selectFolder(): Promise<string | null> {
  const bridge = getDesktopBridge();
  if (!bridge?.selectFolder) {
    return Promise.resolve(null);
  }
  return Promise.resolve(bridge.selectFolder());
}

export function exportDiagnostics(): Promise<string | null> {
  const bridge = getDesktopBridge();
  if (!bridge?.exportDiagnostics) {
    return Promise.resolve(null);
  }
  return Promise.resolve(bridge.exportDiagnostics());
}

export function requestDesktopAdminSession(): Promise<DesktopAdminSession> {
  const bridge = getDesktopBridge();
  if (!bridge?.desktopAdminSession) {
    return Promise.reject(new Error("Desktop admin session bridge is unavailable"));
  }
  return Promise.resolve(bridge.desktopAdminSession());
}
