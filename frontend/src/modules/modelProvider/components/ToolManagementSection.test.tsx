import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import ToolManagementSection from "./ToolManagementSection";
import {
  checkMcpServer,
  createMcpServer,
  deleteMcpServer,
  disableTool,
  discoverMcpServerTools,
  enableTool,
  listMcpServersPage,
  listToolAssetsPage,
  updateMcpServer,
  updateMcpServerTools,
} from "@/modules/memory/toolApi";

vi.mock("@/modules/memory/toolApi", () => ({
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

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code?: string) => code || "",
}));

const listToolAssetsPageMock = listToolAssetsPage as unknown as ReturnType<typeof vi.fn>;
const listMcpServersPageMock = listMcpServersPage as unknown as ReturnType<typeof vi.fn>;
const enableToolMock = enableTool as unknown as ReturnType<typeof vi.fn>;
const disableToolMock = disableTool as unknown as ReturnType<typeof vi.fn>;
const createMcpServerMock = createMcpServer as unknown as ReturnType<typeof vi.fn>;
const updateMcpServerMock = updateMcpServer as unknown as ReturnType<typeof vi.fn>;
const deleteMcpServerMock = deleteMcpServer as unknown as ReturnType<typeof vi.fn>;
const checkMcpServerMock = checkMcpServer as unknown as ReturnType<typeof vi.fn>;
const discoverMcpServerToolsMock = discoverMcpServerTools as unknown as ReturnType<typeof vi.fn>;
const updateMcpServerToolsMock = updateMcpServerTools as unknown as ReturnType<typeof vi.fn>;

const builtinTool = {
  id: "tool-1",
  content: "",
  name: "Web Search",
  description: "Search the web",
  category: "search",
  tags: [],
  isEnabled: true,
  readonly: false,
};

const mcpServer = {
  id: "mcp-1",
  name: "My MCP",
  url: "https://example.com/mcp",
  transport: "sse",
  timeout: 30,
  enabled: true,
  isVerified: true,
  share: false,
  toolCount: 2,
  tools: [
    { id: "t1", name: "Tool One", description: "First tool" },
    { id: "t2", name: "Tool Two", description: "Second tool" },
  ],
  allowedTools: ["t1"],
  apiKeyPreview: "sk-***",
  createTime: "",
  updateTime: "",
};

