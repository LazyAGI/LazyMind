import {
  assistantBridgeFetch,
  syncLocalAssistantSession,
  type LocalAssistantSession,
} from "./assistantSession";

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

export interface DesktopLocalFolderRecommendation {
  key: string;
  value: string;
  path: string;
  title: string;
  productId?: string;
  source?: "desktop_discovery";
}

export interface DesktopLocalFolderAccessState {
  available?: boolean;
  canceled?: boolean;
  discoveryConsentGranted: boolean;
  discoveryRoots: string[];
  allowedRoots: string[];
  items?: DesktopLocalFolderRecommendation[];
  scannedEntries?: number;
  truncated?: boolean;
  stoppedReason?: string;
  durationMs?: number;
}

export interface DesktopLocalFolderAuthorizationResult
  extends DesktopLocalFolderAccessState {
  granted: boolean;
  addedRoots: string[];
}

export type DesktopAgent = "codex" | "cursor" | "workbuddy" | "raccoon" | "traework" | "deepseek-harness";

export type DesktopAgentIntegrationState =
  | "requirements_missing"
  | "ready"
  | "action_required"
  | "enabled"
  | "conflict"
  | "error";

export interface DesktopAgentRequirement {
  id: string;
  description: string;
  satisfied: boolean;
}

export interface DesktopAgentAction {
  kind: "open_url" | "login";
  url?: string;
}

export interface DesktopAgentIntegrationStatus {
  agent: DesktopAgent;
  display_name: string;
  version?: string;
  state: DesktopAgentIntegrationState;
  requirements?: DesktopAgentRequirement[];
  action?: DesktopAgentAction;
  message?: string;
}

export type DesktopAgentIntegrationAction = "connect" | "disconnect" | "login";

export type DesktopExecutorProvider = "codex" | "cursor" | "workbuddy";
export type DesktopExecutorPolicyAction = "enable" | "disable";
export type DesktopAgentBindingTarget =
  | "codex-cli"
  | "codex-desktop"
  | "cursor-cli"
  | "codebuddy-cli"
  | "cursor-desktop"
  | "workbuddy-desktop"
  | "raccoon-desktop"
  | "traework-desktop";

