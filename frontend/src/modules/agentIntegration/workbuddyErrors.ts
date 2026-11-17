import type { TFunction } from "i18next";
import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";

export function workbuddyErrorMessage(status: DesktopAgentIntegrationStatus | undefined, t: TFunction): string {
  const code = status?.issue?.code;
  if (code === "environment_mismatch") return t("agentIntegration.setupFeedback.environmentMismatch");
  if (code === "configuration_unreadable") return t("agentIntegration.setupFeedback.readFailed");
  if (code === "cdp_unavailable" || code === "installation_unavailable") return t(`agentIntegration.connectionErrors.${code}`, { agent: "WorkBuddy" });
  if (!code) {
    const runtime = status?.connection?.runtime_state;
    if (runtime === "connection_required" || runtime === "waiting") return t(`agentIntegration.connectionErrors.${runtime}`, { agent: "WorkBuddy" });
    return t("agentIntegration.workbuddyInstallGuide.controlUnavailableDescription");
  }
  const key = code && ["workbuddy_login_required", "workbuddy_auth_unreadable", "quit_required", "process_inspection_failed", "control_port_conflict", "control_window_unavailable", "launch_failed", "startup_timeout", "runtime_unavailable", "lazymind_auth_required", "lazymind_unavailable", "configuration_failed", "operation_busy", "operation_cancelled", "startup_unavailable", "pairing_required", "pairing_disabled", "pairing_account_mismatch", "pairing_credential_mismatch", "pairing_invalid", "pairing_unreadable", "mcp_trust_required", "mcp_enable_required", "unsupported_version", "version_unreadable", "capability_unavailable", "host_unavailable"].includes(code) ? code : "operation_failed";
  return t(`agentIntegration.workbuddyErrors.${key}`);
}