describe("ToolManagementSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listToolAssetsPageMock.mockResolvedValue({ records: [builtinTool], total: 1 });
    listMcpServersPageMock.mockResolvedValue({ records: [mcpServer], total: 1 });
  });

  it("renders builtin tools and toggles enabled state", async () => {
    renderWithProviders(<ToolManagementSection view="builtin" />);
    await waitFor(() => {
      expect(screen.getByText("Web Search")).toBeInTheDocument();
    });
    disableToolMock.mockResolvedValue({});
    listToolAssetsPageMock.mockResolvedValue({
      records: [{ ...builtinTool, isEnabled: false }],
      total: 1,
    });
    const switchButton = screen.getByRole("switch");
    fireEvent.click(switchButton);
    await waitFor(() => {
      expect(disableToolMock).toHaveBeenCalledWith("tool-1");
    });
  });

  it("shows an empty state when there are no builtin tools", async () => {
    listToolAssetsPageMock.mockResolvedValue({ records: [], total: 0 });
    renderWithProviders(<ToolManagementSection view="builtin" />);
    await waitFor(() => {
      expect(screen.getByText("admin.memoryEmpty")).toBeInTheDocument();
    });
  });

  it("filters tools using the search input", async () => {
    renderWithProviders(<ToolManagementSection view="builtin" />);
    await waitFor(() => {
      expect(listToolAssetsPageMock).toHaveBeenCalled();
    });
    const input = screen.getByPlaceholderText(
      "modelProvider.external.toolSearchPlaceholder",
    );
    fireEvent.change(input, { target: { value: "search" } });
    await waitFor(() => {
      expect(listToolAssetsPageMock).toHaveBeenCalledWith({ keyword: "search" });
    });
  });

  it("renders mcp servers and toggles enabled state", async () => {
    renderWithProviders(<ToolManagementSection view="mcp" />);
    await waitFor(() => {
      expect(screen.getByText("My MCP")).toBeInTheDocument();
    });
    updateMcpServerMock.mockResolvedValue({});
    const switchButton = screen.getByRole("switch");
    fireEvent.click(switchButton);
    await waitFor(() => {
      expect(updateMcpServerMock).toHaveBeenCalledWith(
        "mcp-1",
        expect.objectContaining({ enabled: false }),
      );
    });
  });

  it("checks an mcp server and shows the discovered tool count", async () => {
    checkMcpServerMock.mockResolvedValue({ success: true, message: "", toolCount: 3 });
    renderWithProviders(<ToolManagementSection view="mcp" />);
    await waitFor(() => {
      expect(screen.getByText("My MCP")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("admin.memoryMcpCheck"));
    await waitFor(() => {
      expect(checkMcpServerMock).toHaveBeenCalledWith("mcp-1");
    });
  });

  it("discovers mcp server tools and opens the tools drawer", async () => {
    discoverMcpServerToolsMock.mockResolvedValue({
      success: true,
      tools: [{ id: "t3", name: "Tool Three", description: "" }],
    });
    renderWithProviders(<ToolManagementSection view="mcp" />);
    await waitFor(() => {
      expect(screen.getByText("My MCP")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("admin.memoryMcpDiscover"));
    await waitFor(() => {
      expect(discoverMcpServerToolsMock).toHaveBeenCalledWith("mcp-1");
    });
    await waitFor(() => {
      expect(screen.getByText("Tool Three")).toBeInTheDocument();
    });
  });

  it("deletes an mcp server after confirming", async () => {
    deleteMcpServerMock.mockResolvedValue({});
    renderWithProviders(<ToolManagementSection view="mcp" />);
    await waitFor(() => {
      expect(screen.getByText("My MCP")).toBeInTheDocument();
    });
    fireEvent.click(screen.getAllByText("common.delete")[0]);
    const confirmButton = await screen.findByText("common.delete", {
      selector: ".ant-popconfirm-buttons .ant-btn-dangerous span",
    });
    fireEvent.click(confirmButton);
    await waitFor(() => {
      expect(deleteMcpServerMock).toHaveBeenCalledWith("mcp-1");
    });
  });

  it("opens the create modal and creates a new mcp server", async () => {
    createMcpServerMock.mockResolvedValue({});
    renderWithProviders(<ToolManagementSection view="mcp" />);
    await waitFor(() => {
      expect(screen.getByText("My MCP")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("admin.memoryMcpCreateButton"));
    const nameInput = await screen.findByPlaceholderText("admin.memoryMcpNamePlaceholder");
    fireEvent.change(nameInput, { target: { value: "New Server" } });
    const urlInput = screen.getByPlaceholderText("https://example.com/mcp");
    fireEvent.change(urlInput, { target: { value: "https://new.example.com/mcp" } });
    const apiKeyInput = screen.getByPlaceholderText("admin.memoryMcpApiKeyPlaceholder");
    fireEvent.change(apiKeyInput, { target: { value: "secret-key" } });
    fireEvent.click(screen.getByText("common.save"));
    await waitFor(() => {
      expect(createMcpServerMock).toHaveBeenCalledWith(
        expect.objectContaining({ name: "New Server", url: "https://new.example.com/mcp" }),
      );
    });
  });

  it("saves selected tools from the tools drawer", async () => {
    updateMcpServerToolsMock.mockResolvedValue(mcpServer);
    renderWithProviders(<ToolManagementSection view="mcp" />);
    await waitFor(() => {
      expect(screen.getByText("My MCP")).toBeInTheDocument();
    });
    discoverMcpServerToolsMock.mockResolvedValue({ success: true, tools: mcpServer.tools });
    fireEvent.click(screen.getByText("admin.memoryMcpDiscover"));
    await waitFor(() => {
      expect(discoverMcpServerToolsMock).toHaveBeenCalled();
    });
    const drawerSave = await screen.findAllByText("common.save");
    fireEvent.click(drawerSave[drawerSave.length - 1]);
    await waitFor(() => {
      expect(updateMcpServerToolsMock).toHaveBeenCalledWith("mcp-1", ["t1"]);
    });
  });
});