export interface DesktopExecutorPolicy {
  provider: DesktopExecutorProvider;
  enabled: boolean;
  installed?: boolean;
  ready?: boolean;
  unavailable_reason?: string;
  bridge_state?: "ready" | "authentication_required" | "unavailable";
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

export type DesktopExecutorPoliciesResult =
  | { ok: true; data: Partial<Record<DesktopExecutorProvider, DesktopExecutorPolicy>> }
  | { ok: false; reason: DesktopBridgeUnavailableReason; error?: unknown };

export type DesktopExecutorPolicyResult =
  | { ok: true; data: DesktopExecutorPolicy }
  | { ok: false; reason: DesktopBridgeUnavailableReason; error?: unknown };

export interface DesktopAgentExecutableBinding {
  target: DesktopAgentBindingTarget;
  configured: boolean;
  path: string;
}

export type DesktopAgentExecutableBindingsResult =
  | { ok: true; data: Partial<Record<DesktopAgentBindingTarget, string>> }
  | { ok: false; reason: DesktopBridgeUnavailableReason; error?: unknown };

export type DesktopAgentExecutableBindingResult =
  | { ok: true; data: DesktopAgentExecutableBinding }
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
  agentIntegrationStatuses?: () => Promise<unknown> | unknown;
  agentIntegrationAction?: (agent: DesktopAgent, action: DesktopAgentIntegrationAction) => Promise<unknown> | unknown;
  executorIntegrationPolicies?: () => Promise<unknown> | unknown;
  executorIntegrationAction?: (provider: DesktopExecutorProvider, action: DesktopExecutorPolicyAction) => Promise<unknown> | unknown;
  agentExecutableBindings?: () => Promise<unknown> | unknown;
  agentExecutableBind?: (target: DesktopAgentBindingTarget, path: string) => Promise<unknown> | unknown;
  agentExecutableClear?: (target: DesktopAgentBindingTarget) => Promise<unknown> | unknown;
  assistantSessionSet?: (session: LocalAssistantSession) => Promise<unknown> | unknown;
  assistantSessionClear?: () => Promise<unknown> | unknown;
  restartRuntime?: () => Promise<unknown> | unknown;
  resetRuntime?: (scope?: "kb" | "all") => Promise<unknown> | unknown;
  localFolderAccessStatus?: () => Promise<DesktopLocalFolderAccessState> | DesktopLocalFolderAccessState;
  chooseLocalDiscoveryRoots?: () => Promise<DesktopLocalFolderAccessState> | DesktopLocalFolderAccessState;
  discoverLocalFolders?: () => Promise<DesktopLocalFolderAccessState> | DesktopLocalFolderAccessState;
  authorizeLocalFolders?: (paths: string[]) => Promise<DesktopLocalFolderAuthorizationResult> | DesktopLocalFolderAuthorizationResult;
  selectFolder?: () => Promise<string | null> | string | null;
  selectExecutable?: (target?: DesktopAgentBindingTarget) => Promise<string | null> | string | null;
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

async function syncCurrentLocalAssistantSession() {
  const { AgentAppsAuth } = await import("@/components/auth");
  await syncLocalAssistantSession(
    AgentAppsAuth.getUserInfo(),
    window.location.origin,
    ACTION_TIMEOUT_MS,
  );
}

export async function agentIntegrationStatuses(): Promise<DesktopAgentIntegrationStatusesResult> {
  const bridge = getDesktopBridge();
  try {
    await syncCurrentLocalAssistantSession();
    if (bridge?.agentIntegrationStatuses) {
      const payload = await bridge.agentIntegrationStatuses() as {
        agents?: Partial<Record<DesktopAgent, DesktopAgentIntegrationStatus>>;
      };
      return { ok: true, data: payload?.agents || {} };
    }
    const response = await assistantBridgeFetch("/agents", undefined, STATUS_TIMEOUT_MS);
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

export async function agentIntegrationAction(agent: DesktopAgent, action: DesktopAgentIntegrationAction): Promise<DesktopAgentIntegrationResult> {
  const bridge = getDesktopBridge();
  try {
    await syncCurrentLocalAssistantSession();
    if (bridge?.agentIntegrationAction) {
      return callAgentIntegration((value) => value.agentIntegrationAction!(agent, action));
    }
    return callLocalAssistantBridge(
      `/agents/${encodeURIComponent(agent)}/${action}`,
      { method: "POST" },
      action === "login" ? LOGIN_TIMEOUT_MS : ACTION_TIMEOUT_MS,
    );
  } catch (error) {
    return { ok: false, reason: "unavailable", error };
  }
}

export async function executorIntegrationPolicies(): Promise<DesktopExecutorPoliciesResult> {
  const bridge = getDesktopBridge();
  try {
    if (bridge?.executorIntegrationPolicies) {
      const payload = await bridge.executorIntegrationPolicies() as {
        executors?: Partial<Record<DesktopExecutorProvider, DesktopExecutorPolicy>>;
      };
      return { ok: true, data: payload.executors || {} };
    }
    const response = await assistantBridgeFetch("/executors", undefined, ACTION_TIMEOUT_MS);
    const payload = await response.json().catch(() => ({})) as {
      executors?: Partial<Record<DesktopExecutorProvider, DesktopExecutorPolicy>>;
      error?: string;
    };
    if (!response.ok) throw new Error(payload.error || `Assistant Bridge returned HTTP ${response.status}`);
    return { ok: true, data: payload.executors || {} };
  } catch (error) {
    return { ok: false, reason: "unavailable", error };
  }
}

export async function executorIntegrationAction(
  provider: DesktopExecutorProvider,
  action: DesktopExecutorPolicyAction,
): Promise<DesktopExecutorPolicyResult> {
  const bridge = getDesktopBridge();
  try {
    await syncCurrentLocalAssistantSession();
    let payload: unknown;
    if (bridge?.executorIntegrationAction) {
      payload = await bridge.executorIntegrationAction(provider, action);
    } else {
      const response = await assistantBridgeFetch(
        `/executors/${encodeURIComponent(provider)}/${action}`,
        { method: "POST" },
        ACTION_TIMEOUT_MS,
      );
      payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        const error = (payload as { error?: string }).error;
        throw new Error(error || `Assistant Bridge returned HTTP ${response.status}`);
      }
    }
    return { ok: true, data: payload as DesktopExecutorPolicy };
  } catch (error) {
    return { ok: false, reason: "unavailable", error };
  }
}

export async function agentExecutableBindings(): Promise<DesktopAgentExecutableBindingsResult> {
  const bridge = getDesktopBridge();
  try {
    let payload: unknown;
    if (bridge?.agentExecutableBindings) {
      payload = await bridge.agentExecutableBindings();
    } else {
      const response = await assistantBridgeFetch("/bindings", undefined, ACTION_TIMEOUT_MS);
      payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(`Assistant Bridge returned HTTP ${response.status}`);
    }
    const bindings = (payload as {
      bindings?: Partial<Record<DesktopAgentBindingTarget, string>>;
    }).bindings;
    return { ok: true, data: bindings || {} };
  } catch (error) {
    return { ok: false, reason: "unavailable", error };
  }
}

export async function bindAgentExecutable(
  target: DesktopAgentBindingTarget,
  path: string,
): Promise<DesktopAgentExecutableBindingResult> {
  return changeAgentExecutable(target, path);
}

export async function clearAgentExecutable(
  target: DesktopAgentBindingTarget,
): Promise<DesktopAgentExecutableBindingResult> {
  return changeAgentExecutable(target);
}

async function changeAgentExecutable(
  target: DesktopAgentBindingTarget,
  path?: string,
): Promise<DesktopAgentExecutableBindingResult> {
  const bridge = getDesktopBridge();
  try {
    let payload: unknown;
    if (path !== undefined && bridge?.agentExecutableBind) {
      payload = await bridge.agentExecutableBind(target, path);
    } else if (path === undefined && bridge?.agentExecutableClear) {
      payload = await bridge.agentExecutableClear(target);
    } else {
      const response = await assistantBridgeFetch(
        `/bindings/${encodeURIComponent(target)}`,
        path === undefined
          ? { method: "DELETE" }
          : {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ path }),
          },
        BINDING_TIMEOUT_MS,
      );
      payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error((payload as { error?: string }).error || `Assistant Bridge returned HTTP ${response.status}`);
    }
    return { ok: true, data: payload as DesktopAgentExecutableBinding };
  } catch (error) {
    return { ok: false, reason: "failed", error };
  }
}

