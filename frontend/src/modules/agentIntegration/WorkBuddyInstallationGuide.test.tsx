import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createInstance } from "i18next";
import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";
import zhCN from "@/i18n/locales/zh-CN";
import WorkBuddyInstallationGuide from "./WorkBuddyInstallationGuide";

const guideI18n = createInstance();
void guideI18n.init({ lng: "zh-CN", fallbackLng: false, initImmediate: false,
  resources: { "zh-CN": { translation: zhCN } } });

const mocks = vi.hoisted(() => ({ statuses: vi.fn(), node: vi.fn(), local: vi.fn(), desktop: vi.fn() }));
vi.mock("@/runtime/mode", () => ({ isLocalRuntime: mocks.local, isDesktopRuntime: mocks.desktop }));
vi.mock("@/runtime/desktopBridge", () => ({ agentIntegrationStatuses: mocks.statuses }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, values?: { agent?: string }) => guideI18n.t(key, values) }) }));
vi.mock("@/modules/modelProvider/api/systemDependencies", () => ({ getNodeDependencyStatus: mocks.node }));
const copy = zhCN.agentIntegration.workbuddyInstallGuide;
const installed: DesktopAgentIntegrationStatus = {
  agent: "workbuddy", display_name: "WorkBuddy", state: "enabled", requirements: [],
  connection: { presentation_status: "ready", mcp_configured: true, mcp_trusted: false, mcp_enabled: false, control_ready: true },
};
const signedOut: DesktopAgentIntegrationStatus = {
  ...installed, state: "action_required", action: { kind: "login" },
  issue: { code: "workbuddy_login_required", operation_id: "test-operation" },
};
beforeEach(() => {
  vi.clearAllMocks();
  mocks.node.mockResolvedValue({ installed: true });
  mocks.local.mockReturnValue(true);
  mocks.desktop.mockReturnValue(false);
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe("manual agent shutdown", () => {
  it.each(["workbuddy", "codex"] as const)("waits for an explicit retry for %s and resumes after it quits", async (agent) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const quit: DesktopAgentIntegrationStatus = { ...installed, agent, display_name: agent === "codex" ? "Codex" : "WorkBuddy",
      state: "action_required", action: { kind: "quit_agent" }, issue: { code: "quit_required", operation_id: "quit-test" } };
    const success = { ...installed, agent };
    const connect = vi.fn().mockResolvedValueOnce(quit).mockResolvedValueOnce(quit).mockResolvedValue(success);
    const labels = agent === "codex" ? zhCN.agentIntegration.codexInstallGuide : copy;
    render(<WorkBuddyInstallationGuide agent={agent} onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: labels.saveAndContinue }));
    await screen.findByRole("button", { name: zhCN.agentIntegration.quitRequired.retryInstall });
    expect(screen.getByText(guideI18n.t("agentIntegration.quitRequired.description", { agent: quit.display_name }))).toBeInTheDocument();
    expect(screen.queryByText(labels.installFailedTitle)).not.toBeInTheDocument();
    expect(screen.getByText(/quit-test/)).toBeInTheDocument();
    await act(async () => { fireEvent(window, new Event("focus")); await vi.advanceTimersByTimeAsync(10000); });
    expect(connect).toHaveBeenCalledTimes(1);
    expect(mocks.statuses).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: zhCN.agentIntegration.quitRequired.retryInstall }));
    await waitFor(() => expect(connect).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("button", { name: zhCN.agentIntegration.quitRequired.retryInstall })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: zhCN.agentIntegration.quitRequired.retryInstall }));
    await screen.findByText(labels.installedTitle);
    expect(connect).toHaveBeenCalledTimes(3);
  });

  it("keeps inspection failures in the installation error stage", async () => {
    const connect = vi.fn().mockResolvedValue({ ...installed, state: "error", issue: { code: "process_inspection_failed" } });
    render(<WorkBuddyInstallationGuide onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: copy.saveAndContinue }));
    await screen.findByText(copy.installFailedTitle);
    expect(screen.queryByRole("button", { name: zhCN.agentIntegration.quitRequired.retryInstall })).not.toBeInTheDocument();
  });
});

