import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";
import { isDisconnectPending, requiresAgentQuit } from "./agentActions";

const connectionIssueCodes = new Set([
  "configuration_unreadable", "environment_mismatch", "workbuddy_login_required", "workbuddy_auth_unreadable",
  "process_inspection_failed", "cdp_unavailable", "control_port_conflict", "control_window_unavailable", "launch_failed",
  "startup_timeout", "runtime_unavailable", "lazymind_auth_required", "lazymind_unavailable",
  "configuration_failed", "operation_busy", "operation_cancelled", "startup_unavailable", "pairing_required",
  "pairing_disabled", "pairing_account_mismatch", "pairing_credential_mismatch", "pairing_invalid",
  "pairing_unreadable", "mcp_trust_required", "mcp_enable_required", "unsupported_version", "dependency_environment",
  "operation_failed", "version_unreadable", "capability_unavailable", "host_unavailable", "installation_unavailable",
  "plugin_update_required", "mcp_unavailable", // workflow-naming: dsh-external
]);

export function agentConnectionReady(status?: DesktopAgentIntegrationStatus): boolean {
  const connection = status?.connection;
  // A saved configuration or enabled switch is not evidence of a live connection.
  return status?.state === "enabled" && status.enabled !== false && status.retry_action !== "disconnect"
    && !status.issue && !requiresAgentQuit(status) && connection?.mcp_configured === true
    && connection.mcp_enabled === true && connection.mcp_trusted === true
    && connection.control_ready === true && connection.runtime_state === "connected";
}

export function agentConnectionStatusKey(status: DesktopAgentIntegrationStatus | undefined, enabled: boolean): string {
  if (requiresAgentQuit(status)) return "agentIntegration.quitRequired.waiting";
  if (isDisconnectPending(status)) return "agentIntegration.quitRequired.pendingDisconnect";
  if (status?.issue) {
    const code = status.issue.code;
    return `agentIntegration.connectionIssues.${connectionIssueCodes.has(code) ? code : "operation_failed"}`;
  }
  if (status?.state === "conflict") return "agentIntegration.connectionIssues.environment_mismatch";
  if (status?.state === "error") return status.retry_action === "disconnect"
    ? "agentIntegration.setupFeedback.disableFailed" : "agentIntegration.connectionIssues.operation_failed";
  if (status?.state === "action_required") return "agentIntegration.awaitingConfirmation";
  if (enabled) return agentConnectionReady(status) ? "agentIntegration.connected" : "agentIntegration.enabledPendingConnection";
  return "agentIntegration.notEnabled";
}
