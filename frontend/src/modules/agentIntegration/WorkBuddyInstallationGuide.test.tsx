import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";
import zhCN from "@/i18n/locales/zh-CN";
import WorkBuddyInstallationGuide from "./WorkBuddyInstallationGuide";

const mocks = vi.hoisted(() => ({ statuses: vi.fn() }));
vi.mock("@/runtime/desktopBridge", () => ({ agentIntegrationStatuses: mocks.statuses }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, values?: { agent?: string }) =>
  String(key.split(".").reduce((value: any, part) => value?.[part], zhCN) || key).replaceAll("{{agent}}", values?.agent || "Agent") }) }));
const copy = zhCN.agentIntegration.workbuddyInstallGuide;
const installed: DesktopAgentIntegrationStatus = {
  agent: "workbuddy", display_name: "WorkBuddy", state: "enabled", requirements: [],
  connection: { presentation_status: "ready", mcp_configured: true, mcp_trusted: false, mcp_enabled: false, control_ready: true },
};
const signedOut: DesktopAgentIntegrationStatus = {
  ...installed, state: "action_required", action: { kind: "login" },
  issue: { code: "workbuddy_login_required", operation_id: "test-operation" },
};
beforeEach(() => vi.clearAllMocks());
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
