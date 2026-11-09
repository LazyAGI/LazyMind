import type { TFunction } from "i18next";
import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";

export function dshConnectionReady(status?: DesktopAgentIntegrationStatus): boolean {
  const connection = status?.connection;
  return status?.state === "enabled" && status.enabled !== false && status.retry_action !== "disconnect"
    && !status.issue && connection?.mcp_configured === true
    && connection.mcp_enabled === true && connection.mcp_trusted === true
    && connection.control_ready === true && connection.runtime_state === "connected";
}

// Errors come from known status codes. Native output may contain local paths or
// credentials and must never be presented as setup instructions.
export function dshErrorMessage(status: DesktopAgentIntegrationStatus | undefined, t: TFunction): string {
  const code = status?.issue?.code || (status?.state === "conflict" ? "environment_mismatch" : undefined);
  const known = ["unsupported_version", "dependency_environment",
    "plugin_update_required", // workflow-naming: dsh-external
    "operation_failed", "process_inspection_failed",
    "configuration_unreadable", "environment_mismatch", "pairing_required", "pairing_disabled", "pairing_account_mismatch",
    "pairing_credential_mismatch", "pairing_invalid", "pairing_unreadable", "mcp_enable_required", "host_unavailable",
    "capability_unavailable", "installation_unavailable", "lazymind_auth_required", "lazymind_unavailable", "mcp_unavailable"];
  return t(`agentIntegration.dshErrors.${code && known.includes(code) ? code : "operation_failed"}`);
}
