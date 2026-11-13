import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";
import zhCN from "@/i18n/locales/zh-CN";
import WorkBuddyInstallationGuide from "./WorkBuddyInstallationGuide";

const mocks = vi.hoisted(() => ({ statuses: vi.fn(), node: vi.fn(), local: vi.fn(), desktop: vi.fn() }));
vi.mock("@/runtime/mode", () => ({ isLocalRuntime: mocks.local, isDesktopRuntime: mocks.desktop }));
vi.mock("@/runtime/desktopBridge", () => ({ agentIntegrationStatuses: mocks.statuses }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, values?: { agent?: string }) =>
  String(key.split(".").reduce((value: any, part) => value?.[part], zhCN) || key).replaceAll("{{agent}}", values?.agent || "Agent") }) }));
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

  it.each([
    ["control_port_conflict", "Codex 控制端口被其他服务占用。请处理端口冲突后重新连接。"],
    ["control_window_unavailable", "未找到 Codex 会话窗口。请打开会话窗口后重新检查连接。"],
    ["pairing_credential_mismatch", "Codex 正在使用旧配对凭据，请重新连接以加载当前配对。"],
  ] as const)("shows the specific %s failure and current diagnostic ID", async (code, expected) => {
    await beginCheck({ ...connected, diagnostic_id: "latest-inspection", issue: { code, operation_id: "older-operation" },
      connection: { ...connected.connection!, control_ready: false, runtime_state: "unavailable" } });
    expect(screen.getByText(expected)).toBeInTheDocument();
    expect(screen.getByText(/诊断编号: latest-inspection/)).toBeInTheDocument();
    expect(screen.queryByText(/older-operation/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: labels.done })).not.toBeInTheDocument();
  });

  it.each([
    ["connection_required", "Codex 尚未建立会话控制连接。请点击「重新连接」，保存工作后确认启动或重启。"],
    ["waiting", "Codex 尚未运行。请点击「重新连接」，由 LazyMind 启动并建立会话控制连接。"],
  ] as const)("explains the %s runtime state", async (runtime_state, expected) => {
    await beginCheck({ ...connected, connection: { ...connected.connection!, control_ready: false, runtime_state } });
    expect(screen.getByText(expected)).toBeInTheDocument();
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
    expect(screen.getByText(zhCN.agentIntegration.connectionErrors.capability_unavailable)).toBeInTheDocument();
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
    expect(screen.getByText("Codex 启动失败。请确认应用可以正常打开，然后重新连接。")).toBeInTheDocument();
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