describe("installation status recovery", () => {
  it.each(["workbuddy", "codex"] as const)("clears a stale installation failure for %s after a healthy inspection", async (agent) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const labels = agent === "codex" ? zhCN.agentIntegration.codexInstallGuide : copy;
    const pending: DesktopAgentIntegrationStatus = { ...installed, agent, state: "error",
      issue: { code: "capability_unavailable", operation_id: "startup-test" },
      connection: { ...installed.connection, mcp_trusted: true, mcp_enabled: true, control_ready: false } };
    const connect = vi.fn().mockResolvedValue(pending);
    const update = vi.fn();
    mocks.statuses.mockResolvedValue({ ok: true, data: { [agent]: pending } });
    render(<WorkBuddyInstallationGuide agent={agent} onConnect={connect} onStatus={update} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: labels.saveAndContinue }));
    await screen.findByText(labels.installFailedTitle);
    await act(async () => { await vi.advanceTimersByTimeAsync(3100); });
    expect(screen.getByText(labels.installFailedTitle)).toBeInTheDocument();
    const healthy: DesktopAgentIntegrationStatus = { ...pending, state: "enabled", issue: undefined,
      connection: { ...pending.connection, control_ready: true } };
    mocks.statuses.mockResolvedValue({ ok: true, data: { [agent]: healthy } });
    fireEvent(window, new Event("focus"));
    await screen.findByText(labels.successTitle);
    expect(update).toHaveBeenCalledWith(healthy);
    expect(screen.queryByText(labels.installFailedTitle)).not.toBeInTheDocument();
    expect(connect).toHaveBeenCalledTimes(1);
  });
});

describe("Codex connection diagnostics", () => {
  const labels = zhCN.agentIntegration.codexInstallGuide;
  const connected: DesktopAgentIntegrationStatus = { ...installed, agent: "codex", display_name: "Codex",
    connection: { ...installed.connection!, mcp_trusted: true, mcp_enabled: true, control_ready: true, runtime_state: "connected" } };

  const beginCheck = async (next: DesktopAgentIntegrationStatus) => {
    mocks.statuses.mockResolvedValue({ ok: true, data: { codex: next } });
    render(<WorkBuddyInstallationGuide agent="codex" status={connected} onConnect={vi.fn()} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: labels.enabledConfirmation }));
    fireEvent.click(screen.getByRole("button", { name: labels.check }));
    await screen.findByText(labels.notReadyTitle);
  };

  it.each(["control_port_conflict", "control_window_unavailable", "pairing_credential_mismatch"] as const)("shows the specific %s failure and current diagnostic ID", async (code) => {
    await beginCheck({ ...connected, diagnostic_id: "latest-inspection", issue: { code, operation_id: "older-operation" },
      connection: { ...connected.connection!, control_ready: false, runtime_state: "unavailable" } });
    expect(screen.getByText(guideI18n.t(`agentIntegration.connectionErrors.${code}`, { agent: "Codex" }))).toBeInTheDocument();
    expect(screen.getByText(/诊断编号: latest-inspection/)).toBeInTheDocument();
    expect(screen.queryByText(/older-operation/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: labels.done })).not.toBeInTheDocument();
  });

  it.each(["connection_required", "waiting"] as const)("explains the %s runtime state", async (runtime_state) => {
    await beginCheck({ ...connected, connection: { ...connected.connection!, control_ready: false, runtime_state } });
    expect(screen.getByText(guideI18n.t(`agentIntegration.connectionErrors.${runtime_state}`, { agent: "Codex" }))).toBeInTheDocument();
  });

  it("updates a changed failure and diagnostics during automatic checks without showing a loading step", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await beginCheck({ ...connected, diagnostic_id: "first-check", issue: { code: "cdp_unavailable" },
      connection: { ...connected.connection!, control_ready: false } });
    let finishCheck: ((value: unknown) => void) | undefined;
    mocks.statuses.mockImplementationOnce(() => new Promise(resolve => { finishCheck = resolve; }));
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(screen.getByText(labels.notReadyTitle)).toBeInTheDocument();
    expect(screen.queryByText(labels.checkingDescription)).not.toBeInTheDocument();
    await act(async () => { finishCheck?.({ ok: true, data: { codex: { ...connected, diagnostic_id: "latest-check",
      issue: { code: "capability_unavailable" }, connection: { ...connected.connection!, control_ready: false } } } }); });
    expect(screen.getByText(guideI18n.t("agentIntegration.connectionErrors.capability_unavailable", { agent: "Codex" }))).toBeInTheDocument();
    expect(screen.getByText(/诊断编号: latest-check/)).toBeInTheDocument();
    expect(screen.queryByText(/first-check/)).not.toBeInTheDocument();
    expect(screen.queryByText(labels.successTitle)).not.toBeInTheDocument();
    mocks.statuses.mockResolvedValueOnce({ ok: false, reason: "failed" });
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(screen.getByText(labels.checkFailedDescription)).toBeInTheDocument();
    expect(screen.queryByText(/latest-check/)).not.toBeInTheDocument();
    expect(screen.queryByText(labels.checkingDescription)).not.toBeInTheDocument();
  });

  it("uses a specific connect failure instead of exposing the raw backend message", async () => {
    const onConnect = vi.fn().mockResolvedValue({ ...connected, state: "error", diagnostic_id: "launch-check",
      issue: { code: "launch_failed" }, message: "private executable path" });
    render(<WorkBuddyInstallationGuide agent="codex" onConnect={onConnect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: labels.saveAndContinue }));
    await screen.findByText(labels.installFailedTitle);
    expect(screen.getByText(guideI18n.t("agentIntegration.connectionErrors.launch_failed", { agent: "Codex" }))).toBeInTheDocument();
    expect(screen.queryByText("private executable path")).not.toBeInTheDocument();
  });

  it("keeps the plugin enablement step available when native enablement is required", async () => {
    const onConnect = vi.fn().mockResolvedValue({ ...connected, issue: { code: "mcp_enable_required" },
      connection: { ...connected.connection!, mcp_enabled: false, control_ready: false } });
    render(<WorkBuddyInstallationGuide agent="codex" onConnect={onConnect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: labels.saveAndContinue }));
    await screen.findByRole("button", { name: labels.enabledConfirmation });
    expect(screen.queryByText(labels.installFailedTitle)).not.toBeInTheDocument();
    expect(screen.queryByText(labels.successTitle)).not.toBeInTheDocument();
  });
});

