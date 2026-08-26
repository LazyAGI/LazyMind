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

export type DesktopAgentIntegrationStatusesResult =
  | { ok: true; data: Partial<Record<DesktopAgent, DesktopAgentIntegrationStatus>> }
  | { ok: false; reason: DesktopBridgeUnavailableReason; error?: unknown };

export interface DesktopArtifactFilePayload {
  source: string;
  filename?: string;
  data?: ArrayBuffer;
}

export type DesktopFileActionResult =
  | { ok: true; path?: string; canceled?: false }
  | { ok: true; canceled: true }
  | { ok: false; reason: DesktopBridgeUnavailableReason; error?: unknown };

type DesktopBridgeCommand =
  | "openLogsDir"
  | "openDataDir"
  | "restartRuntime";

interface LazyMindDesktopBridge {
  platform?: string;
  openLogsDir?: () => Promise<void> | void;
  openDataDir?: () => Promise<void> | void;
  runtimeStatus?: () => Promise<unknown> | unknown;
  agentIntegrationStatus?: (agent: DesktopAgent) => Promise<unknown> | unknown;
  agentIntegrationAction?: (agent: DesktopAgent, action: "connect" | "disconnect") => Promise<unknown> | unknown;
  codexIntegrationAction?: (action: "connect" | "disconnect") => Promise<unknown> | unknown;
  restartRuntime?: () => Promise<unknown> | unknown;
  resetRuntime?: (scope?: "kb" | "all") => Promise<unknown> | unknown;
  selectFolder?: () => Promise<string | null> | string | null;
  selectExecutable?: () => Promise<string | null> | string | null;
  exportDiagnostics?: () => Promise<string> | string;
  showItemInFolder?: (
    payload: DesktopArtifactFilePayload | string,
  ) => Promise<unknown> | unknown;
  saveFileAs?: (
    payload: DesktopArtifactFilePayload,
  ) => Promise<unknown> | unknown;
  downloadFile?: (
    payload: DesktopArtifactFilePayload,
  ) => Promise<unknown> | unknown;
}

function getDesktopBridge(): LazyMindDesktopBridge | undefined {
  if (typeof window === "undefined") {
    return undefined;
  }

  return (window as Window & { lazymindDesktop?: LazyMindDesktopBridge })
    .lazymindDesktop;
}

export function hasDesktopFileBridge(): boolean {
  const bridge = getDesktopBridge();
  return Boolean(bridge?.showItemInFolder && bridge?.saveFileAs && bridge?.downloadFile);
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
	const bridge = getDesktopBridge();
	if (bridge?.agentIntegrationStatus) {
		return callAgentIntegration((value) => value.agentIntegrationStatus!(agent));
	}
	return callLocalAssistantBridge(`/agents/${encodeURIComponent(agent)}`);
}

export async function agentIntegrationStatuses(agents: DesktopAgent[]): Promise<DesktopAgentIntegrationStatusesResult> {
  const bridge = getDesktopBridge();
  if (bridge?.agentIntegrationStatus) {
    const results = await Promise.all(agents.map(async (agent) => ({ agent, result: await agentIntegrationStatus(agent) })));
    const statuses: Partial<Record<DesktopAgent, DesktopAgentIntegrationStatus>> = {};
    for (const { agent, result } of results) {
      if (!result.ok) return result;
      statuses[agent] = result.data;
    }
    return { ok: true, data: statuses };
  }
  try {
    const response = await fetch(`${LOCAL_ASSISTANT_BRIDGE}/agents`);
    const payload = await response.json().catch(() => ({})) as {
      agents?: Partial<Record<DesktopAgent, DesktopAgentIntegrationStatus>>;
      error?: string;
    };
    if (!response.ok) throw new Error(payload.error || `Assistant Bridge returned HTTP ${response.status}`);
    return { ok: true, data: payload.agents || {} };
  } catch (error) {
    return { ok: false, reason: "unavailable", error };
  }
}

export function agentIntegrationAction(agent: DesktopAgent, action: "connect" | "disconnect"): Promise<DesktopAgentIntegrationResult> {
	const bridge = getDesktopBridge();
	if (bridge?.agentIntegrationAction) {
		return callAgentIntegration((value) => value.agentIntegrationAction!(agent, action));
	}
	if (agent === "codex" && bridge?.codexIntegrationAction) {
		return callAgentIntegration((value) => value.codexIntegrationAction!(action));
	}
	return callLocalAssistantBridge(`/agents/${encodeURIComponent(agent)}/${action}`, { method: "POST" });
}

export function codexIntegrationAction(action: "connect" | "disconnect"): Promise<DesktopAgentIntegrationResult> {
	return agentIntegrationAction("codex", action);
}

const LOCAL_ASSISTANT_BRIDGE = "http://127.0.0.1:19091/v1";

async function callLocalAssistantBridge(path: string, init?: RequestInit): Promise<DesktopAgentIntegrationResult> {
	try {
		const response = await fetch(`${LOCAL_ASSISTANT_BRIDGE}${path}`, init);
		const payload = await response.json().catch(() => ({})) as DesktopAgentIntegrationStatus & { error?: string };
		if (!response.ok) throw new Error(payload.error || `Assistant Bridge returned HTTP ${response.status}`);
		return { ok: true, data: payload };
	} catch (error) {
		return { ok: false, reason: "unavailable", error };
	}
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

export function getDesktopPlatform(): string | undefined {
  return getDesktopBridge()?.platform;
}

async function callDesktopFileAction(
  method: "showItemInFolder" | "saveFileAs" | "downloadFile",
  payload: DesktopArtifactFilePayload,
): Promise<DesktopFileActionResult> {
  const bridge = getDesktopBridge();
  const handler = bridge?.[method];
  if (!handler) {
    return { ok: false, reason: "unavailable" };
  }
  try {
    const result = (await handler.call(bridge, payload)) as
      | { ok?: boolean; path?: string; canceled?: boolean }
      | undefined;
    if (result?.canceled) {
      return { ok: true, canceled: true };
    }
    return { ok: true, path: result?.path };
  } catch (error) {
    return { ok: false, reason: "failed", error };
  }
}

export function showItemInFolder(
  payload: DesktopArtifactFilePayload,
): Promise<DesktopFileActionResult> {
  return callDesktopFileAction("showItemInFolder", payload);
}

export function saveFileAs(
  payload: DesktopArtifactFilePayload,
): Promise<DesktopFileActionResult> {
  return callDesktopFileAction("saveFileAs", payload);
}

export function downloadDesktopFile(
  payload: DesktopArtifactFilePayload,
): Promise<DesktopFileActionResult> {
  return callDesktopFileAction("downloadFile", payload);
}

