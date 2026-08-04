import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import GlossaryListSection from "./GlossaryListSection";
import type { GlossaryAsset } from "../shared";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const makeAsset = (overrides: Partial<GlossaryAsset> = {}): GlossaryAsset => ({
  id: "asset-1",
  term: "术语A",
  aliases: [],
  content: "内容A",
  group: "",
  source: "user",
  ...overrides,
} as GlossaryAsset);

const baseProps = {
  t,
  assets: [] as GlossaryAsset[],
  columns: [
    { title: "Term", dataIndex: "term", key: "term" },
  ],
  filteredItems: [] as GlossaryAsset[],
  glossaryListPage: 1,
  glossaryListPageSize: 8,
  glossaryListTotal: 0,
  glossaryLoadError: "",
  glossaryLoading: false,
  glossarySource: undefined,
  handleBatchDeleteGlossary: vi.fn(),
  handleBatchMergeGlossary: vi.fn(),
  query: "",
  refreshGlossaryAssets: vi.fn(),
  selectedGlossaryAssetIds: [] as string[],
  selectedGlossaryAssets: [] as GlossaryAsset[],
  setGlossaryListPage: vi.fn(),
  setGlossaryListPageSize: vi.fn(),
  setSelectedGlossaryAssetIds: vi.fn(),
};

describe("GlossaryListSection", () => {
  it("renders table rows for the filtered glossary items", () => {
    render(
      <GlossaryListSection
        {...baseProps}
        filteredItems={[makeAsset()]}
      />,
    );
    expect(screen.getByText("术语A")).toBeInTheDocument();
  });

  it("disables batch action buttons when nothing is selected", () => {
    render(<GlossaryListSection {...baseProps} />);
    expect(screen.getByText("admin.memoryGlossaryBatchMerge").closest("button")).toBeDisabled();
    expect(screen.getByText("admin.memoryGlossaryBatchDelete").closest("button")).toBeDisabled();
  });

  it("enables and triggers batch action callbacks when assets are selected", () => {
    const handleBatchMergeGlossary = vi.fn();
    const handleBatchDeleteGlossary = vi.fn();
    render(
      <GlossaryListSection
        {...baseProps}
        selectedGlossaryAssets={[makeAsset()]}
        handleBatchMergeGlossary={handleBatchMergeGlossary}
        handleBatchDeleteGlossary={handleBatchDeleteGlossary}
      />,
    );
    fireEvent.click(screen.getByText("admin.memoryGlossaryBatchMerge"));
    fireEvent.click(screen.getByText("admin.memoryGlossaryBatchDelete"));
    expect(handleBatchMergeGlossary).toHaveBeenCalledTimes(1);
    expect(handleBatchDeleteGlossary).toHaveBeenCalledTimes(1);
  });

  it("shows the load error alert with a retry action", () => {
    const refreshGlossaryAssets = vi.fn();
    render(
      <GlossaryListSection
        {...baseProps}
        glossaryLoadError="load failed"
        refreshGlossaryAssets={refreshGlossaryAssets}
      />,
    );
    expect(screen.getByText("load failed")).toBeInTheDocument();
    fireEvent.click(screen.getByText("common.retry"));
    expect(refreshGlossaryAssets).toHaveBeenCalledWith(
      expect.objectContaining({ page: 1, pageSize: 8 }),
    );
  });
});