describe("explicit Codex connection failure stability", () => {
  const labels = zhCN.agentIntegration.codexInstallGuide;
  const healthy: DesktopAgentIntegrationStatus = { ...installed, agent: "codex", display_name: "Codex",
    connection: { ...installed.connection!, mcp_trusted: true, mcp_enabled: true, control_ready: true, runtime_state: "connected" } };

  it.each(["poll", "focus"] as const)("preserves the failed connect message and empty diagnostics through ordinary %s updates", async (trigger) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const operationError = zhCN.agentIntegration.setupFeedback.enableFailed;
    const connect = vi.fn().mockRejectedValue(new Error(operationError));
    const update = vi.fn();
    render(<WorkBuddyInstallationGuide agent="codex" onConnect={connect} onStatus={update} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: labels.saveAndContinue }));
    await screen.findByText(labels.installFailedTitle);
    expect(screen.getByText(operationError)).toBeInTheDocument();
    expect(screen.queryByText(/诊断编号:/)).not.toBeInTheDocument();

    const disabled: DesktopAgentIntegrationStatus = { ...healthy, diagnostic_id: "background-disabled-check",
      issue: { code: "mcp_enable_required" },
      connection: { ...healthy.connection!, mcp_enabled: false, control_ready: false, runtime_state: "unavailable" } };
    const unready: DesktopAgentIntegrationStatus = { ...healthy, diagnostic_id: "background-waiting-check",
      connection: { ...healthy.connection!, control_ready: false, runtime_state: "connection_required" } };
    for (const current of [disabled, unready, undefined, "unavailable", "rejected"] as const) {
      if (current === "unavailable") mocks.statuses.mockResolvedValue({ ok: false, reason: "unavailable" });
      else if (current === "rejected") mocks.statuses.mockRejectedValue(new Error("Background inspection unavailable"));
      else mocks.statuses.mockResolvedValue({ ok: true, data: current ? { codex: current } : {} });
      await act(async () => {
        if (trigger === "focus") fireEvent(window, new Event("focus"));
        else await vi.advanceTimersByTimeAsync(3000);
      });
      expect(screen.getByText(labels.installFailedTitle)).toBeInTheDocument();
      expect(screen.getByText(operationError)).toBeInTheDocument();
      expect(screen.queryByText(/诊断编号:/)).not.toBeInTheDocument();
      expect(screen.queryByText(labels.enableRequiredDescription)).not.toBeInTheDocument();
      expect(screen.queryByText(labels.notReadyTitle)).not.toBeInTheDocument();
      expect(screen.queryByText(labels.checkingDescription)).not.toBeInTheDocument();
      expect(connect).toHaveBeenCalledTimes(1);
    }

    mocks.statuses.mockResolvedValue({ ok: true, data: { codex: healthy } });
    await act(async () => {
      if (trigger === "focus") fireEvent(window, new Event("focus"));
      else await vi.advanceTimersByTimeAsync(3000);
    });
    expect(await screen.findByText(labels.successTitle)).toBeInTheDocument();
    expect(screen.queryByText(labels.installFailedTitle)).not.toBeInTheDocument();
    expect(screen.queryByText(operationError)).not.toBeInTheDocument();
    expect(update).toHaveBeenCalledWith(healthy);
    expect(connect).toHaveBeenCalledTimes(1);
  });

  it("preserves the failed connect diagnostic instead of replacing it with a background inspection", async () => {
    const failed: DesktopAgentIntegrationStatus = { ...healthy, state: "error", diagnostic_id: "explicit-connect-failure",
      issue: { code: "launch_failed", operation_id: "connect-operation" } };
    const ordinary: DesktopAgentIntegrationStatus = { ...healthy, diagnostic_id: "background-inspection",
      connection: { ...healthy.connection!, control_ready: false, runtime_state: "unavailable" } };
    const connect = vi.fn().mockResolvedValue(failed);
    mocks.statuses.mockResolvedValue({ ok: true, data: { codex: ordinary } });
    render(<WorkBuddyInstallationGuide agent="codex" onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: labels.saveAndContinue }));
    await screen.findByText(labels.installFailedTitle);
    expect(screen.getByText(guideI18n.t("agentIntegration.connectionErrors.launch_failed", { agent: "Codex" }))).toBeInTheDocument();
    expect(screen.getByText(/诊断编号: explicit-connect-failure/)).toBeInTheDocument();
    await act(async () => { fireEvent(window, new Event("focus")); });
    expect(screen.getByText(labels.installFailedTitle)).toBeInTheDocument();
    expect(screen.getByText(guideI18n.t("agentIntegration.connectionErrors.launch_failed", { agent: "Codex" }))).toBeInTheDocument();
    expect(screen.getByText(/诊断编号: explicit-connect-failure/)).toBeInTheDocument();
    expect(screen.queryByText(/background-inspection/)).not.toBeInTheDocument();
    expect(screen.queryByText(labels.notReadyTitle)).not.toBeInTheDocument();
    expect(connect).toHaveBeenCalledTimes(1);
  });

  it("clears the previous operation diagnostic when an explicit retry fails without an ID", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const failed: DesktopAgentIntegrationStatus = { ...healthy, state: "error", diagnostic_id: "previous-connect-failure",
      issue: { code: "launch_failed" } };
    const operationError = zhCN.agentIntegration.setupFeedback.enableFailed;
    const connect = vi.fn().mockResolvedValueOnce(failed).mockRejectedValue(new Error(operationError));
    const mismatch: DesktopAgentIntegrationStatus = { ...healthy, diagnostic_id: "unrelated-background-inspection",
      issue: { code: "pairing_account_mismatch" },
      connection: { ...healthy.connection!, control_ready: false, runtime_state: "waiting" } };
    mocks.statuses.mockResolvedValue({ ok: true, data: { codex: mismatch } });
    render(<WorkBuddyInstallationGuide agent="codex" onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: labels.saveAndContinue }));
    await screen.findByText(labels.installFailedTitle);
    expect(screen.getByText(/诊断编号: previous-connect-failure/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: labels.retryInstall }));
    expect(await screen.findByText(operationError)).toBeInTheDocument();
    expect(screen.getByText(labels.installFailedTitle)).toBeInTheDocument();
    expect(screen.queryByText(/诊断编号:/)).not.toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(screen.getByText(labels.installFailedTitle)).toBeInTheDocument();
    expect(screen.getByText(operationError)).toBeInTheDocument();
    expect(screen.queryByText(/诊断编号:/)).not.toBeInTheDocument();
    expect(screen.queryByText(guideI18n.t("agentIntegration.connectionErrors.pairing_account_mismatch", { agent: "Codex" }))).not.toBeInTheDocument();
    expect(screen.queryByText(labels.notReadyTitle)).not.toBeInTheDocument();
    expect(connect).toHaveBeenCalledTimes(2);
  });
});

