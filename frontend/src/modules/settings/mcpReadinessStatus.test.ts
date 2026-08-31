import { describe, expect, it } from "vitest";

import type { SettingsOverviewIssue, SettingsOverviewSection } from "./api";
import { resolveMcpReadinessStatus } from "./mcpReadinessStatus";

const mcpSection = (
  enabled: number,
  verified: number,
  runnable: number,
): SettingsOverviewSection => ({
  id: "mcp",
  title: "MCP",
  route: "/settings?section=mcp",
  effective_enabled: runnable > 0,
  counts: { total: enabled, enabled, verified, runnable, configured: 0 },
  status: "ready",
  detail: "",
});

const mcpIssue = (id: string): SettingsOverviewIssue => ({
  id,
  severity: "warning",
  message: "",
  section: "mcp",
});

describe("resolveMcpReadinessStatus", () => {
  it("uses the verification issue when aggregate counts are ambiguous", () => {
    const section = mcpSection(1, 1, 0);
    // The verified service is disabled; the sole enabled service is unverified.
    section.counts.total = 2;

    expect(resolveMcpReadinessStatus(section, [
      mcpIssue("mcp-needs-verification"),
    ])).toBe("needs_verification");
  });

  it("reports authorization from the dedicated backend issue", () => {
    expect(resolveMcpReadinessStatus(mcpSection(2, 2, 0), [
      mcpIssue("mcp-needs-authorization"),
    ])).toBe("needs_authorization");
  });

  it("prioritizes verification when both setup issues are present", () => {
    expect(resolveMcpReadinessStatus(mcpSection(2, 2, 1), [
      mcpIssue("mcp-needs-authorization"),
      mcpIssue("mcp-needs-verification"),
    ])).toBe("needs_verification");
  });

  it("reports availability once at least one service is runnable", () => {
    expect(resolveMcpReadinessStatus(mcpSection(2, 2, 1))).toBe("available");
  });
});
