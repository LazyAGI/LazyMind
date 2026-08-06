import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import DataSourceSyncPickerModal, {
  type DataSourceSyncPickerModalProps,
} from "./DataSourceSyncPickerModal";

const t = (key: string) => key;

function makeProps(
  overrides: Partial<DataSourceSyncPickerModalProps> = {},
): DataSourceSyncPickerModalProps {
  return {
    t,
    open: true,
    syncSubmitting: false,
    selectedCount: 0,
    syncKeyword: "",
    setSyncKeyword: vi.fn(),
    hasFilteredSelected: false,
    filteredSyncNodeKeys: [],
    setSyncSelectedDocIds: vi.fn(),
    syncTreeLoading: false,
    syncTreeData: [],
    checkedTreeKeys: [],
    selectableSyncFileKeys: new Set<string>(),
    onCancel: vi.fn(),
    onOk: vi.fn(),
    ...overrides,
  };
}

describe("DataSourceSyncPickerModal", () => {
  it("shows an empty state when there is no tree data and not loading", () => {
    renderWithProviders(<DataSourceSyncPickerModal {...makeProps()} />);
    expect(screen.getByText("admin.dataSourceDetailNoMatchedFile")).toBeInTheDocument();
  });

  it("shows a loading indicator while the tree is loading", () => {
    renderWithProviders(
      <DataSourceSyncPickerModal {...makeProps({ syncTreeLoading: true })} />,
    );
    expect(screen.getByText("admin.dataSourceDetailTreeLoading")).toBeInTheDocument();
  });

  it("renders tree data when available", () => {
    renderWithProviders(
      <DataSourceSyncPickerModal
        {...makeProps({
          syncTreeData: [{ key: "file-1", title: "file-1.pdf", isLeaf: true }],
        })}
      />,
    );
    expect(screen.getByText("file-1.pdf")).toBeInTheDocument();
  });

  it("disables the ok button when nothing is selected", () => {
    renderWithProviders(<DataSourceSyncPickerModal {...makeProps({ selectedCount: 0 })} />);
    const okButton = screen.getByText(
      "admin.dataSourceDetailStartPull",
    ).closest("button");
    expect(okButton).toBeDisabled();
  });

  it("enables the ok button once items are selected and calls onOk when clicked", () => {
    const onOk = vi.fn();
    renderWithProviders(
      <DataSourceSyncPickerModal {...makeProps({ selectedCount: 2, onOk })} />,
    );
    const okButton = screen.getByText("admin.dataSourceDetailStartPull").closest("button");
    expect(okButton).not.toBeDisabled();
    fireEvent.click(okButton as HTMLElement);
    expect(onOk).toHaveBeenCalled();
  });

  it("updates the search keyword when typing in the filter input", () => {
    const setSyncKeyword = vi.fn();
    renderWithProviders(
      <DataSourceSyncPickerModal {...makeProps({ setSyncKeyword })} />,
    );
    const input = screen.getByPlaceholderText(
      "admin.dataSourceDetailSearchInModalPlaceholder",
    );
    fireEvent.change(input, { target: { value: "report" } });
    expect(setSyncKeyword).toHaveBeenCalledWith("report");
  });

  it("shows a select-all action when nothing is filtered-selected, and selects all filtered keys", () => {
    const setSyncSelectedDocIds = vi.fn();
    renderWithProviders(
      <DataSourceSyncPickerModal
        {...makeProps({
          hasFilteredSelected: false,
          filteredSyncNodeKeys: ["a", "b"],
          setSyncSelectedDocIds,
        })}
      />,
    );
    const selectAllButton = screen.getByText("chat.selectAll");
    fireEvent.click(selectAllButton);
    expect(setSyncSelectedDocIds).toHaveBeenCalledWith(["a", "b"]);
  });

  it("shows a cancel-select-all action when some filtered items are selected", () => {
    renderWithProviders(
      <DataSourceSyncPickerModal
        {...makeProps({ hasFilteredSelected: true, filteredSyncNodeKeys: ["a"] })}
      />,
    );
    expect(screen.getByText("chat.cancelSelectAll")).toBeInTheDocument();
  });
});
