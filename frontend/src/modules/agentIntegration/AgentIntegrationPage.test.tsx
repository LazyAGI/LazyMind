import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
  getDesktopPlatform: () => "win32",
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ConversationSettingsApi: () => ({ listChatExecutors: mocks.executors }),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, options?: { defaultValue?: string; agent?: string }) => ({
      "common.refresh": "刷新",
      "agentIntegration.title": "外部 Agent 集成",
      "agentIntegration.description": "集成说明",
      "agentIntegration.mcpTitle": "外部 Agent 使用 LazyMind",
      "agentIntegration.mcpDescription": "MCP 说明",
      "agentIntegration.executorTitle": "LazyMind 使用外部 Agent",
      "agentIntegration.executorDescription": "执行器说明",
      "agentIntegration.states.ready": "可以启用",
      "agentIntegration.states.action_required": "等待外部确认",
      "agentIntegration.states.requirements_missing": "缺少前置条件",
      "agentIntegration.enable": "启用",
      "agentIntegration.disable": "停用",
      "agentIntegration.login": "登录",
      "agentIntegration.continueInAgent": `前往 ${options?.agent || "Agent"} 完成`,
      "agentIntegration.executorAvailable": "可使用",
      "agentIntegration.executorConnecting": "正在连接",
      "agentIntegration.executorNotInstalled": "未安装",
      "agentIntegration.executorLoginRequired": "需要登录",
      "agentIntegration.executorDisabled": "已停用",
      "agentIntegration.executorEnableSuccess": `已允许 LazyMind 使用 ${options?.agent || "Agent"}`,
      "agentIntegration.executorDisableSuccess": `已停止 LazyMind 使用 ${options?.agent || "Agent"}`,
      "agentIntegration.locateApplication": "定位桌面应用",
      "agentIntegration.locateCLI": "定位 CLI",
      "agentIntegration.restoreAutoDetection": "恢复自动检测",
      "agentIntegration.executableBindingSaved": "已保存本机程序路径",
      "agentIntegration.executableBindingCleared": "已恢复自动检测",
      "agentIntegration.notSupported": "不支持",
      "agentIntegration.executorRequirements.codex": "需要 Codex CLI",
      "agentIntegration.executorRequirements.cursor": "需要 Cursor Agent CLI",
      "agentIntegration.executorRequirements.workbuddy": "需要 CodeBuddy Code CLI",
      "agentIntegration.executorRequirements.raccoon": "Raccoon 不支持执行器",
      "agentIntegration.executorRequirements.traework": "TRAE 不支持执行器",
      "agentIntegration.executorRequirements.deepseek-harness": "DSH 不支持执行器",
    }[key] ?? options?.defaultValue ?? key),
  }),
}));

