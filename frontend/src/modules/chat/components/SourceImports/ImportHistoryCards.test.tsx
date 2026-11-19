import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
    batch_created_at: "2026-11-16T02:04:00Z",
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
    fireEvent.click(screen.getByRole("button", { name: /文献库 one/ }));
    expect(screen.getByText("已加入 · 处理失败")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重试正文处理" })).toBeInTheDocument();
    expect(importStatusLabel({ ...importedPaper, status: "unknown", state_unavailable: true })).toBe("状态暂时无法获取");
  });
  it("shows the existing collection without a creation receipt and refreshes on request", async () => {
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
    expect(screen.getByRole("link", { name: "文献库 one" })).toHaveAttribute("href", "/lib/knowledge/detail/one");
    expect(screen.queryByText(/本次新建/)).not.toBeInTheDocument();
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
  it("sorts submissions into the same collection and keeps them independently collapsible", async () => {
    const later: ImportItem = {
      ...importedPaper,
      item_id: "later-item",
      batch_id: "later-batch",
      batch_created_at: "2026-11-16T02:17:00Z",
      title: "Later paper",
      status: "pending",
    };
    const view = (items: ImportItem[]) => <MemoryRouter><ImportHistoryCards imports={items} onRetry={vi.fn()} retrying={false} /></MemoryRouter>;
    const { rerender } = render(view([later, importedPaper]));
    const [newer, older] = screen.getAllByRole("button", { name: /文献库 one/ });
    expect(newer).toHaveTextContent("等待处理 1");
    expect(newer.querySelector("time")).toHaveAttribute("datetime", later.batch_created_at);
    expect(older.querySelector("time")).toHaveAttribute("datetime", importedPaper.batch_created_at);
    expect(newer).toHaveAttribute("aria-expanded", "false");
    expect(older).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(newer);
    await waitFor(() => expect(screen.getByText("Later paper")).toBeVisible());
    expect(screen.queryByText("Paper")).not.toBeInTheDocument();

    rerender(view([importedPaper, { ...later, status: "added" }]));
    expect(newer).toHaveAttribute("aria-expanded", "true");
    expect(older).toHaveAttribute("aria-expanded", "false");
    expect(newer).toHaveTextContent("已加入 1");
    fireEvent.click(older);
    await waitFor(() => expect(screen.getByText("Paper")).toBeVisible());
    expect(newer).toHaveAttribute("aria-expanded", "true");
  });

  it("folds multiple saved papers and shares the failure reason and retry time within a batch", async () => {
    const throttled: ImportItem = {
      ...importedPaper,
      item_id: "throttled",
      paper_key: "throttled",
      title: "Throttled paper",
      status: "failed",
      stage: "validate",
      retry_at: "2099-11-16T02:30:00Z",
      error_message: "论文来源请求受限，论文尚未入库",
    };
    const retryable: ImportItem = {
      ...throttled,
      item_id: "retryable",
      paper_key: "retryable",
      title: "Download failed paper",
      retry_at: undefined,
      retryable: true,
      error_message: "论文文件下载失败，论文尚未入库",
    };
    const retry = vi.fn();
    render(
      <MemoryRouter>
        <ImportHistoryCards
          imports={[
            { ...importedPaper, document_id: "pdf" },
            { ...importedPaper, item_id: "second", paper_key: "second", title: "Second paper", document_id: "second-pdf" },
            throttled,
            { ...throttled, item_id: "also-throttled", paper_key: "also-throttled", title: "Another throttled paper" },
            retryable,
          ]}
          initialOpenBatchId="batch"
          onRetry={retry}
          retrying={false}
        />
      </MemoryRouter>,
    );

    const saved = screen.getByRole("button", { name: /已加入的论文.*2 篇/ });
    const limited = screen.getByRole("button", { name: /论文来源请求受限/ });
    expect(saved).toHaveAttribute("aria-expanded", "false");
    expect(limited).toHaveAttribute("aria-expanded", "false");
    expect(within(limited).getByText(/后可重试/)).toBeVisible();
    expect(screen.queryByRole("link", { name: "Second paper" })).not.toBeInTheDocument();
    expect(screen.queryByText(throttled.title)).not.toBeInTheDocument();

    fireEvent.click(limited);
    await waitFor(() => expect(screen.getByText(throttled.title)).toBeVisible());
    expect(screen.getByText("Another throttled paper")).toBeVisible();
    expect(screen.getAllByText(/论文来源请求受限/)).toHaveLength(1);
    expect(screen.getAllByText(/后可重试/)).toHaveLength(1);
    expect(screen.queryByRole("button", { name: "重试" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /论文文件下载失败/ }));
    fireEvent.click(await screen.findByRole("button", { name: "重试" }));
    expect(retry).toHaveBeenCalledWith([retryable]);

    fireEvent.click(saved);
    expect(await screen.findByRole("link", { name: "Second paper" })).toHaveAttribute(
      "href", "/lib/knowledge/knowledge/one/second-pdf",
    );
  });
});
