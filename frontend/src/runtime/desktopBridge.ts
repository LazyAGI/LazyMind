import { isDesktopRuntime } from "./mode";

export type DesktopBridgeUnavailableReason = "unavailable" | "failed";

export type DesktopBridgeResult =
  | { ok: true }
  | { ok: false; reason: DesktopBridgeUnavailableReason; error?: unknown };

export interface DesktopRuntimeServiceStatus {
  status?: string;
}

export interface DesktopRuntimeStatus {
  overallStatus?: string;
  services?: Record<string, DesktopRuntimeServiceStatus>;
}

export type DesktopAgent = "codex" | "cursor" | "workbuddy" | "traework" | "deepseek-harness";

export interface DesktopAgentSetupGuide {
  method: "config_file" | "cursor_install_url" | "trae_config_file" | "dsh_profile_patch";
  url?: string;
  config_path?: string;
  configuration?: string;
}

export interface DesktopAgentIntegrationStatus {
  agent: DesktopAgent;
  display_name?: string;
  mode?: "managed" | "manual";
  installed: boolean;
  version?: string;
  configured?: boolean;
  owned?: boolean;
  service_ready: boolean;
  ready: boolean;
  command?: string;
  arguments?: string[];
  endpoint?: string;
  tools?: string[];
  setup?: DesktopAgentSetupGuide;
  readiness_error?: string;
}

export type DesktopRuntimeStatusResult =
  | { ok: true; data: DesktopRuntimeStatus }
  | { ok: false; reason: DesktopBridgeUnavailableReason; error?: unknown };

export type DesktopAgentIntegrationResult =
  | { ok: true; data: DesktopAgentIntegrationStatus }
  | { ok: false; reason: DesktopBridgeUnavailableReason; error?: unknown };

type DesktopBridgeCommand =
  | "openLogsDir"
  | "openDataDir"
  | "restartRuntime";

interface LazyMindDesktopBridge {
  openLogsDir?: () => Promise<void> | void;
  openDataDir?: () => Promise<void> | void;
  runtimeStatus?: () => Promise<unknown> | unknown;
  agentIntegrationStatus?: (agent: DesktopAgent) => Promise<unknown> | unknown;
  codexIntegrationAction?: (action: "connect" | "disconnect") => Promise<unknown> | unknown;
  restartRuntime?: () => Promise<unknown> | unknown;
  resetRuntime?: (scope?: "kb" | "all") => Promise<unknown> | unknown;
  selectFolder?: () => Promise<string | null> | string | null;
  selectExecutable?: () => Promise<string | null> | string | null;
  exportDiagnostics?: () => Promise<string> | string;
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

export function runtimeStatus(): Promise<DesktopRuntimeStatusResult> {
  const bridge = getDesktopBridge();
  if (!bridge?.runtimeStatus) {
    return Promise.resolve({ ok: false, reason: "unavailable" });
  }

  return Promise.resolve()
    .then(() => bridge.runtimeStatus?.())
    .then((data) => ({
      ok: true as const,
      data: (data || {}) as DesktopRuntimeStatus,
    }))
    .catch((error) => ({
      ok: false as const,
      reason: "failed" as const,
      error,
    }));
}

async function callAgentIntegration(
  call: (bridge: LazyMindDesktopBridge) => Promise<unknown> | unknown,
): Promise<DesktopAgentIntegrationResult> {
  const bridge = getDesktopBridge();
  if (!bridge) {
    return { ok: false, reason: "unavailable" };
  }
  try {
    const data = await call(bridge);
    return { ok: true, data: data as DesktopAgentIntegrationStatus };
  } catch (error) {
    return { ok: false, reason: "failed", error };
  }
}

export function agentIntegrationStatus(agent: DesktopAgent): Promise<DesktopAgentIntegrationResult> {
  return callAgentIntegration((bridge) => {
    if (!bridge.agentIntegrationStatus) throw new Error("Agent integration is unavailable");
    return bridge.agentIntegrationStatus(agent);
  });
}

export function codexIntegrationAction(action: "connect" | "disconnect"): Promise<DesktopAgentIntegrationResult> {
  return callAgentIntegration((bridge) => {
    if (!bridge.codexIntegrationAction) throw new Error("Codex integration is unavailable");
    return bridge.codexIntegrationAction(action);
  });
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

export function selectExecutable(): Promise<string | null> {
  const bridge = getDesktopBridge();
  if (!bridge?.selectExecutable) {
    return Promise.resolve(null);
  }
  return Promise.resolve(bridge.selectExecutable());
}

export function exportDiagnostics(): Promise<string | null> {
  const bridge = getDesktopBridge();
  if (!bridge?.exportDiagnostics) {
    return Promise.resolve(null);
  }
  return Promise.resolve(bridge.exportDiagnostics());
}
