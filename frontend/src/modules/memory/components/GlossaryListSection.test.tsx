import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import GlossaryListSection from "./GlossaryListSection";

describe("GlossaryListSection", () => {
  it("keeps create and inbox actions in the list toolbar", () => {
    const onCreate = vi.fn();
    const onOpenInbox = vi.fn();
    render(
      <GlossaryListSection
        t={(key) => key}
        assets={[]}
        columns={[{ key: "term", title: "Term", dataIndex: "term" }]}
        filteredItems={[]}
        glossaryListPage={1}
        glossaryListPageSize={20}
        glossaryListTotal={0}
        glossaryLoadError=""
        glossaryLoading={false}
        availableGlossarySourceOptions={[{ value: "user", label: "User" }]}
        searchInput=""
        query=""
        inboxCount={2}
        showInbox
        onSearchInputChange={vi.fn()}
        onSearch={vi.fn()}
        onSourceChange={vi.fn()}
        onCreate={onCreate}
        onOpenInbox={onOpenInbox}
        handleBatchDeleteGlossary={vi.fn()}
        handleBatchMergeGlossary={vi.fn()}
        refreshGlossaryAssets={vi.fn()}
        selectedGlossaryAssetIds={[]}
        selectedGlossaryAssets={[]}
        setGlossaryListPage={vi.fn()}
        setGlossaryListPageSize={vi.fn()}
        setSelectedGlossaryAssetIds={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /memoryCreateGlossaryButton/ }));
    fireEvent.click(screen.getByRole("button", { name: /memoryGlossaryInboxButton/ }));
    expect(onCreate).toHaveBeenCalledTimes(1);
    expect(onOpenInbox).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("heading", { name: "admin.memoryTabGlossary" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "admin.memoryAllSources" })).toHaveAttribute("aria-selected", "true");
  });
});
