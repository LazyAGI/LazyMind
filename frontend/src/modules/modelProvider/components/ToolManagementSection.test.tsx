import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const toolApiMocks = vi.hoisted(() => ({
  checkMcpServer: vi.fn(),
  createMcpServer: vi.fn(),
  deleteMcpServer: vi.fn(),
  disableTool: vi.fn(),
  discoverMcpServerTools: vi.fn(),
  enableTool: vi.fn(),
  listMcpServersPage: vi.fn(),
  listToolAssetsPage: vi.fn(),
  updateMcpServer: vi.fn(),
  updateMcpServerTools: vi.fn(),
}));

vi.mock("@/modules/memory/toolApi", () => toolApiMocks);

import ToolManagementSection, { ManagedToolSummary } from "./ToolManagementSection";

const mcpTools = [
  { id: "mst_local_search", name: "remote_search", description: "Search remotely" },
  { id: "mst_local_fetch", name: "remote_fetch", description: "Fetch remotely" },
];

const mcpServer = {
  id: "mcp_server_1",
  name: "Remote MCP",
  url: "https://example.com/mcp",
  transport: "http",
  timeout: 30,
  enabled: true,
  isVerified: true,
  share: false,
  toolCount: mcpTools.length,
  tools: mcpTools,
  allowedTools: [] as string[],
  apiKeyPreview: "***",
  createTime: "",
  updateTime: "",
};

const mockSummarySize = (overflowing: boolean) => {
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("model-provider-service-summary") ? 36 : 0;
  });
  vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(function (this: HTMLElement) {
    if (!this.classList.contains("model-provider-service-summary")) return 0;
    return overflowing ? 72 : 36;
  });
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("model-provider-service-summary") ? 240 : 0;
  });
  vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("model-provider-service-summary") ? 240 : 0;
  });
};

describe("ManagedToolSummary", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does not add a tooltip or focus stop when the summary is fully visible", async () => {
    mockSummarySize(false);
    const { container } = render(
      <ManagedToolSummary
        fallback="暂无数据"
        primary="简短简介"
        secondary="仅应在溢出气泡中出现的补充介绍"
      />,
    );

    const trigger = container.querySelector(".model-provider-service-summary-wrap");
    expect(trigger).not.toHaveAttribute("tabindex");
    fireEvent.mouseEnter(trigger as Element);

    await waitFor(() => {
      expect(screen.queryByText("仅应在溢出气泡中出现的补充介绍")).not.toBeInTheDocument();
    });
  });

  it("shows the full description below an actually truncated summary", async () => {
    mockSummarySize(true);
    const { container } = render(
      <ManagedToolSummary
        fallback="暂无数据"
        primary="被截断的简介"
        secondary="完整功能介绍"
      />,
    );

    const trigger = container.querySelector(".model-provider-service-summary-wrap");
    await waitFor(() => expect(trigger).toHaveAttribute("tabindex", "0"));
    fireEvent.mouseEnter(trigger as Element);

    expect(await screen.findByText(/完整功能介绍/)).toBeInTheDocument();
    expect(document.querySelector(".ant-tooltip-placement-bottomLeft")).toBeInTheDocument();
  });
});

describe("ToolManagementSection MCP overview synchronization", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    const getComputedStyle = window.getComputedStyle.bind(window);
    vi.spyOn(window, "getComputedStyle").mockImplementation((element) =>
      getComputedStyle(element),
    );
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
    toolApiMocks.listMcpServersPage.mockResolvedValue({
      records: [mcpServer],
      total: 1,
    });
    toolApiMocks.discoverMcpServerTools.mockResolvedValue({
      success: true,
      tools: mcpTools,
    });
    toolApiMocks.checkMcpServer.mockResolvedValue({
      success: true,
      message: "ok",
      toolCount: mcpTools.length,
    });
    toolApiMocks.updateMcpServerTools.mockResolvedValue(mcpServer);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("submits remote tool names and refreshes the overview after authorization", async () => {
    const onChanged = vi.fn().mockResolvedValue(undefined);
    render(
      <ToolManagementSection
        layout="settings"
        onChanged={onChanged}
        view="mcp"
      />,
    );

    expect(await screen.findByText("Remote MCP")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /发现工具|Discover Tools/ }));

    await waitFor(() => {
      expect(toolApiMocks.discoverMcpServerTools).toHaveBeenCalledWith("mcp_server_1");
      expect(onChanged).toHaveBeenCalledTimes(1);
    });
    onChanged.mockClear();

    fireEvent.click(screen.getByRole("checkbox", { name: /允许全部工具|Allow all tools/ }));
    const saveButtons = screen.getAllByRole("button", { name: /保\s*存|Save/ });
    fireEvent.click(saveButtons[saveButtons.length - 1]);

    await waitFor(() => {
      expect(toolApiMocks.updateMcpServerTools).toHaveBeenCalledWith(
        "mcp_server_1",
        ["remote_search", "remote_fetch"],
      );
      expect(onChanged).toHaveBeenCalledTimes(1);
    });
  });

  it("refreshes the overview after a successful connection check", async () => {
    const onChanged = vi.fn().mockResolvedValue(undefined);
    render(
      <ToolManagementSection
        layout="settings"
        onChanged={onChanged}
        view="mcp"
      />,
    );

    expect(await screen.findByText("Remote MCP")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /重新连接|Reconnect/ }));

    await waitFor(() => {
      expect(toolApiMocks.checkMcpServer).toHaveBeenCalledWith("mcp_server_1");
      expect(onChanged).toHaveBeenCalledTimes(1);
    });
  });
});
