import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";
import zhCN from "@/i18n/locales/zh-CN";
import enUS from "@/i18n/locales/en-US";
import RepairRequestCopy from "./RepairRequestCopy";
import { AGENT_REPAIR_GUIDE_URL } from "./repairRequest";

const execCommandDescriptor = Object.getOwnPropertyDescriptor(document, "execCommand");
let copied = "";
beforeEach(() => {
  copied = "";
  Object.defineProperty(document, "execCommand", { configurable: true, value: vi.fn(() => {
    copied = document.getSelection()?.toString() || "";
    return true;
  }) });
});
afterEach(() => {
  cleanup();
  if (execCommandDescriptor) Object.defineProperty(document, "execCommand", execCommandDescriptor);
  else Reflect.deleteProperty(document, "execCommand");
});

describe("copying an Agent repair request", () => {
  it.each(["zh-CN", "en-US"])("copies current diagnostics and preserves false vs unknown (%s)", async (language) => {
    const i18n = createInstance();
    await i18n.init({ lng: language, fallbackLng: false,
      resources: { "zh-CN": { translation: zhCN }, "en-US": { translation: enUS } } });
    const labels = (language === "zh-CN" ? zhCN : enUS).agentIntegration.repairRequest;
    const status: DesktopAgentIntegrationStatus = {
      agent: "codex", display_name: "Codex", state: "error", version: "test-version",
      diagnostic_id: "latest-check", diagnostic_log: "~/logs/agent-setup.log",
      diagnostic_context: { action: "inspect", stage: "control", current_cli: "/current/lazymind", app_version: "test-app-version",
        environment: { LAZYMIND_HOME: "~/LazyMind data", LAZYMIND_RUNTIME_PROFILE: "desktop",
          LAZYMIND_CONNECTOR_CONFIG: "", CODEX_HOME: "~/Codex profile", ACCESS_TOKEN: "private-environment-token" },
        configuration: [{ config_path: "~/agent/config.json", configured_cli: "/previous/lazymind", connection_file: "~/private-connection.json" }] },
      issue: { code: "capability_unavailable", operation_id: "old-operation", stage: "control" },
      connection: { mcp_configured: true, mcp_enabled: true, mcp_trusted: false,
        control_ready: false, presentation_status: "host-offline" },
      message: "raw backend output must stay local",
      action: { kind: "open_url", url: "https://example.test/?token=private" },
    };
    const view = render(<I18nextProvider i18n={i18n}><RepairRequestCopy agent="codex" status={status} /></I18nextProvider>);
    fireEvent.click(screen.getByRole("button", { name: labels.copyHint }));
    await waitFor(() => expect(copied).toContain(AGENT_REPAIR_GUIDE_URL));
    expect(copied).toContain(`${labels.diagnosticID}: latest-check`);
    expect(copied).toContain(`${labels.mcpTrusted}: false`);
    expect(copied).toContain(`${labels.mcpConfigured}: true`);
    expect(copied).toContain(`${labels.page}: ${labels.unknown}`);
    expect(copied).toContain("capability_unavailable");
    expect(copied).toContain("~/logs/agent-setup.log");
    expect(copied).toContain(`${labels.currentCLI}: /current/lazymind`);
    expect(copied).toContain('"LAZYMIND_HOME": "~/LazyMind data"');
    expect(copied).toContain('"LAZYMIND_RUNTIME_PROFILE": "desktop"');
    expect(copied).toContain('"LAZYMIND_CONNECTOR_CONFIG": ""');
    expect(copied).toContain('"CODEX_HOME": "~/Codex profile"');
    expect(copied).toContain(`${labels.configuredCLI}: /previous/lazymind`);
    expect(copied).not.toMatch(/old-operation|raw backend|token=private|private-connection|private-environment-token|ACCESS_TOKEN|agentIntegration\./);

    // A failed recheck must not send a previous successful/failed snapshot.
    view.rerender(<I18nextProvider i18n={i18n}><RepairRequestCopy agent="codex" stage="notReady" error="Check failed" /></I18nextProvider>);
    fireEvent.click(screen.getByRole("button"));
    await waitFor(() => expect(copied).toContain(`${labels.error}: Check failed`));
    expect(copied).toContain(`${labels.mcpConfigured}: ${labels.unknown}`);
    expect(copied).toContain(`${labels.diagnosticID}: ${labels.unknown}`);
    expect(copied).toContain(`${labels.stage}: notReady`);
    expect(copied).toContain(`${labels.checkEnvironment}: ${labels.unknown}`);
    expect(copied).not.toMatch(/latest-check|host-offline|test-version|test-app-version|\/current\/lazymind|\/previous\/lazymind/);
  });

  it.each(["zh-CN", "en-US"])("does not turn an unavailable inspection into current evidence (%s)", async language => {
    const i18n = createInstance();
    await i18n.init({ lng: language, fallbackLng: false,
      resources: { "zh-CN": { translation: zhCN }, "en-US": { translation: enUS } } });
    const labels = (language === "zh-CN" ? zhCN : enUS).agentIntegration.repairRequest;
    const previous: DesktopAgentIntegrationStatus = {
      agent: "codex", display_name: "Codex", state: "enabled", enabled: true,
      inspection_unavailable: true, diagnostic_id: "previous-check", diagnostic_log: "~/logs/agent-setup.log",
      issue: { code: "host_unavailable", operation_id: "previous-operation" },
      diagnostic_context: { action: "inspect", stage: "inspect", current_cli: "/installed/lazymind",
        environment: { LAZYMIND_HOME: "~/installed-home", LAZYMIND_CONNECTOR_CONFIG: "" },
        configuration: [{ config_path: "~/old-config", reason: "installation_mismatch" }] },
      connection: { mcp_configured: true, mcp_enabled: true, mcp_trusted: true, control_ready: false,
        presentation_status: "host-offline", runtime_state: "unavailable" },
    };
    render(<I18nextProvider i18n={i18n}><RepairRequestCopy agent="codex" status={previous} /></I18nextProvider>);
    fireEvent.click(screen.getByRole("button", { name: labels.copyHint }));
    await waitFor(() => expect(copied).toContain(labels.inspectionUnavailable));
    for (const key of ["configuration", "state", "code", "operation", "stage", "mcpConfigured", "mcpEnabled", "mcpTrusted", "control", "runtime", "diagnosticID"] as const) {
      expect(copied).toContain(`${labels[key]}: ${labels.unknown}`);
    }
    expect(copied).toContain(labels.hints.check);
    expect(copied).toContain("/installed/lazymind");
    expect(copied).toContain('"LAZYMIND_HOME": "~/installed-home"');
    expect(copied).toContain("~/logs/agent-setup.log");
    expect(copied).not.toMatch(/previous-check|previous-operation|host_unavailable|host-offline|old-config|installation_mismatch/);
  });

  const dsh: DesktopAgentIntegrationStatus = { agent: "deepseek-harness", display_name: "DeepSeek Harness", state: "ready" };
  const scenarios: Array<{ name: string; status: DesktopAgentIntegrationStatus;
    configuration: "configurationAbsent" | "configurationPresent" | "configurationUnreadable" | "unknown";
    hint: keyof typeof zhCN.agentIntegration.repairRequest.hints }> = [
    { name: "completed disconnect", status: { ...dsh, enabled: false, can_connect: true,
      diagnostic_context: { action: "disconnect", stage: "disconnect", current_cli: "/current/lazymind" } },
      configuration: "configurationAbsent", hint: "connect" },
    { name: "ready without configuration evidence", status: dsh, configuration: "unknown", hint: "diagnose" },
    { name: "native plugin disabled", status: { ...dsh, state: "enabled", enabled: true,
      issue: { code: "mcp_enable_required" }, connection: { mcp_configured: true, mcp_enabled: false,
        control_ready: false, presentation_status: "host-offline", runtime_state: "unavailable" } },
      configuration: "configurationPresent", hint: "enable" },
    { name: "installation mismatch", status: { ...dsh, state: "conflict", enabled: true, issue: { code: "environment_mismatch" },
      diagnostic_context: { action: "inspect", current_cli: "/current/lazymind",
        configuration: [{ config_path: "~/dsh/cordis.patch.yml", configured_cli: "/previous/lazymind", reason: "installation_mismatch" }] } },
      configuration: "configurationPresent", hint: "environment" },
    { name: "unreadable configuration", status: { ...dsh, state: "error", issue: { code: "configuration_unreadable" } },
      configuration: "configurationUnreadable", hint: "readConfiguration" },
    { name: "pending disconnect requiring exit", status: { ...dsh, state: "action_required", enabled: true,
      retry_action: "disconnect", issue: { code: "quit_required" }, action: { kind: "quit_agent" } },
      configuration: "configurationPresent", hint: "handoff" },
  ];

  describe.each(["zh-CN", "en-US"])("recovery directions (%s)", (language) => {
    it.each(scenarios)("copies evidence and the next branch for $name", async ({ status, configuration, hint }) => {
      const i18n = createInstance();
      await i18n.init({ lng: language, fallbackLng: false,
        resources: { "zh-CN": { translation: zhCN }, "en-US": { translation: enUS } } });
      const labels = (language === "zh-CN" ? zhCN : enUS).agentIntegration.repairRequest;
      render(<I18nextProvider i18n={i18n}><RepairRequestCopy agent="deepseek-harness" status={status} /></I18nextProvider>);
      fireEvent.click(screen.getByRole("button", { name: labels.copyHint }));
      await waitFor(() => expect(copied).toContain(`${labels.configuration}: ${labels[configuration]}`));
      expect(copied).toContain(`${labels.firstAction}: ${labels.hints[hint]}`);
      expect(copied).toContain(`${labels.state}: ${status.state} (${labels.states[status.state]})`);
      expect(copied).toContain(`${labels.checkArguments}: internal agent deepseek-harness status`);
      if (status.diagnostic_context?.action === "disconnect") {
        expect(copied).toContain(`${labels.operation}: disconnect (${labels.actions.disconnect})`);
        expect(copied).toContain(`${labels.code}: ${labels.noErrorReported}`);
        expect(copied).toContain(`${labels.runtime}: ${labels.runtimeUnverified}`);
      }
      if (status.issue?.code === "mcp_enable_required") {
        expect(copied).toContain(`${labels.mcpEnabled}: false`);
      }
      if (status.issue?.code === "environment_mismatch") {
        expect(copied).toContain(`${labels.currentCLI}: /current/lazymind`);
        expect(copied).toContain(`${labels.configuredCLI}: /previous/lazymind`);
        expect(copied).toContain(`${labels.configurationReason}: installation_mismatch`);
      }
      expect(copied).not.toContain("agentIntegration.");
    });
  });
});
