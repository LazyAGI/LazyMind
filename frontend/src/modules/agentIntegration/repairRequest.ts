import type { TFunction } from "i18next";
import type { DesktopAgent, DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";

export const AGENT_REPAIR_GUIDE_URL = "https://raw.githubusercontent.com/ADAM-CHENG666/LazyRAG/main/docs/external-agent-troubleshooting.md";

export interface AgentRepairContext {
  agent: DesktopAgent;
  status?: DesktopAgentIntegrationStatus;
  error?: string;
  stage?: string;
}

function recoveryHint(status?: DesktopAgentIntegrationStatus): string {
  if (!status) return "check";
  if (status.action?.kind === "quit_agent" || status.issue?.code === "quit_required" || status.retry_action === "disconnect") return "handoff";
  if (status.issue?.code === "configuration_unreadable") return "readConfiguration";
  if (status.state === "conflict" || status.issue?.code === "environment_mismatch") return "environment";
  if (status.issue?.code === "mcp_enable_required") return "enable";
  if (status.state === "ready" && !status.issue && status.enabled === false && status.can_connect === true) return "connect";
  return "diagnose";
}

// Copy allowlisted status fields and the displayed error, never the whole status
// object (which can include raw messages and action URLs). This performs no I/O.
export function buildAgentRepairRequest({ agent, status, error, stage }: AgentRepairContext, t: TFunction): string {
  const text = (key: string) => t(`agentIntegration.repairRequest.${key}`);
  const field = (key: string, value: string | boolean | undefined) =>
    `${text(key)}: ${value === undefined || value === "" ? text("unknown") : String(value)}`;
  const connection = status?.connection;
  const diagnostic = status?.diagnostic_context;
  const noErrorReported = status && !status.issue && !["error", "conflict", "action_required"].includes(status.state);
  const configuration = status?.issue?.code === "configuration_unreadable" ? text("configurationUnreadable")
    : status?.enabled === true ? text("configurationPresent")
      : status?.enabled === false ? text("configurationAbsent") : undefined;
  const state = status ? `${status.state} (${text(`states.${status.state}`)})` : undefined;
  const action = diagnostic?.action;
  const actionLabel = action && ["inspect", "connect", "disconnect", "refresh"].includes(action)
    ? `${action} (${text(`actions.${action}`)})` : action;
  const configurationDetails = (diagnostic?.configuration || []).flatMap(item => [
    field("configPath", item.config_path),
    ...(item.configured_cli ? [field("configuredCLI", item.configured_cli)] : []),
    ...(item.reason ? [field("configurationReason", item.reason)] : []),
  ]);
  return [
    text("intro"),
    `${text("guide")}: ${AGENT_REPAIR_GUIDE_URL}`,
    "",
    text("snapshot"),
    field("agent", status?.display_name ? `${status.display_name} (${agent})` : agent),
    field("appVersion", diagnostic?.app_version),
    field("version", status?.version),
    field("state", state),
    field("configuration", configuration),
    field("operation", actionLabel),
    field("error", error || (noErrorReported ? text("noErrorReported") : undefined)),
    field("code", status?.issue?.code || (noErrorReported ? text("noErrorReported") : undefined)),
    field("stage", diagnostic?.stage || status?.issue?.stage || stage),
    field("retryAction", status?.retry_action),
    field("runtime", connection?.runtime_state || (status ? text("runtimeUnverified") : undefined)),
    field("mcpConfigured", connection?.mcp_configured),
    field("mcpEnabled", connection?.mcp_enabled),
    field("mcpTrusted", connection?.mcp_trusted),
    field("control", connection?.control_ready),
    field("presentation", connection?.presentation_status),
    field("page", connection?.page_status),
    field("diagnosticID", status?.diagnostic_id || status?.issue?.operation_id),
    field("logPath", status?.diagnostic_log),
    field("currentCLI", diagnostic?.current_cli),
    field("checkArguments", `internal agent ${agent} status`),
    ...configurationDetails,
    "",
    field("firstAction", text(`hints.${recoveryHint(status)}`)),
    text("instructions"),
    text("handoff"),
    text("verification"),
  ].join("\n");
}
