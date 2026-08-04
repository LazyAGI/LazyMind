import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { message } from "antd";
import { dataSourceScanApi } from "@/modules/dataSource/api/clients";
import { useLocalDataSourceSettings } from "./useLocalDataSourceSettings";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("antd", () => ({
  message: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "admin" }) },
}));

vi.mock("@/modules/dataSource/utils/role", () => ({
  isAdminRole: (role?: string) => role === "admin",
}));

vi.mock("@/modules/dataSource/utils/scanAccessors", () => ({
  inferSourceKind: (item: { source_options?: { source_type?: string } }) =>
    item.source_options?.source_type === "local" ? "local" : "cloud",
}));

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceScanApi: {
    listSources: vi.fn(),
    listBindingChatSettings: vi.fn(),
    updateBindingChatSetting: vi.fn(),
  },
}));

const mockedListSources = dataSourceScanApi.listSources as unknown as ReturnType<typeof vi.fn>;
const mockedListBindingChatSettings = dataSourceScanApi.listBindingChatSettings as unknown as ReturnType<
  typeof vi.fn
>;
const mockedUpdateBindingChatSetting = dataSourceScanApi.updateBindingChatSetting as unknown as ReturnType<
  typeof vi.fn
>;

beforeEach(() => {
  mockedListSources.mockReset();
  mockedListBindingChatSettings.mockReset();
  mockedUpdateBindingChatSetting.mockReset();
  (message.error as ReturnType<typeof vi.fn>).mockReset();
  (message.success as ReturnType<typeof vi.fn>).mockReset();

  mockedListSources.mockResolvedValue({
    data: { items: [{ source_options: { source_type: "local" } }, { source_options: {} }] },
  });
  mockedListBindingChatSettings.mockResolvedValue({
    data: {
      sources: [
        {
          id: "src-1",
          bindings: [
            {
              binding_id: "b1",
              connector_type: "local_fs",
              target_type: "local_path",
              chat_enabled: true,
            },
            {
              binding_id: "b2",
              connector_type: "feishu",
              target_type: "wiki",
              chat_enabled: false,
            },
          ],
        },
      ],
    },
  });
});

describe("useLocalDataSourceSettings", () => {
  it("loads the local source count and filters chat sources to local-only bindings", async () => {
    const { result } = renderHook(() => useLocalDataSourceSettings());

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.localSourceCount).toBe(1);
    expect(result.current.localChatSources).toHaveLength(1);
    expect(result.current.localChatSources[0].bindings).toEqual([
      expect.objectContaining({ binding_id: "b1" }),
    ]);
    expect(result.current.canCreateLocalSource).toBe(true);
  });

  it("opens the chat settings modal and preselects currently-enabled bindings", async () => {
    const { result } = renderHook(() => useLocalDataSourceSettings());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.handleOpenChatSettings();
    });

    expect(result.current.chatSettingsModalOpen).toBe(true);
    expect(result.current.selectedBindingIds).toEqual(["b1"]);
  });

  it("skips saving and closes the modal when no binding selection changed", async () => {
    const { result } = renderHook(() => useLocalDataSourceSettings());
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.setSelectedBindingIds(["b1"]);
      result.current.setChatSettingsModalOpen(true);
    });

    await act(async () => {
      await result.current.handleSaveChatSettings();
    });

    expect(mockedUpdateBindingChatSetting).not.toHaveBeenCalled();
    expect(result.current.chatSettingsModalOpen).toBe(false);
  });

  it("only calls updateBindingChatSetting for bindings whose enabled state changed", async () => {
    const { result } = renderHook(() => useLocalDataSourceSettings());
    await waitFor(() => expect(result.current.loading).toBe(false));

    mockedUpdateBindingChatSetting.mockResolvedValue({});
    mockedListBindingChatSettings.mockResolvedValue({
      data: {
        sources: [
          {
            id: "src-1",
            bindings: [
              { binding_id: "b1", connector_type: "local_fs", target_type: "local_path", chat_enabled: true },
            ],
          },
        ],
      },
    });

    act(() => {
      result.current.setSelectedBindingIds([]);
    });

    await act(async () => {
      await result.current.handleSaveChatSettings();
    });

    expect(mockedUpdateBindingChatSetting).toHaveBeenCalledWith({
      bindingId: "b1",
      updateBindingChatSettingRequest: { chat_enabled: false },
    });
  });

  it("marks chat settings load as failed when the request rejects", async () => {
    mockedListBindingChatSettings.mockRejectedValueOnce(new Error("network"));
    const { result } = renderHook(() => useLocalDataSourceSettings());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.chatSettingsLoadFailed).toBe(true);
  });
});
