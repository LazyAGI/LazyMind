import type { SettingsOverviewIssue, SettingsOverviewSection } from "./api";

export type McpReadinessStatus =
  | "available"
  | "needs_authorization"
  | "needs_verification";

export function resolveMcpReadinessStatus(
  section: SettingsOverviewSection | undefined,
  issues: SettingsOverviewIssue[] = [],
): McpReadinessStatus {
  const mcpIssueIDs = new Set(
    issues.filter((issue) => issue.section === "mcp").map((issue) => issue.id),
  );
  if (mcpIssueIDs.has("mcp-needs-verification")) return "needs_verification";
  if (mcpIssueIDs.has("mcp-needs-authorization")) return "needs_authorization";
  if (section?.effective_enabled) return "available";
  return "needs_verification";
}
