import { beforeEach, describe, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import {
  checkMcpServer,
  createMcpServer,
  disableTool,
  discoverMcpServerTools,
  enableTool,
  listMcpServersPage,
  listToolAssetsPage,
  updateMcpServer,
  updateMcpServerTools,
} from "./toolApi";

const apiFactoryMocks = vi.hoisted(() => ({
  enableTool: vi.fn(),
  disableTool: vi.fn(),
  createMcpServer: vi.fn(),
  updateMcpServer: vi.fn(),
  deleteMcpServer: vi.fn(),
  checkMcpServer: vi.fn(),
  discoverMcpServer: vi.fn(),
  updateMcpServerTools: vi.fn(),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: { get: vi.fn(), post: vi.fn() },
  BASE_URL: "",
}));

vi.mock("@/api/generated/core-client", () => ({
  Configuration: class {},
  ToolsApiFactory: () => ({
    apiCoreToolsToolNameEnablePost: apiFactoryMocks.enableTool,
    apiCoreToolsToolNameDisablePost: apiFactoryMocks.disableTool,
  }),
  McpServersApiFactory: () => ({
    apiCoreMcpServersPost: apiFactoryMocks.createMcpServer,
    apiCoreMcpServersIdPatch: apiFactoryMocks.updateMcpServer,
    apiCoreMcpServersIdDelete: apiFactoryMocks.deleteMcpServer,
    apiCoreMcpServersIdCheckPost: apiFactoryMocks.checkMcpServer,
    apiCoreMcpServersIdDiscoverPost: apiFactoryMocks.discoverMcpServer,
    apiCoreMcpServersIdToolsPut: apiFactoryMocks.updateMcpServerTools,
  }),
}));

const mockedGet = axiosInstance.get as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockedGet.mockReset();
  Object.values(apiFactoryMocks).forEach((mockFn) => mockFn.mockReset());
});

describe("listToolAssetsPage", () => {
  it("normalizes tool groups into structured assets", async () => {
    mockedGet.mockResolvedValue({
      data: {
        tool_groups: [
          {
            name: "search",
            label: "搜索工具",
            description: "desc",
            disabled: false,
            can_disable: true,
            methods: [{ summary: "查询" }],
          },
        ],
        total: 1,
      },
    });

    const result = await listToolAssetsPage({ keyword: "search" });

    expect(mockedGet).toHaveBeenCalledWith(
      expect.stringContaining("/tools"),
      expect.objectContaining({ params: { keyword: "search" } }),
    );
    expect(result.records).toEqual([
      {
        id: "search",
        name: "搜索工具",
        description: "desc",
        category: "",
        tags: [],
        content: "查询",
        isEnabled: true,
        readonly: false,
      },
    ]);
    expect(result.total).toBe(1);
  });

  it("omits the keyword param when empty", async () => {
    mockedGet.mockResolvedValue({ data: { tool_groups: [] } });
    await listToolAssetsPage({});
    expect(mockedGet).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ params: {} }));
  });
});

describe("enableTool / disableTool", () => {
  it("calls the enable endpoint with tool name", async () => {
    apiFactoryMocks.enableTool.mockResolvedValue({});
    await enableTool("search");
    expect(apiFactoryMocks.enableTool).toHaveBeenCalledWith({ toolName: "search" });
  });

  it("calls the disable endpoint with tool name", async () => {
    apiFactoryMocks.disableTool.mockResolvedValue({});
    await disableTool("search");
    expect(apiFactoryMocks.disableTool).toHaveBeenCalledWith({ toolName: "search" });
  });
});

describe("listMcpServersPage", () => {
  it("normalizes server records including nested tools", async () => {
    mockedGet.mockResolvedValue({
      data: {
        mcp_servers: [
          {
            id: "srv-1",
            name: "server1",
            url: "http://x",
            transport: "http",
            timeout: 10,
            enabled: true,
            is_verified: true,
            share: false,
            tool_count: 1,
            tools: [{ id: "t1", tool_name: "tool1", description: "d" }],
          },
        ],
        total: 1,
      },
    });

    const result = await listMcpServersPage();
    expect(result.records[0].tools).toEqual([{ id: "t1", name: "tool1", description: "d" }]);
    expect(result.total).toBe(1);
  });
});

describe("createMcpServer / updateMcpServer", () => {
  it("normalizes streamable_http transport to http on create", async () => {
    apiFactoryMocks.createMcpServer.mockResolvedValue({
      data: { id: "srv-1", name: "server1", tools: [] },
    });

    await createMcpServer({
      name: "server1",
      url: "http://x",
      transport: "streamable_http",
      apiKey: "key",
      timeout: 10,
      enabled: true,
    });

    expect(apiFactoryMocks.createMcpServer).toHaveBeenCalledWith(
      expect.objectContaining({
        createServerRequest: expect.objectContaining({ transport: "http" }),
      }),
    );
  });

  it("only includes api_key in update payload when non-empty", async () => {
    apiFactoryMocks.updateMcpServer.mockResolvedValue({ data: { id: "srv-1", tools: [] } });

    await updateMcpServer("srv-1", {
      name: "server1",
      url: "http://x",
      transport: "http",
      apiKey: "  ",
      timeout: 10,
      enabled: true,
    });

    const payload = apiFactoryMocks.updateMcpServer.mock.calls[0][0].updateServerRequest;
    expect(payload.api_key).toBeUndefined();
  });
});

describe("checkMcpServer / discoverMcpServerTools", () => {
  it("returns a normalized check result", async () => {
    apiFactoryMocks.checkMcpServer.mockResolvedValue({
      data: { success: true, message: "ok", tool_count: 3 },
    });
    const result = await checkMcpServer("srv-1");
    expect(result).toEqual({ success: true, message: "ok", toolCount: 3 });
  });

  it("filters out tools without an id when discovering", async () => {
    apiFactoryMocks.discoverMcpServer.mockResolvedValue({
      data: { success: true, tools: [{ id: "", tool_name: "" }, { id: "t1", tool_name: "tool1" }] },
    });
    const result = await discoverMcpServerTools("srv-1");
    expect(result.tools).toEqual([{ id: "t1", name: "tool1", description: "" }]);
  });
});

describe("updateMcpServerTools", () => {
  it("passes the allowed tools list through to the API", async () => {
    apiFactoryMocks.updateMcpServerTools.mockResolvedValue({ data: { id: "srv-1", tools: [] } });
    await updateMcpServerTools("srv-1", ["t1", "t2"]);
    expect(apiFactoryMocks.updateMcpServerTools).toHaveBeenCalledWith({
      id: "srv-1",
      updateToolsRequest: { allowed_tools: ["t1", "t2"] },
    });
  });
});
