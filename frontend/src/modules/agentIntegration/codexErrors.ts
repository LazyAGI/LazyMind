import type { TFunction } from "i18next";
import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";

const connectionIssueCodes = new Set([
  "host_unavailable", "capability_unavailable", "cdp_unavailable", "control_port_conflict",
  "control_window_unavailable", "process_inspection_failed", "launch_failed", "startup_timeout",
  "startup_unavailable", "runtime_unavailable", "configuration_failed", "pairing_required",
  "pairing_disabled", "pairing_account_mismatch", "pairing_credential_mismatch", "pairing_invalid",
  "pairing_unreadable", "operation_cancelled", "operation_busy", "installation_unavailable",
  "lazymind_auth_required", "lazymind_unavailable",
]);

export function codexErrorMessage(status: DesktopAgentIntegrationStatus | undefined, t: TFunction,
  fallback = "controlUnavailableDescription"): string {
  const code = status?.issue?.code;
  if (code === "mcp_enable_required") return t("agentIntegration.codexInstallGuide.notReadyDescription");
  if (code === "mcp_trust_required") return t("agentIntegration.codexInstallGuide.trustRequiredDescription");
  if (code === "environment_mismatch") return t("agentIntegration.setupFeedback.environmentMismatch");
  if (code === "configuration_unreadable") return t("agentIntegration.connectionErrors.configuration_failed");
  if (code === "quit_required") return t("agentIntegration.quitRequired.description", { agent: "Codex" });
  if (code) return t(`agentIntegration.connectionErrors.${connectionIssueCodes.has(code) ? code : "operation_failed"}`);
  const runtime = status?.connection?.runtime_state;
  if (runtime === "connection_required" || runtime === "waiting") return t(`agentIntegration.connectionErrors.${runtime}`);
  return t(`agentIntegration.codexInstallGuide.${fallback}`);
}
