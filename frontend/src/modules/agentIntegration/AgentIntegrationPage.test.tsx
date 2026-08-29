import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import AgentIntegrationPage from "./AgentIntegrationPage";

const mocks = vi.hoisted(() => ({
  statuses: vi.fn(),
  action: vi.fn(),
  executors: vi.fn(),
  executorPolicies: vi.fn(),
  executorAction: vi.fn(),
  bindings: vi.fn(),
  bind: vi.fn(),
  clearBinding: vi.fn(),
  selectExecutable: vi.fn(),
  platform: vi.fn(),
}));

vi.mock("@/runtime/desktopBridge", () => ({
  agentIntegrationStatuses: mocks.statuses,
  agentIntegrationAction: mocks.action,
  executorIntegrationPolicies: mocks.executorPolicies,
  executorIntegrationAction: mocks.executorAction,
  agentExecutableBindings: mocks.bindings,
  bindAgentExecutable: mocks.bind,
  clearAgentExecutable: mocks.clearBinding,
  selectExecutable: mocks.selectExecutable,
  getDesktopPlatform: mocks.platform,
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ConversationSettingsApi: () => ({ listChatExecutors: mocks.executors }),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, options?: { defaultValue?: string; agent?: string }) => {
      const agent = options?.agent || "Agent";
      const values: Record<string, string> = {
        "common.refresh": "刷新",
        "common.save": "保存",
        "common.cancel": "取消",
        "agentIntegration.title": "外部 Agent 集成",
        "agentIntegration.mergedDescription": "双向集成说明",
        "agentIntegration.installed": "已安装",
        "agentIntegration.notInstalled": "未安装",
        "agentIntegration.detectedSummary": `已检测到 ${agent}`,
        "agentIntegration.singleDirectionSummary": `${agent} 已检测，仅支持接入 LazyMind MCP`,
        "agentIntegration.missingSummary": `尚未检测到 ${agent}`,
        "agentIntegration.waitingForDetection": "等待检测结果",
        "agentIntegration.viewInstallGuide": "查看安装指南",
        "agentIntegration.viewExecutorGuide": "查看执行器安装指南",
        "agentIntegration.mcpModeTitle": `${agent} 使用 LazyMind MCP`,
        "agentIntegration.mcpModeDescription": "调用 LazyMind 能力",
        "agentIntegration.executorModeTitle": `LazyMind 调用 ${agent}`,
        "agentIntegration.executorModeDescription": "调用外部 Agent 能力",
        "agentIntegration.mcpGuideTitle": `配置教程：让 ${agent} 使用 LazyMind`,
        "agentIntegration.executorGuideTitle": `配置教程：让 LazyMind 调用 ${agent}`,
        "agentIntegration.clientStageTitle": `${agent} 客户端准备`,
        "agentIntegration.executorStageTitle": `${agent} 执行器准备`,
        "agentIntegration.integrationStageTitle": "集成方式",
        "agentIntegration.stageReady": "配置已完成",
        "agentIntegration.stageActionRequired": "尚有配置待完成",
        "agentIntegration.chooseIntegrationMode": "完成对应配置后可启用",
        "agentIntegration.executorAccountReady": "执行器账号已登录",
        "agentIntegration.completeConfigurationHint": "请先完成对应流程的配置",
        "agentIntegration.notEnabled": "未启用",
        "agentIntegration.enabled": "已启用",
        "agentIntegration.awaitingConfirmation": "等待确认",
        "agentIntegration.configurationIncomplete": "配置未完成",
        "agentIntegration.configurationIssue": "配置异常",
        "agentIntegration.sessionPrivacyNotice": `启用后，LazyMind 会读取 ${agent} 的本机会话信息；关闭后停止读取。`,
        "agentIntegration.guideFooter": "完成后重新检测",
        "agentIntegration.checkAgain": "重新检测",
        "agentIntegration.login": "登录",
        "agentIntegration.continueInAgent": `前往 ${agent} 完成`,
        "agentIntegration.executorDetectionReady": "本机 Agent 检测服务已就绪",
        "agentIntegration.executorConnecting": "正在连接",
        "agentIntegration.executorLoginRequired": "需要登录",
        "agentIntegration.executorStatusCheckFailed": "登录状态检测失败",
        "agentIntegration.mcpClients.codex": "Codex CLI",
        "agentIntegration.mcpClients.cursor": "Cursor 桌面版",
        "agentIntegration.mcpClients.workbuddy": "WorkBuddy 桌面版",
        "agentIntegration.mcpClients.raccoon": "商汤小浣熊桌面版",
        "agentIntegration.mcpClients.traework": "TRAE Work 桌面版",
        "agentIntegration.mcpClients.deepseek-harness": "DeepSeek Harness Web",
        "agentIntegration.requirements.workbuddy_desktop.missing": "WorkBuddy 桌面版未安装",
        "agentIntegration.requirements.workbuddy_desktop_initialized.missing": "WorkBuddy 桌面版尚未完成首次启动",
        "agentIntegration.locateApplication": "定位桌面应用",
        "agentIntegration.locateCLI": "定位 CLI",
        "agentIntegration.enterExecutablePath": "输入本机路径",
        "agentIntegration.executablePathTitle": "配置本机程序路径",
        "agentIntegration.executablePathDescription": "输入运行 Docker 的主机上的完整可执行文件路径",
        "agentIntegration.executablePathPlaceholder": "例如 C:\\Program Files\\Cursor\\Cursor.exe",
        "agentIntegration.restoreAutoDetection": "恢复自动检测",
        "agentIntegration.operationFailed": "操作未完成",
        "agentIntegration.loginStarted": `已打开 ${agent} 登录流程`,
        "agentIntegration.enableSuccess": `${agent} 已配置 MCP`,
        "agentIntegration.disconnectSuccess": `已移除 ${agent} MCP`,
        "agentIntegration.executorEnableSuccess": `已允许 LazyMind 使用 ${agent}`,
        "agentIntegration.executorDisableSuccess": `已停止 LazyMind 使用 ${agent}`,
        "agentIntegration.executableBindingSaved": "已保存本机程序路径",
        "agentIntegration.executableBindingCleared": "已恢复自动检测",
        "agentIntegration.guides.codex.mcp.install": "执行 `codex --version`",
        "agentIntegration.guides.codex.mcp.connect": "开启 MCP 开关",
        "agentIntegration.guides.codex.mcp.verify": "在 Codex 中验证工具",
        "agentIntegration.guides.codex.executor.install": "安装 Codex CLI",
        "agentIntegration.guides.codex.executor.login": "执行 codex login",
        "agentIntegration.guides.codex.executor.enable": "开启执行器开关",
      };
      return values[key] ?? options?.defaultValue ?? key;
    },
  }),
}));

