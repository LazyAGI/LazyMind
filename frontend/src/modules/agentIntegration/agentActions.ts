import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";

export function requiresAgentQuit(status?: DesktopAgentIntegrationStatus): boolean {
  return status?.action?.kind === "quit_agent" || status?.issue?.code === "quit_required";
}
