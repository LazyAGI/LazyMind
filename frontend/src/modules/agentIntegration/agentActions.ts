import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";

export function requiresAgentQuit(status?: DesktopAgentIntegrationStatus): boolean {
  return status?.action?.kind === "quit_agent" || status?.issue?.code === "quit_required";
}

export function isDisconnectPending(status?: DesktopAgentIntegrationStatus): boolean {
  return status?.state === "action_required" && status.retry_action === "disconnect" && status.issue?.code === "disconnect_pending";
}
