import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import LocalDataSourcePage from "./LocalDataSourcePage";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const useLocalDataSourceSettingsMock = vi.fn();

vi.mock("../hooks/useLocalDataSourceSettings", () => ({
  useLocalDataSourceSettings: () => useLocalDataSourceSettingsMock(),
}));

function buildVm(overrides: Record<string, unknown> = {}) {
  return {
    t,
    loading: false,
    canCreateLocalSource: true,
    localChatSources: [],
    chatSettingsLoading: false,
    chatSettingsLoadFailed: false,
    chatSettingsSaving: false,
    chatSettingsModalOpen: false,
    selectedBindingIds: [],
    setSelectedBindingIds: vi.fn(),
    setChatSettingsModalOpen: vi.fn(),
    handleOpenChatSettings: vi.fn(),
    handleRetryChatSettings: vi.fn(),
    handleSaveChatSettings: vi.fn(),
    ...overrides,
  };
}

describe("LocalDataSourcePage", () => {
  it("renders the local detail title and empty state when there are no sources", () => {
    useLocalDataSourceSettingsMock.mockReturnValue(buildVm());
    renderWithProviders(<LocalDataSourcePage />);
    expect(
      screen.getByText("modelProvider.cloudDocuments.localDetailTitle"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("modelProvider.cloudDocuments.localChatDirectoriesEmpty"),
    ).toBeInTheDocument();
  });

  it("renders a directory tree when chat-enabled local bindings exist", () => {
    useLocalDataSourceSettingsMock.mockReturnValue(
      buildVm({
        localChatSources: [
          {
            source_id: "src-1",
            name: "Docs Source",
            bindings: [
              {
                binding_id: "bind-1",
                target_ref: "/data/docs",
                chat_enabled: true,
                connector_type: "local_fs",
                target_type: "local_path",
              },
            ],
          },
        ],
      }),
    );
    const { container } = renderWithProviders(<LocalDataSourcePage />);
    expect(
      container.querySelector(".model-provider-local-chat-directory-overview"),
    ).not.toBeNull();
  });

  it("shows an error alert with retry when chat settings fail to load", () => {
    const handleRetryChatSettings = vi.fn();
    useLocalDataSourceSettingsMock.mockReturnValue(
      buildVm({ chatSettingsLoadFailed: true, handleRetryChatSettings }),
    );
    renderWithProviders(<LocalDataSourcePage />);
    expect(
      screen.getByText("modelProvider.cloudDocuments.localChatDirectoriesLoadFailed"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByText("common.retry"));
    expect(handleRetryChatSettings).toHaveBeenCalledTimes(1);
  });

  it("calls handleOpenChatSettings when clicking the configure button", () => {
    const handleOpenChatSettings = vi.fn();
    useLocalDataSourceSettingsMock.mockReturnValue(buildVm({ handleOpenChatSettings }));
    renderWithProviders(<LocalDataSourcePage />);
    fireEvent.click(
      screen.getByText("modelProvider.cloudDocuments.localChatDirectoriesConfigure"),
    );
    expect(handleOpenChatSettings).toHaveBeenCalledTimes(1);
  });

  it("opens the configuration modal with a tree of bindable directories", () => {
    useLocalDataSourceSettingsMock.mockReturnValue(
      buildVm({
        chatSettingsModalOpen: true,
        localChatSources: [
          {
            source_id: "src-1",
            name: "Docs Source",
            bindings: [
              {
                binding_id: "bind-1",
                target_ref: "/data/docs",
                chat_enabled: false,
                connector_type: "local_fs",
                target_type: "local_path",
              },
            ],
          },
        ],
      }),
    );
    renderWithProviders(<LocalDataSourcePage />);
    expect(
      screen.getByText("modelProvider.cloudDocuments.localChatDirectoriesModalTitle"),
    ).toBeInTheDocument();
    expect(screen.getByText("Docs Source")).toBeInTheDocument();
  });

  it("navigates back via the breadcrumb without throwing", () => {
    useLocalDataSourceSettingsMock.mockReturnValue(buildVm());
    renderWithProviders(<LocalDataSourcePage />);
    const backButton = screen.getByText("modelProvider.cloudDocuments.backToProviders");
    expect(() => fireEvent.click(backButton)).not.toThrow();
  });
});
