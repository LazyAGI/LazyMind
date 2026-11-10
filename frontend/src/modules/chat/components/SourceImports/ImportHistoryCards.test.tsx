import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import ImportHistoryCards, { importStatusLabel } from "./ImportHistoryCards";
import type { ImportItem } from "./api";

describe("paper import history", () => {
  it("does not label an existing failed document as newly added", () => {
    expect(
      importStatusLabel({
        status: "exists",
        processing_status: "failed",
      } as ImportItem),
    ).toBe("已存在 · 处理失败");
    expect(
      importStatusLabel({
        status: "added",
        processing_status: "not_requested",
      } as ImportItem),
    ).toBe("已加入 · 仅保存原件");
  });
  const importedPaper: ImportItem = {
    item_id: "item",
    batch_id: "batch",
    title: "Paper",
    paper_key: "arxiv:2210.03629",
    target_dataset_id: "one",
    target_name: "文献库 one",
    created_library: false,
    status: "added",
    stage: "processing",
    processing_status: "ready",
    retryable: false,
  };
  it("keeps a saved original in the added count even when processing failed", () => {
    render(<MemoryRouter><ImportHistoryCards
      imports={[{ ...importedPaper, document_id: "pdf", processing_status: "failed", retryable: true }]}
      onRetry={vi.fn()} retrying={false} /></MemoryRouter>);
    expect(screen.getByText(/已加入 1 · 已存在 0 · 加入失败 0/)).toBeInTheDocument();
    expect(screen.getByText("当前处理状态：已加入 · 处理失败")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重试正文处理" })).toBeInTheDocument();
    expect(importStatusLabel({ ...importedPaper, status: "unknown", state_unavailable: true })).toBe("状态暂时无法获取");
  });
  it("does not invent a creation receipt or heading for imports into an existing target", async () => {
    const refreshImports = vi.fn().mockResolvedValue(undefined);
    render(
      <MemoryRouter>
        <ImportHistoryCards
          imports={[importedPaper]}
          onRetry={vi.fn()}
          retrying={false}
          onRefreshImports={refreshImports}
        />
      </MemoryRouter>,
    );
    expect(screen.queryByRole("heading")).not.toBeInTheDocument();
    expect(screen.queryByText(/已创建知识库/)).not.toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "刷新当前状态" }),
      ).not.toHaveClass("ant-btn-loading"),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "刷新当前状态" }),
    );
    await waitFor(() => expect(refreshImports).toHaveBeenCalledTimes(1));
  });
});
