import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DesktopAgentIntegrationStatus } from "@/runtime/desktopBridge";
import zhCN from "@/i18n/locales/zh-CN";
import DSHInstallationGuide from "./DSHInstallationGuide";

const mocks = vi.hoisted(() => ({ statuses: vi.fn() }));
vi.mock("@/runtime/desktopBridge", () => ({ agentIntegrationStatuses: mocks.statuses }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, values?: { agent?: string }) =>
  String(key.split(".").reduce((value: any, part) => value?.[part], zhCN) || key).replaceAll("{{agent}}", values?.agent || "Agent") }) }));
const copy = zhCN.agentIntegration.dshInstallGuide;
const configured: DesktopAgentIntegrationStatus = {
  agent: "deepseek-harness", display_name: "DeepSeek Harness", state: "enabled", enabled: true,
  connection: { mcp_configured: true, mcp_enabled: true, mcp_trusted: true, control_ready: false,
    presentation_status: "host-offline", runtime_state: "waiting" },
};
const connected: DesktopAgentIntegrationStatus = { ...configured,
  connection: { ...configured.connection!, control_ready: true, runtime_state: "connected" } };
beforeEach(() => vi.clearAllMocks());
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe("DeepSeek Harness connection verification", () => {
  it("shows the plugin image only after installation reaches the third step", async () => {
    let finish!: (status: DesktopAgentIntegrationStatus) => void;
    const connect = vi.fn(() => new Promise<DesktopAgentIntegrationStatus>(resolve => { finish = resolve; }));
    render(<DSHInstallationGuide onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByRole("img", { name: copy.imageAlt })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: copy.continue }));
    expect(screen.queryByRole("img", { name: copy.imageAlt })).not.toBeInTheDocument();
    await act(async () => finish(configured));
    expect(screen.getByRole("img", { name: copy.imageAlt })).toBeInTheDocument();
  });

  it("keeps the first step active when installation detects a running DSH", async () => {
    const quit: DesktopAgentIntegrationStatus = { ...configured, state: "action_required",
      issue: { code: "quit_required" }, action: { kind: "quit_agent" } };
    const connect = vi.fn().mockResolvedValue(quit);
    render(<DSHInstallationGuide onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: copy.continue }));
    await screen.findByText(zhCN.agentIntegration.quitRequired.title.replace("{{agent}}", "DeepSeek Harness"));
    expect(screen.getByText(copy.prepareStep).closest(".ant-steps-item")).toHaveClass("ant-steps-item-process");
    expect(screen.queryByRole("img", { name: copy.imageAlt })).not.toBeInTheDocument();
    expect(screen.queryByText(copy.installedTitle)).not.toBeInTheDocument();
  });

  it.each([configured, { ...configured, connection: undefined }])("does not treat configuration as a live connection", async (status) => {
    mocks.statuses.mockResolvedValue({ ok: true, data: { "deepseek-harness": status } });
    const connect = vi.fn();
    render(<DSHInstallationGuide status={status} onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getByText(copy.installedTitle)).toBeInTheDocument();
    expect(screen.queryByText(copy.successTitle)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: copy.check }));
    await screen.findByText(copy.notReadyTitle);
    expect(screen.queryByText(copy.successTitle)).not.toBeInTheDocument();
    expect(connect).not.toHaveBeenCalled();
  });

  it.each(["mcp_enabled", "mcp_trusted", "control_ready", "mcp_configured"] as const)("requires verified %s before completing", async (field) => {
    const current = { ...connected, connection: { ...connected.connection!, [field]: false } };
    mocks.statuses.mockResolvedValue({ ok: true, data: { "deepseek-harness": current } });
    render(<DSHInstallationGuide status={configured} onConnect={vi.fn()} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: copy.check }));
    await screen.findByText(copy.notReadyTitle);
    expect(screen.queryByText(copy.successTitle)).not.toBeInTheDocument();
  });

  it("recovers on a late authenticated plugin heartbeat without reinstalling", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const connect = vi.fn(), update = vi.fn();
    mocks.statuses.mockResolvedValue({ ok: true, data: { "deepseek-harness": configured } });
    render(<DSHInstallationGuide status={configured} onConnect={connect} onStatus={update} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: copy.check }));
    await screen.findByText(copy.waitingDescription);
    mocks.statuses.mockResolvedValue({ ok: true, data: { "deepseek-harness": connected } });
    await act(async () => { fireEvent.focus(window); });
    await screen.findByText(copy.successTitle);
    expect(update).toHaveBeenCalledWith(connected);
    expect(connect).not.toHaveBeenCalled();
  });

  it("shows a safe actionable failure, allows rechecking, and never reinstalls automatically", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const connect = vi.fn();
    const failed = { ...configured, issue: { code: "mcp_enable_required", operation_id: "dsh-check-test" },
      message: "private native output", connection: { ...configured.connection!, runtime_state: "unavailable" as const } };
    mocks.statuses.mockResolvedValue({ ok: true, data: { "deepseek-harness": failed } });
    render(<DSHInstallationGuide status={failed} onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: copy.check }));
    await screen.findByText(zhCN.agentIntegration.dshErrors.mcp_enable_required);
    expect(screen.getByText(/dsh-check-test/)).toBeInTheDocument();
    expect(screen.queryByText(/private native/)).not.toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(3100); });
    expect(connect).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: copy.reconnect }));
    expect(screen.getByRole("button", { name: copy.continue })).toBeInTheDocument();
    expect(connect).not.toHaveBeenCalled();
  });

  it("reports an unavailable bridge without replacing the last known configuration", async () => {
    mocks.statuses.mockResolvedValue({ ok: false, reason: "unavailable" });
    const update = vi.fn();
    render(<DSHInstallationGuide status={configured} onConnect={vi.fn()} onStatus={update} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: copy.check }));
    await screen.findByText(copy.checkFailedDescription);
    expect(update).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: copy.retryCheck })).toBeEnabled();
  });

  it.each(["reconnect", "unmount"] as const)("ignores an old background check after %s", async (action) => {
    let finish!: (value: unknown) => void;
    mocks.statuses.mockResolvedValueOnce({ ok: true, data: { "deepseek-harness": configured } })
      .mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    const update = vi.fn(), connect = vi.fn().mockResolvedValue(configured);
    const view = render(<DSHInstallationGuide status={configured} onConnect={connect} onStatus={update} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: copy.check }));
    await screen.findByText(copy.notReadyTitle);
    fireEvent.focus(window);
    await waitFor(() => expect(mocks.statuses).toHaveBeenCalledTimes(2));
    const previousUpdates = update.mock.calls.length;
    if (action === "unmount") view.unmount();
    else fireEvent.click(screen.getByRole("button", { name: copy.reconnect }));
    await act(async () => finish({ ok: true, data: { "deepseek-harness": connected } }));
    expect(update).toHaveBeenCalledTimes(previousUpdates);
    expect(screen.queryByText(copy.successTitle)).not.toBeInTheDocument();
    if (action === "reconnect") {
      fireEvent.click(screen.getByRole("button", { name: copy.continue }));
      await screen.findByText(copy.installedTitle);
      expect(connect).toHaveBeenCalledTimes(1);
    }
  });

  it("preserves a pending disconnect instead of offering installation or automatic checks", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const disconnect: DesktopAgentIntegrationStatus = { ...configured, state: "action_required", retry_action: "disconnect",
      issue: { code: "quit_required" }, action: { kind: "quit_agent" } };
    const connect = vi.fn();
    render(<DSHInstallationGuide status={disconnect} onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getByText(copy.disconnectPendingDescription)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: copy.retryInstall })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: copy.check })).not.toBeInTheDocument();
    await act(async () => { fireEvent.focus(window); await vi.advanceTimersByTimeAsync(10000); });
    expect(connect).not.toHaveBeenCalled();
    expect(mocks.statuses).not.toHaveBeenCalled();
  });

  it("waits for an explicit retry after a quit request", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const quit: DesktopAgentIntegrationStatus = { ...configured, state: "action_required",
      issue: { code: "quit_required" }, action: { kind: "quit_agent" } };
    const connect = vi.fn().mockResolvedValue(configured);
    render(<DSHInstallationGuide status={quit} onConnect={connect} onStatus={vi.fn()} onClose={vi.fn()} />);
    await act(async () => { fireEvent.focus(window); await vi.advanceTimersByTimeAsync(10000); });
    expect(mocks.statuses).not.toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: copy.retryInstall }));
    await waitFor(() => expect(screen.getByText(copy.installedTitle)).toBeInTheDocument());
    expect(connect).toHaveBeenCalledTimes(1);
  });
});