const readyCodexStatus = {
  agent: "codex",
  display_name: "Codex",
  state: "ready",
  requirements: [
    { id: "codex_cli", description: "Codex CLI installed", satisfied: true },
    { id: "codex_login", description: "Codex signed in", satisfied: true },
  ],
};

const readyCodexExecutor = {
  id: "codex",
  display_name: "Codex CLI",
  kind: "external",
  installed: true,
  host_online: true,
  available: false,
  unavailable_reason: "Disabled in LazyMind settings",
};

function expandAgent(id: string) {
  const panel = screen.getByTestId(`agent-panel-${id}`);
  const header = panel.querySelector<HTMLButtonElement>(".agent-integration-card-header");
  expect(header).not.toBeNull();
  if (header?.getAttribute("aria-expanded") === "false") fireEvent.click(header);
  return panel;
}

describe("AgentIntegrationPage", () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset());
    mocks.platform.mockReturnValue("win32");
    mocks.statuses.mockResolvedValue({ ok: true, data: { codex: readyCodexStatus } });
    mocks.executors.mockResolvedValue({ data: { data: { executors: [readyCodexExecutor] } } });
    mocks.executorPolicies.mockResolvedValue({
      ok: true,
      data: {
        codex: { provider: "codex", enabled: false, installed: true, ready: true },
        cursor: { provider: "cursor", enabled: false, installed: false, ready: false },
        workbuddy: { provider: "workbuddy", enabled: false, installed: false, ready: false },
      },
    });
    mocks.bindings.mockResolvedValue({ ok: true, data: {} });
  });

  it("keeps Agent rows compact and expands one three-stage configuration flow at a time", async () => {
    render(<AgentIntegrationPage />);

    expect(await screen.findByText("外部 Agent 集成")).toBeInTheDocument();
    expect(screen.getAllByTestId(/^agent-panel-/)).toHaveLength(6);
    const codex = screen.getByTestId("agent-panel-codex");
    expect(codex.querySelectorAll(".agent-integration-stage")).toHaveLength(3);
    expect(codex.querySelector(".agent-integration-capability")).toBeNull();

    const cursor = expandAgent("cursor");
    expect(cursor.querySelectorAll(".agent-integration-stage")).toHaveLength(3);
    expect(codex.querySelector(".agent-integration-card-detail")).toBeNull();
  });

  it("renders two integration switches and unlocks each only after its prerequisites are ready", async () => {
    mocks.statuses.mockResolvedValue({
      ok: true,
      data: {
        cursor: {
          agent: "cursor",
          display_name: "Cursor",
          state: "requirements_missing",
          requirements: [
            { id: "cursor_desktop", description: "Cursor installed", satisfied: false },
          ],
        },
      },
    });
    mocks.executors.mockResolvedValue({ data: { data: { executors: [{
      id: "cursor",
      display_name: "Cursor Agent CLI",
      kind: "external",
      installed: true,
      host_online: true,
      available: false,
      unavailable_reason: "Disabled in LazyMind settings",
    }] } } });
    mocks.executorPolicies.mockResolvedValue({
      ok: true,
      data: { cursor: {
        provider: "cursor", enabled: false, installed: true, ready: false,
        unavailable_reason: "Cursor Agent CLI is not signed in",
      } },
    });

    render(<AgentIntegrationPage />);

    await screen.findByText("外部 Agent 集成");
    const cursor = expandAgent("cursor");
    expect(within(cursor).getByRole("switch", { name: "Cursor 桌面版 使用 LazyMind MCP" })).toBeDisabled();
    expect(within(cursor).getByRole("switch", { name: "LazyMind 调用 Cursor Agent CLI" })).toBeDisabled();
    expect(within(cursor).getByRole("button", { name: /登录/ })).toBeEnabled();
  });

  it("does not mislabel a Cursor status probe failure as a sign-in requirement", async () => {
    mocks.executorPolicies.mockResolvedValue({
      ok: true,
      data: { cursor: {
        provider: "cursor", enabled: false, installed: true, ready: false,
        unavailable_reason: "Cursor Agent CLI status check failed",
      } },
    });
    mocks.executors.mockResolvedValue({ data: { data: { executors: [{
      id: "cursor", display_name: "Cursor Agent CLI", kind: "external",
      installed: true, host_online: true, available: false, unavailable_reason: "status check failed",
    }] } } });

    render(<AgentIntegrationPage />);

    await screen.findByText("外部 Agent 集成");
    const cursor = expandAgent("cursor");
    expect(within(cursor).getByText("登录状态检测失败")).toBeInTheDocument();
    expect(within(cursor).queryByRole("button", { name: /登录/ })).not.toBeInTheDocument();
  });

  it("renders stale WorkBuddy state as unmet when the app is not installed", async () => {
    mocks.statuses.mockResolvedValue({
      ok: true,
      data: { workbuddy: {
        agent: "workbuddy",
        display_name: "WorkBuddy",
        state: "requirements_missing",
        requirements: [
          { id: "workbuddy_desktop", description: "Install WorkBuddy", satisfied: false },
          { id: "workbuddy_desktop_initialized", description: "Open WorkBuddy once", satisfied: false },
        ],
      } },
    });

    render(<AgentIntegrationPage />);

    await screen.findByText("外部 Agent 集成");
    const workbuddy = expandAgent("workbuddy");
    expect(within(workbuddy).getByText("WorkBuddy 桌面版未安装")).toBeInTheDocument();
    expect(within(workbuddy).getByText("WorkBuddy 桌面版尚未完成首次启动")).toBeInTheDocument();
  });

  it("moves detailed setup instructions and privacy disclosure behind contextual help", async () => {
    render(<AgentIntegrationPage />);

    const codex = await screen.findByTestId("agent-panel-codex");
    expect(screen.queryByText("codex --version")).not.toBeInTheDocument();

    const helpButtons = within(codex).getAllByRole("button", {
      name: "配置教程：让 Codex CLI 使用 LazyMind help",
    });
    fireEvent.click(helpButtons[0]);
    expect(await screen.findByText("codex --version")).toBeInTheDocument();
    expect(document.querySelector(".agent-integration-help-content")?.textContent).not.toContain("`");

    const executorHelp = within(codex).getAllByRole("button", {
      name: "配置教程：让 LazyMind 调用 Codex CLI help",
    });
    fireEvent.click(executorHelp[0]);
    expect(await screen.findByText(/启用后，LazyMind 会读取 Codex CLI 的本机会话信息/)).toBeInTheDocument();
  });

  it("enables MCP and executor actions from the two switches when configuration is complete", async () => {
    mocks.action.mockResolvedValue({
      ok: true,
      data: { ...readyCodexStatus, state: "enabled" },
    });
    mocks.executorAction.mockResolvedValue({
      ok: true,
      data: { provider: "codex", enabled: true, installed: true, ready: true },
    });

    render(<AgentIntegrationPage />);

    const codex = await screen.findByTestId("agent-panel-codex");
    fireEvent.click(within(codex).getByRole("switch", { name: "Codex CLI 使用 LazyMind MCP" }));
    await waitFor(() => expect(mocks.action).toHaveBeenCalledWith("codex", "connect"));

    fireEvent.click(within(codex).getByRole("switch", { name: "LazyMind 调用 Codex CLI" }));
    await waitFor(() => expect(mocks.executorAction).toHaveBeenCalledWith("codex", "enable"));
  });

  it("shows only the supported direction for a single-direction Agent", async () => {
    mocks.statuses.mockResolvedValue({
      ok: true,
      data: { raccoon: {
        agent: "raccoon",
        display_name: "Raccoon",
        state: "ready",
        requirements: [{ id: "raccoon_desktop", description: "Raccoon installed", satisfied: true }],
      } },
    });
    render(<AgentIntegrationPage />);

    await screen.findByText("外部 Agent 集成");
    const raccoon = expandAgent("raccoon");
    expect(within(raccoon).getByText("商汤小浣熊桌面版 已检测，仅支持接入 LazyMind MCP")).toBeInTheDocument();
    expect(raccoon.querySelectorAll(".agent-integration-stage")).toHaveLength(2);
    expect(within(raccoon).getByRole("switch", { name: "商汤小浣熊桌面版 使用 LazyMind MCP" })).toBeEnabled();
    expect(within(raccoon).queryByRole("switch", { name: /LazyMind 调用/ })).not.toBeInTheDocument();
  });

  it("keeps Cursor confirmation as an explicit follow-up action", async () => {
    mocks.statuses.mockResolvedValue({
      ok: true,
      data: {
        cursor: {
          agent: "cursor",
          display_name: "Cursor",
          state: "ready",
          requirements: [
            { id: "cursor_desktop", description: "Cursor installed", satisfied: true },
          ],
        },
      },
    });
    mocks.action.mockResolvedValue({
      ok: true,
      data: {
        agent: "cursor",
        display_name: "Cursor",
        state: "action_required",
        requirements: [{ id: "cursor_desktop", description: "Cursor installed", satisfied: true }],
        action: { kind: "open_url", url: "cursor://install-lazymind" },
      },
    });

    render(<AgentIntegrationPage />);

    await screen.findByText("外部 Agent 集成");
    const cursor = expandAgent("cursor");
    fireEvent.click(within(cursor).getByRole("switch", { name: "Cursor 桌面版 使用 LazyMind MCP" }));
    expect(await within(cursor).findByRole("link", { name: /前往 Cursor 完成/ })).toHaveAttribute(
      "href",
      "cursor://install-lazymind",
    );
  });

  it("rechecks Cursor after returning from its external configuration flow", async () => {
    const ready = {
      agent: "cursor",
      display_name: "Cursor",
      state: "ready",
      requirements: [{ id: "cursor_desktop", description: "Cursor installed", satisfied: true }],
    };
    mocks.statuses.mockResolvedValue({ ok: true, data: { cursor: ready } });
    mocks.action.mockResolvedValue({
      ok: true,
      data: {
        ...ready,
        state: "action_required",
        action: { kind: "open_url", url: "cursor://install-lazymind" },
      },
    });

    render(<AgentIntegrationPage />);

    await screen.findByText("外部 Agent 集成");
    const cursor = expandAgent("cursor");
    fireEvent.click(within(cursor).getByRole("switch", { name: "Cursor 桌面版 使用 LazyMind MCP" }));
    const continueLink = await within(cursor).findByRole("link", { name: /前往 Cursor 完成/ });
    fireEvent.click(continueLink);

    mocks.statuses.mockResolvedValue({ ok: true, data: { cursor: { ...ready, state: "enabled" } } });
    act(() => window.dispatchEvent(new Event("focus")));

    await waitFor(() => expect(
      within(cursor).getByRole("switch", { name: "Cursor 桌面版 使用 LazyMind MCP" }),
    ).toBeChecked());
  });

  it("supports explicit CLI location without making it a separate permanent panel", async () => {
    mocks.executors.mockResolvedValue({ data: { data: { executors: [{
      ...readyCodexExecutor,
      installed: false,
      host_online: true,
      unavailable_reason: "Codex CLI is not installed",
    }] } } });
    mocks.executorPolicies.mockResolvedValue({
      ok: true,
      data: { codex: { provider: "codex", enabled: false, installed: false, ready: false } },
    });
    mocks.selectExecutable.mockResolvedValue("D:\\Agents\\codex.cmd");
    mocks.bind.mockResolvedValue({
      ok: true,
      data: { target: "codex-cli", configured: true, path: "D:\\Agents\\codex.cmd" },
    });

    render(<AgentIntegrationPage />);

    const codex = await screen.findByTestId("agent-panel-codex");
    fireEvent.click(within(codex).getByRole("button", { name: /定位 CLI/ }));
    await waitFor(() => expect(mocks.bind).toHaveBeenCalledWith("codex-cli", "D:\\Agents\\codex.cmd"));
  });

  it("accepts a validated host path from the Docker browser on Windows", async () => {
    mocks.platform.mockReturnValue(null);
    mocks.executors.mockResolvedValue({ data: { data: { executors: [{
      ...readyCodexExecutor,
      installed: false,
      unavailable_reason: "Codex CLI is not installed",
    }] } } });
    mocks.executorPolicies.mockResolvedValue({
      ok: true,
      data: { codex: { provider: "codex", enabled: false, installed: false, ready: false } },
    });
    mocks.bind.mockResolvedValue({
      ok: true,
      data: { target: "codex-cli", configured: true, path: "D:\\Agents\\codex.cmd" },
    });

    render(<AgentIntegrationPage />);

    const codex = await screen.findByTestId("agent-panel-codex");
    fireEvent.click(await within(codex).findByRole("button", { name: /输入本机路径/ }));
    fireEvent.change(within(screen.getByRole("dialog")).getByRole("textbox"), {
      target: { value: "D:\\Agents\\codex.cmd" },
    });
    fireEvent.click(screen.getByRole("button", { name: /保\s*存/ }));

    await waitFor(() => expect(mocks.bind).toHaveBeenCalledWith("codex-cli", "D:\\Agents\\codex.cmd"));
  });

  it("polls a stale local Host result until the ready report arrives", async () => {
    const stale = {
      ...readyCodexExecutor,
      available: false,
      unavailable_reason: "Codex CLI is not signed in",
    };
    mocks.executors
      .mockResolvedValueOnce({ data: { data: { executors: [stale] } } })
      .mockResolvedValueOnce({ data: { data: { executors: [{ ...stale, available: true, unavailable_reason: "" }] } } });
    mocks.executorPolicies.mockResolvedValue({
      ok: true,
      data: { codex: { provider: "codex", enabled: true, installed: true, ready: true } },
    });

    render(<AgentIntegrationPage />);

    const codex = await screen.findByTestId("agent-panel-codex");
    await waitFor(() => {
      expect(within(codex).getByRole("switch", { name: "LazyMind 调用 Codex CLI" })).toBeChecked();
      expect(mocks.executors).toHaveBeenCalledTimes(2);
    });
  });
});
