import { axiosInstance, BASE_URL } from "@/components/request";
import {
  authorizeLocalWorkspace as authorizeDesktopWorkspace,
  reauthorizeLocalWorkspace as reauthorizeDesktopWorkspace,
  selectLocalWorkspace as selectDesktopWorkspace,
} from "@/runtime/desktopBridge";

export type WorkspacePermissionMode = "always_ask" | "ask_as_needed" | "allow_all";
export interface WorkspaceSelectionCandidate {
  canceled: boolean;
  selection_token?: string;
  display_name?: string;
  path?: string;
  expires_in_seconds?: number;
}
export interface LocalWorkspaceView {
  workspace_id: string;
  display_name: string;
  path: string;
  status: "active" | "revoked" | "path_unavailable";
  version: number;
  source: "local" | "desktop";
  affected_task_count?: number;
  permission_mode?: WorkspacePermissionMode;
  permission_version?: number;
}

const coreBase = `${BASE_URL}/api/core`;
const data = <T>(value: unknown): T => ((value as { data?: T })?.data ?? value) as T;

export async function selectWorkspaceCandidate(runtime: "local" | "desktop"): Promise<WorkspaceSelectionCandidate> {
  if (runtime === "desktop") return (await selectDesktopWorkspace()) ?? { canceled: true };
  return data<WorkspaceSelectionCandidate>((await axiosInstance.post("/_local/workspaces:select", {}, { timeout: 0 })).data);
}
export async function prepareWorkspaceReauthorization(runtime: "local" | "desktop", workspaceId: string): Promise<WorkspaceSelectionCandidate> {
  if (runtime === "desktop") return (await reauthorizeDesktopWorkspace(workspaceId)) ?? { canceled: true };
  return data<WorkspaceSelectionCandidate>((await axiosInstance.post("/_local/workspaces:reauthorize", { workspace_id: workspaceId }, { timeout: 0 })).data);
}
export async function authorizeWorkspace(runtime: "local" | "desktop", token: string): Promise<LocalWorkspaceView> {
  if (runtime === "desktop") {
    const result = await authorizeDesktopWorkspace(token);
    if (!result) throw new Error("workspace authorization unavailable");
    return result as LocalWorkspaceView;
  }
  return data<LocalWorkspaceView>((await axiosInstance.post("/_local/workspaces:authorize", { selection_token: token })).data);
}
export async function listWorkspaces(options: { query?: string; includeInactive?: boolean } = {}): Promise<LocalWorkspaceView[]> {
  const params = {
    ...(options.query?.trim() ? { query: options.query.trim() } : {}),
    ...(options.includeInactive ? { include_inactive: true } : {}),
  };
  const result = data<{ items?: LocalWorkspaceView[] }>((await axiosInstance.get(`${coreBase}/local-workspaces`, { params })).data);
  return result.items ?? [];
}
export async function getConversationWorkspace(conversationId: string): Promise<LocalWorkspaceView | undefined> {
  const result = data<{ status?: string; workspace?: LocalWorkspaceView; permission_mode?: WorkspacePermissionMode; permission_version?: number }>(
    (await axiosInstance.get(`${coreBase}/conversations/${encodeURIComponent(conversationId)}:workspace`)).data,
  );
  return result.workspace ? { ...result.workspace, permission_mode: result.permission_mode, permission_version: result.permission_version } : undefined;
}
export async function updateWorkspacePermission(conversationId: string, mode: WorkspacePermissionMode, version: number) {
  return data<{ permission_mode: WorkspacePermissionMode; permission_version: number; effective_at: "next_request" }>(
    (await axiosInstance.put(`${coreBase}/conversations/${encodeURIComponent(conversationId)}:workspace-permission`, { permission_mode: mode, version })).data,
  );
}
export function workspaceReason(error: unknown): string {
  const value = error as { response?: { data?: { reason?: string; detail?: { reason?: string }; data?: { detail?: { reason?: string } } } }; code?: string };
  return value.response?.data?.data?.detail?.reason ?? value.response?.data?.detail?.reason ?? value.response?.data?.reason ?? value.code ?? "unknown";
}


export async function revokeWorkspace(workspaceId: string, version: number) {
  return data<{ workspace_id: string; status: "revoked"; version: number; affected_task_count: number; stop_requested: boolean; stop_failed_count: number }>(
    (await axiosInstance.post(`${coreBase}/local-workspaces/${encodeURIComponent(workspaceId)}:revoke`, { version })).data,
  );
}