describe("account pairing diagnostics", () => {
  it.each(["codex", "workbuddy"] as const)("prioritizes account mismatch during %s connection checks", async (agent) => {
    const labels = agent === "codex" ? zhCN.agentIntegration.codexInstallGuide : copy;
    const healthy: DesktopAgentIntegrationStatus = { ...installed, agent,
      display_name: agent === "codex" ? "Codex" : "WorkBuddy",
      connection: { ...installed.connection!, mcp_enabled: true, mcp_trusted: true, control_ready: true, runtime_state: "connected" } };
    const mismatch: DesktopAgentIntegrationStatus = { ...healthy,
      issue: { code: "pairing_account_mismatch" }, message: "private native pairing details",
      connection: { ...healthy.connection!, control_ready: false, runtime_state: "waiting" } };
    mocks.statuses.mockResolvedValue({ ok: true, data: { [agent]: mismatch } });
    const connect = vi.fn();
    render(<WorkBuddyInstallationGuide agent={agent} status={healthy} onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    if (agent === "workbuddy") {
      fireEvent.click(screen.getByRole("button", { name: copy.next }));
      fireEvent.click(screen.getByRole("button", { name: copy.trustConfirmation }));
    }
    fireEvent.click(screen.getByRole("button", { name: labels.enabledConfirmation }));
    fireEvent.click(screen.getByRole("button", { name: labels.check }));
    await screen.findByText(labels.notReadyTitle);
    const expected = guideI18n.t(`agentIntegration.${agent === "codex" ? "connectionErrors" : "workbuddyErrors"}.pairing_account_mismatch`, { agent: healthy.display_name });
    expect(screen.getByText(expected)).toBeInTheDocument();
    expect(screen.queryByText(guideI18n.t("agentIntegration.connectionErrors.waiting", { agent: healthy.display_name }))).not.toBeInTheDocument();
    expect(screen.queryByText("private native pairing details")).not.toBeInTheDocument();
    expect(screen.queryByText(labels.successTitle)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: labels.reconnect }));
    expect(screen.getByRole("button", { name: labels.saveAndContinue })).toBeInTheDocument();
    expect(connect).not.toHaveBeenCalled();
  });

  it.each([
    ["workbuddy", "focus", "entry"],
    ["codex", "poll", "enable"],
    ["workbuddy", "poll", "waiting"],
    ["codex", "focus", "waiting"],
  ] as const)("shows account mismatch from a background %s %s check while in %s", async (agent, trigger, initialStage) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const labels = agent === "codex" ? zhCN.agentIntegration.codexInstallGuide : copy;
    const healthy: DesktopAgentIntegrationStatus = { ...installed, agent,
      display_name: agent === "codex" ? "Codex" : "WorkBuddy",
      connection: { ...installed.connection!, mcp_enabled: true, mcp_trusted: true, control_ready: true, runtime_state: "connected" } };
    const waiting: DesktopAgentIntegrationStatus = { ...healthy,
      connection: { ...healthy.connection!, control_ready: false, runtime_state: "waiting" } };
    const mismatch: DesktopAgentIntegrationStatus = { ...waiting, diagnostic_id: "account-background-check",
      issue: { code: "pairing_account_mismatch", operation_id: "older-account-operation" },
      message: "private native pairing details" };
    const connect = vi.fn();
    const update = vi.fn();
    mocks.statuses.mockResolvedValue({ ok: true, data: { [agent]: waiting } });
    render(<WorkBuddyInstallationGuide agent={agent} status={healthy} onConnect={connect} onStatus={update} onClose={vi.fn()} />);
    if (initialStage === "waiting") {
      if (agent === "workbuddy") {
        fireEvent.click(screen.getByRole("button", { name: copy.next }));
        fireEvent.click(screen.getByRole("button", { name: copy.trustConfirmation }));
      }
      fireEvent.click(screen.getByRole("button", { name: labels.enabledConfirmation }));
      fireEvent.click(screen.getByRole("button", { name: labels.check }));
      await screen.findByText(labels.notReadyTitle);
    }
    mocks.statuses.mockResolvedValue({ ok: true, data: { [agent]: mismatch } });
    await act(async () => {
      if (trigger === "focus") fireEvent(window, new Event("focus"));
      else await vi.advanceTimersByTimeAsync(3000);
    });
    const expected = guideI18n.t(`agentIntegration.${agent === "codex" ? "connectionErrors" : "workbuddyErrors"}.pairing_account_mismatch`, { agent: healthy.display_name });
    expect(await screen.findByText(expected)).toBeInTheDocument();
    expect(screen.getByText(labels.notReadyTitle)).toBeInTheDocument();
    expect(screen.getByText(/诊断编号: account-background-check/)).toBeInTheDocument();
    expect(screen.queryByText(/older-account-operation/)).not.toBeInTheDocument();
    expect(screen.queryByText(guideI18n.t("agentIntegration.connectionErrors.waiting", { agent: healthy.display_name }))).not.toBeInTheDocument();
    expect(screen.queryByText("private native pairing details")).not.toBeInTheDocument();
    expect(screen.queryByText(labels.installedTitle)).not.toBeInTheDocument();
    expect(screen.queryByText(labels.checkingDescription)).not.toBeInTheDocument();
    expect(update).toHaveBeenCalledWith(mismatch);
    expect(connect).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: labels.reconnect }));
    expect(screen.getByRole("button", { name: labels.saveAndContinue })).toBeInTheDocument();
    expect(connect).not.toHaveBeenCalled();
  });

  it.each(["workbuddy", "codex"] as const)("keeps the %s trust or enable step during other background failures", async (agent) => {
    const labels = agent === "codex" ? zhCN.agentIntegration.codexInstallGuide : copy;
    const healthy: DesktopAgentIntegrationStatus = { ...installed, agent,
      display_name: agent === "codex" ? "Codex" : "WorkBuddy",
      connection: { ...installed.connection!, mcp_enabled: true, mcp_trusted: true, control_ready: true } };
    const disabled: DesktopAgentIntegrationStatus = { ...healthy, issue: { code: "mcp_enable_required" },
      connection: { ...healthy.connection!, mcp_enabled: false, control_ready: false } };
    mocks.statuses.mockResolvedValue({ ok: true, data: { [agent]: disabled } });
    const connect = vi.fn();
    render(<WorkBuddyInstallationGuide agent={agent} status={healthy} onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    await act(async () => { fireEvent(window, new Event("focus")); });
    expect(screen.getByRole("button", { name: agent === "codex" ? labels.enabledConfirmation : labels.next })).toBeInTheDocument();
    expect(screen.queryByText(labels.notReadyTitle)).not.toBeInTheDocument();
    expect(connect).not.toHaveBeenCalled();
  });
});