describe("AgentIntegrationPage", () => {
  beforeEach(() => {
    mocks.statuses.mockReset();
    mocks.action.mockReset();
    mocks.executors.mockReset();
    mocks.executorPolicies.mockReset();
    mocks.executorAction.mockReset();
    mocks.bindings.mockReset();
    mocks.bind.mockReset();
    mocks.clearBinding.mockReset();
    mocks.selectExecutable.mockReset();
    mocks.statuses.mockResolvedValue({
      ok: true,
      data: {
        cursor: {
          agent: "cursor",
          display_name: "Cursor",
          state: "ready",
          requirements: [{ id: "cursor_desktop", description: "Cursor Desktop ready", satisfied: true }],
        },
      },
    });
    mocks.executors.mockResolvedValue({ data: { data: { executors: [] } } });
    mocks.executorPolicies.mockResolvedValue({
      ok: true,
      data: {
        codex: { provider: "codex", enabled: true },
        cursor: { provider: "cursor", enabled: true },
        workbuddy: { provider: "workbuddy", enabled: true },
      },
    });
    mocks.bindings.mockResolvedValue({ ok: true, data: {} });
  });

  it("loads status without triggering an integration action", async () => {
    render(<AgentIntegrationPage />);

    expect(await screen.findByText("外部 Agent 使用 LazyMind")).toBeInTheDocument();
    expect(screen.getByText("LazyMind 使用外部 Agent")).toBeInTheDocument();
    expect(screen.getByText("Cursor Desktop ready")).toBeInTheDocument();
    expect(mocks.action).not.toHaveBeenCalled();
  });

  it("opens the explicit Cursor confirmation step only after enable", async () => {
    mocks.action.mockResolvedValue({
      ok: true,
      data: {
        agent: "cursor",
        display_name: "Cursor",
        state: "action_required",
        action: { kind: "open_url", url: "cursor://anysphere.cursor-deeplink/mcp/install?test=1" },
      },
    });
    render(<AgentIntegrationPage />);

    const cursorRequirement = await screen.findByText("Cursor Desktop ready");
    const cursorCard = cursorRequirement.closest(".ant-card");
    expect(cursorCard).not.toBeNull();
    fireEvent.click(within(cursorCard as HTMLElement).getByRole("button", { name: /启用/ }));
    await waitFor(() => expect(mocks.action).toHaveBeenCalledWith("cursor", "connect"));
    expect(await screen.findByRole("link", { name: /前往 Cursor 完成/ })).toHaveAttribute(
      "href",
      "cursor://anysphere.cursor-deeplink/mcp/install?test=1",
    );
  });

  it("names CodeBuddy separately and marks unsupported executors", async () => {
    render(<AgentIntegrationPage />);

    expect(await screen.findByText("CodeBuddy Code")).toBeInTheDocument();
    expect(screen.getByText("需要 CodeBuddy Code CLI")).toBeInTheDocument();
    for (const name of ["Raccoon", "TRAE Work", "DeepSeek Harness"]) {
      const headings = await screen.findAllByRole("heading", { name });
      const card = headings.at(-1)?.closest(".ant-card");
      expect(card).not.toBeNull();
      expect(within(card as HTMLElement).getByText("不支持")).toBeInTheDocument();
    }
  });

  it("waits for the local Host report instead of showing an installed CLI as missing", async () => {
    const offline = {
      id: "codex", display_name: "Codex CLI", kind: "external",
      installed: false, host_online: false, available: false,
      unavailable_reason: "LazyMind Agent Host is offline",
    };
    mocks.executors
      .mockResolvedValueOnce({ data: { data: { executors: [offline] } } })
      .mockResolvedValueOnce({ data: { data: { executors: [{
        ...offline, installed: true, host_online: true, available: true, unavailable_reason: "",
      }] } } });

    render(<AgentIntegrationPage />);

    const codexCard = (await screen.findByRole("heading", { name: "Codex CLI" })).closest(".ant-card");
    expect(codexCard).not.toBeNull();
    await waitFor(() => {
      expect(within(codexCard as HTMLElement).getByText("可使用")).toBeInTheDocument();
      expect(mocks.executors).toHaveBeenCalledTimes(2);
    });
  });

  it("distinguishes an installed signed-out CLI from a missing CLI", async () => {
    mocks.executors.mockResolvedValue({ data: { data: { executors: [{
      id: "cursor", display_name: "Cursor Agent CLI", kind: "external",
      installed: true, host_online: true, available: false,
      unavailable_reason: "Cursor Agent CLI is not signed in",
    }] } } });

    render(<AgentIntegrationPage />);

    const cursorCard = (await screen.findByRole("heading", { name: "Cursor Agent CLI" })).closest(".ant-card");
    expect(cursorCard).not.toBeNull();
    expect(within(cursorCard as HTMLElement).getByText("需要登录")).toBeInTheDocument();
    expect(within(cursorCard as HTMLElement).queryByText("未安装")).not.toBeInTheDocument();
  });

  it("stops LazyMind executor access without changing the external login", async () => {
    mocks.executors.mockResolvedValue({ data: { data: { executors: [{
      id: "codex", display_name: "Codex CLI", kind: "external",
      installed: true, host_online: true, available: true, unavailable_reason: "",
    }] } } });
    mocks.executorPolicies
      .mockResolvedValueOnce({ ok: true, data: { codex: { provider: "codex", enabled: true } } })
      .mockResolvedValue({ ok: true, data: { codex: { provider: "codex", enabled: false } } });
    mocks.executorAction.mockResolvedValue({
      ok: true,
      data: { provider: "codex", enabled: false },
    });

    render(<AgentIntegrationPage />);

    const codexCard = (await screen.findByRole("heading", { name: "Codex CLI" })).closest(".ant-card");
    expect(codexCard).not.toBeNull();
    fireEvent.click(within(codexCard as HTMLElement).getByRole("button", { name: /停用/ }));
    await waitFor(() => expect(mocks.executorAction).toHaveBeenCalledWith("codex", "disable"));
    await waitFor(() => expect(within(codexCard as HTMLElement).getByText("已停用")).toBeInTheDocument());
    expect(within(codexCard as HTMLElement).getByRole("button", { name: /启用/ })).toBeInTheDocument();
  });

  it("binds a CLI installed outside automatically discovered locations", async () => {
    const missing = {
      id: "codex", display_name: "Codex CLI", kind: "external",
      installed: false, host_online: true, available: false,
      unavailable_reason: "Codex CLI is not installed",
    };
    mocks.executors.mockResolvedValue({ data: { data: { executors: [missing] } } });
    mocks.selectExecutable.mockResolvedValue("D:\\Agents\\codex.cmd");
    mocks.bind.mockResolvedValue({
      ok: true,
      data: { target: "codex-cli", configured: true, path: "D:\\Agents\\codex.cmd" },
    });
    mocks.bindings
      .mockResolvedValueOnce({ ok: true, data: {} })
      .mockResolvedValue({ ok: true, data: { "codex-cli": "D:\\Agents\\codex.cmd" } });

    render(<AgentIntegrationPage />);

    const codexCard = (await screen.findByRole("heading", { name: "Codex CLI" })).closest(".ant-card");
    expect(codexCard).not.toBeNull();
    fireEvent.click(within(codexCard as HTMLElement).getByRole("button", { name: /定位 CLI/ }));
    await waitFor(() => expect(mocks.bind).toHaveBeenCalledWith("codex-cli", "D:\\Agents\\codex.cmd"));
    expect(mocks.selectExecutable).toHaveBeenCalledWith("codex-cli");
    expect(await within(codexCard as HTMLElement).findByRole("button", { name: "恢复自动检测" })).toBeInTheDocument();
  });

  it("does not offer application location when only initialization is missing", async () => {
    mocks.statuses.mockResolvedValue({
      ok: true,
      data: {
        cursor: {
          agent: "cursor",
          display_name: "Cursor",
          state: "requirements_missing",
          requirements: [
            { id: "cursor_desktop", description: "Cursor installed", satisfied: true },
            { id: "cursor_desktop_initialized", description: "Cursor initialized", satisfied: false },
          ],
        },
      },
    });

    render(<AgentIntegrationPage />);

    const cursorCard = (await screen.findByRole("heading", { name: "Cursor" })).closest(".ant-card");
    expect(cursorCard).not.toBeNull();
    expect(within(cursorCard as HTMLElement).queryByRole("button", { name: "定位桌面应用" })).not.toBeInTheDocument();
  });
});
