import type { TFunction } from "i18next";
import type { DesktopAgent, DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";

export const AGENT_REPAIR_DOCS_URL = "https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/tree/master/docs";
const AGENT_REPAIR_DOCS_LIST_URL = "https://modelscope.cn/api/v1/datasets/209835/repo/tree?Revision=master&Root=docs&Recursive=True&PageNumber=1&PageSize=100";

const CHECK_ENVIRONMENT_KEYS = [
  "LAZYMIND_HOME", "LAZYMIND_RUNTIME_PROFILE", "LAZYMIND_CONNECTOR_CONFIG",
  "CODEX_HOME", "LAZYMIND_HOST_HOME", "DSH_HOME", "LAZYMIND_DSH_PROFILE",
  "LAZYMIND_CODEX_BIN", "LAZYMIND_DSH_PATH", "LAZYMIND_DSH_DESKTOP_PATH",
] as const;

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
  const diagnostic = status?.diagnostic_context;
  const environment = Object.fromEntries(CHECK_ENVIRONMENT_KEYS.flatMap(key => {
    const value = diagnostic?.environment?.[key];
    return typeof value === "string" ? [[key, value]] : [];
  }));
  const logPath = status?.diagnostic_log;
  if (status?.inspection_unavailable) {
    error = text("inspectionUnavailable");
    status = undefined;
  }
  const connection = status?.connection;
  const noErrorReported = status && !status.issue && !["error", "conflict", "action_required"].includes(status.state);
  const configuration = status?.issue?.code === "configuration_unreadable" ? text("configurationUnreadable")
    : status?.enabled === true ? text("configurationPresent")
      : status?.enabled === false ? text("configurationAbsent") : undefined;
  const state = status ? `${status.state} (${text(`states.${status.state}`)})` : undefined;
  const action = status?.diagnostic_context?.action;
  const actionLabel = action && ["inspect", "connect", "disconnect", "refresh"].includes(action)
    ? `${action} (${text(`actions.${action}`)})` : action;
  const configurationDetails = (status?.diagnostic_context?.configuration || []).flatMap(item => [
    field("configPath", item.config_path),
    ...(item.configured_cli ? [field("configuredCLI", item.configured_cli)] : []),
    ...(item.reason ? [field("configurationReason", item.reason)] : []),
  ]);
  return [
    text("intro"),
    `${text("guide")}: ${AGENT_REPAIR_DOCS_URL}`,
    `${text("guideListing")}: ${AGENT_REPAIR_DOCS_LIST_URL}`,
    text("guideInstructions"),
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
    field("stage", status?.diagnostic_context?.stage || status?.issue?.stage || stage),
    field("retryAction", status?.retry_action),
    field("runtime", connection?.runtime_state || (status ? text("runtimeUnverified") : undefined)),
    field("mcpConfigured", connection?.mcp_configured),
    field("mcpEnabled", connection?.mcp_enabled),
    field("mcpTrusted", connection?.mcp_trusted),
    field("control", connection?.control_ready),
    field("presentation", connection?.presentation_status),
    field("page", connection?.page_status),
    field("diagnosticID", status?.diagnostic_id || status?.issue?.operation_id),
    field("logPath", logPath),
    field("currentCLI", diagnostic?.current_cli),
    field("checkArguments", `internal agent ${agent} status`),
    field("checkEnvironment", Object.keys(environment).length ? JSON.stringify(environment, null, 2) : undefined),
    ...configurationDetails,
    "",
    field("firstAction", text(`hints.${recoveryHint(status)}`)),
    text("instructions"),
    text("handoff"),
    text("verification"),
  ].join("\n");
}
