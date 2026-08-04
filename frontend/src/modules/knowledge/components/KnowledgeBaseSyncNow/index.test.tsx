import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import KnowledgeBaseSyncNow from "./index";

const { resolveScanSourceIdByDatasetId, openSyncPicker, useScanSourceSyncFlow } =
  vi.hoisted(() => ({
    resolveScanSourceIdByDatasetId: vi.fn(),
    openSyncPicker: vi.fn(),
    useScanSourceSyncFlow: vi.fn(),
  }));

vi.mock(
  "@/modules/dataSource/utils/resolveScanSourceIdByDatasetId",
  () => ({ resolveScanSourceIdByDatasetId }),
);

vi.mock("@/modules/dataSource/hooks/useScanSourceSyncFlow", () => ({
  useScanSourceSyncFlow: (...args: unknown[]) => useScanSourceSyncFlow(...args),
}));

vi.mock("@/modules/dataSource/common/components/DataSourceSyncPickerModal", () => ({
  default: ({ open }: { open: boolean }) =>
    open ? <div data-testid="sync-picker-modal" /> : null,
}));

function mockSyncFlow(overrides: Record<string, unknown> = {}) {
  useScanSourceSyncFlow.mockReturnValue({
    detailLoading: false,
    syncSubmitting: false,
    openSyncPicker,
    syncPickerOpen: false,
    syncSelectedDocIds: [],
    syncKeyword: "",
    setSyncKeyword: vi.fn(),
    hasFilteredSelected: false,
    filteredSyncNodeKeys: [],
    setSyncSelectedDocIds: vi.fn(),
    syncTreeLoading: false,
    syncTreeData: [],
    selectableSyncFileKeys: [],
    loadSyncTreeChildren: vi.fn(),
    syncTreeRequestSeqRef: { current: 0 },
    syncTreeInitialLoadRef: { current: false },
    setSyncPickerOpen: vi.fn(),
    confirmSync: vi.fn(),
    ...overrides,
  });
}

describe("KnowledgeBaseSyncNow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockSyncFlow();
  });

  it("renders nothing while no source id can be resolved and resolving finished", async () => {
    resolveScanSourceIdByDatasetId.mockResolvedValue("");
    const { container } = renderWithProviders(
      <KnowledgeBaseSyncNow datasetId="ds-1" />,
    );
    await waitFor(() => {
      expect(container.querySelector("button")).not.toBeInTheDocument();
    });
  });

  it("renders the sync now button once a source id is resolved", async () => {
    resolveScanSourceIdByDatasetId.mockResolvedValue("source-1");
    renderWithProviders(<KnowledgeBaseSyncNow datasetId="ds-1" />);
    // Ant Design's loading icon can briefly attach an aria-label of "loading" while
    // its exit animation runs, so match on a substring rather than the exact name.
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: /admin\.dataSourceDetailSyncNow/ }),
      ).toBeInTheDocument();
    });
  });

  it("opens the sync picker when the button is clicked", async () => {
    resolveScanSourceIdByDatasetId.mockResolvedValue("source-1");
    renderWithProviders(<KnowledgeBaseSyncNow datasetId="ds-1" />);
    const button = await screen.findByRole("button", {
      name: /admin\.dataSourceDetailSyncNow/,
    });
    fireEvent.click(button);
    expect(openSyncPicker).toHaveBeenCalledTimes(1);
  });

  it("disables the button while the source id is still resolving", () => {
    resolveScanSourceIdByDatasetId.mockReturnValue(new Promise(() => {}));
    renderWithProviders(<KnowledgeBaseSyncNow datasetId="ds-1" />);
    expect(
      screen.getByRole("button", { name: /admin\.dataSourceDetailSyncNow/ }),
    ).toBeDisabled();
  });

  it("renders the sync picker modal when syncPickerOpen is true", async () => {
    resolveScanSourceIdByDatasetId.mockResolvedValue("source-1");
    mockSyncFlow({ syncPickerOpen: true });
    renderWithProviders(<KnowledgeBaseSyncNow datasetId="ds-1" />);
    await waitFor(() => {
      expect(screen.getByTestId("sync-picker-modal")).toBeInTheDocument();
    });
  });
});