describe("WorkBuddy native sign-in setup", () => {
  it("opens native sign-in and resumes installation only after login succeeds", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const connect = vi.fn().mockResolvedValueOnce(signedOut).mockResolvedValue(installed);
    const login = vi.fn().mockResolvedValue({ ...signedOut, login: { state: "running" } });
    const update = vi.fn();
    mocks.statuses.mockResolvedValue({ ok: true, data: { workbuddy: { ...signedOut, login: { state: "running" } } } });
    render(<WorkBuddyInstallationGuide onConnect={connect} onLogin={login} onStatus={update} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: copy.saveAndContinue }));
    await waitFor(() => expect(login).toHaveBeenCalledTimes(1));
    expect(screen.getByText(copy.loginDescription)).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(1600); });
    expect(connect).toHaveBeenCalledTimes(1);
    mocks.statuses.mockResolvedValue({ ok: true, data: { workbuddy: { ...signedOut, login: { state: "succeeded" } } } });
    await act(async () => { await vi.advanceTimersByTimeAsync(1600); });
    expect(connect).toHaveBeenCalledTimes(2);
    expect(screen.getByText(copy.entryDescription)).toBeInTheDocument();
    expect(screen.queryByText(copy.successTitle)).not.toBeInTheDocument();
    const healthy = { ...installed, connection: { ...installed.connection, mcp_trusted: true, mcp_enabled: true } };
    mocks.statuses.mockResolvedValue({ ok: true, data: { workbuddy: healthy } });
    fireEvent(window, new Event("focus"));
    await waitFor(() => expect(screen.getByText(copy.successTitle)).toBeInTheDocument());
    expect(update).toHaveBeenCalledWith(healthy);
  });

  it("shows a retryable failure when native sign-in fails without restarting", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const connect = vi.fn().mockResolvedValue(signedOut);
    mocks.statuses.mockResolvedValue({ ok: true, data: { workbuddy: { ...signedOut, login: { state: "failed" } } } });
    render(<WorkBuddyInstallationGuide onConnect={connect} onLogin={vi.fn().mockResolvedValue(signedOut)} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: copy.saveAndContinue }));
    await waitFor(() => expect(screen.getByText(copy.loginDescription)).toBeInTheDocument());
    await act(async () => { await vi.advanceTimersByTimeAsync(1600); });
    expect(screen.getByText(copy.loginFailed)).toBeInTheDocument();
    expect(screen.getByText(/test-operation/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: copy.retryInstall })).toBeEnabled();
    expect(connect).toHaveBeenCalledTimes(1);
  });
});