const STATUS_TIMEOUT_MS = 10_000;
const ACTION_TIMEOUT_MS = 15_000;
const BINDING_TIMEOUT_MS = 30_000;
const LOGIN_TIMEOUT_MS = 125_000;

async function callLocalAssistantBridge(
  path: string,
  init?: RequestInit,
  timeoutMs = ACTION_TIMEOUT_MS,
): Promise<DesktopAgentIntegrationResult> {
  try {
    const response = await assistantBridgeFetch(path, init, timeoutMs);
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

export function localFolderAccessStatus(): Promise<DesktopLocalFolderAccessState | null> {
  const bridge = getDesktopBridge();
  if (!bridge?.localFolderAccessStatus) {
    return Promise.resolve(null);
  }
  return Promise.resolve(bridge.localFolderAccessStatus());
}

export function chooseLocalDiscoveryRoots(): Promise<DesktopLocalFolderAccessState | null> {
  const bridge = getDesktopBridge();
  if (!bridge?.chooseLocalDiscoveryRoots) {
    return Promise.resolve(null);
  }
  return Promise.resolve(bridge.chooseLocalDiscoveryRoots());
}

export function discoverLocalFolders(): Promise<DesktopLocalFolderAccessState | null> {
  const bridge = getDesktopBridge();
  if (!bridge?.discoverLocalFolders) {
    return Promise.resolve(null);
  }
  return Promise.resolve(bridge.discoverLocalFolders());
}

export function authorizeLocalFolders(
  paths: string[],
): Promise<DesktopLocalFolderAuthorizationResult | null> {
  const bridge = getDesktopBridge();
  if (!bridge?.authorizeLocalFolders) {
    return Promise.resolve(null);
  }
  return Promise.resolve(bridge.authorizeLocalFolders(paths));
}

export function selectExecutable(target?: DesktopAgentBindingTarget): Promise<string | null> {
  const bridge = getDesktopBridge();
  if (!bridge?.selectExecutable) {
    return Promise.resolve(null);
  }
  return Promise.resolve(bridge.selectExecutable(target));
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