describe("Codex Node.js prerequisite", () => {
  it("keeps cloud connections on the local assistant bridge without probing the cloud server for Node", async () => {
    mocks.local.mockReturnValue(false);
    mocks.node.mockRejectedValue(new Error("Node.js dependency is only available locally"));
    const connect = vi.fn().mockResolvedValue({ ...installed, agent: "codex" });
    render(<WorkBuddyInstallationGuide agent="codex" onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: zhCN.agentIntegration.codexInstallGuide.saveAndContinue }));
    await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
    expect(mocks.node).not.toHaveBeenCalled();
    expect(screen.queryByRole("link", { name: zhCN.nodeDependency.goto })).not.toBeInTheDocument();
  });

  it("still checks Node before connecting from the desktop runtime", async () => {
    mocks.local.mockReturnValue(false);
    mocks.desktop.mockReturnValue(true);
    mocks.node.mockResolvedValue({ installed: false });
    const connect = vi.fn();
    render(<WorkBuddyInstallationGuide agent="codex" onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: zhCN.agentIntegration.codexInstallGuide.saveAndContinue }));
    expect(await screen.findByRole("link", { name: zhCN.nodeDependency.goto })).toBeInTheDocument();
    expect(mocks.node).toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();
  });

  it("detects and displays the existing Node version when opened", async () => {
    mocks.node.mockResolvedValue({ installed: true, version: "v22.22.2" });
    const connect = vi.fn();
    render(<WorkBuddyInstallationGuide agent="codex" onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    expect(await screen.findByText(/v22.22.2/)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: zhCN.nodeDependency.goto })).not.toBeInTheDocument();
    expect(connect).not.toHaveBeenCalled();
  });

  it("allows retrying detection without starting a connection", async () => {
    mocks.node.mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ installed: false, version: "v20.0.0" });
    const connect = vi.fn();
    render(<WorkBuddyInstallationGuide agent="codex" onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: zhCN.nodeDependency.refresh }));
    expect(await screen.findByText(/v20.0.0/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: zhCN.nodeDependency.goto })).toHaveAttribute("href", "/settings?section=system_tools#node-dependency");
    expect(connect).not.toHaveBeenCalled();
  });

  it("blocks connection until Node is available and links to dependency installation", async () => {
    mocks.node.mockResolvedValueOnce({ installed: false }).mockResolvedValueOnce({ installed: false }).mockResolvedValue({ installed: true });
    const connect = vi.fn().mockResolvedValue({ ...installed, agent: "codex" });
    render(<WorkBuddyInstallationGuide agent="codex" onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: zhCN.agentIntegration.codexInstallGuide.saveAndContinue }));
    expect(await screen.findByRole("link", { name: zhCN.nodeDependency.goto })).toHaveAttribute("href", "/settings?section=system_tools#node-dependency");
    expect(connect).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: zhCN.nodeDependency.refresh }));
    await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
  });
});
