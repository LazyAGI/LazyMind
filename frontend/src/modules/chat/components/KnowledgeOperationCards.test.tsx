import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import KnowledgeOperationCards, {
  importStatusLabel,
} from "./KnowledgeOperationCards";
import type { ImportItem } from "./SourceImports/api";
import type { KnowledgeOperation } from "../utils/knowledgeOperations";
const get = vi.hoisted(() => vi.fn());
const post = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({
  BASE_URL: "",
  axiosInstance: { get, post },
}));

describe("knowledge operation receipts", () => {
  afterEach(() => vi.useRealTimers());
  beforeEach(() => {
    localStorage.setItem("lazymind:developer-active", "1");
    get.mockReset();
    post.mockReset();
  });
  it("keeps immutable receipts separate from current failures and retains status on query errors", async () => {
    get.mockResolvedValueOnce({
      data: {
        processing_status: "failed",
        processing_error: { message: "解析失败" },
      },
    });
    render(
      <MemoryRouter>
        <KnowledgeOperationCards
          operations={[
            {
              call_id: "call",
              action: "knowledge.document.upload",
              status: "partial",
              items: [
                {
                  name: "good.txt",
                  resource_ref: {
                    kind: "document",
                    knowledge_base_id: "kb",
                    document_id: "doc",
                  },
                  resource_status: "created",
                  processing_status: "submitted",
                },
                {
                  name: "bad.zzz",
                  resource_ref: { kind: "document", knowledge_base_id: "kb" },
                  resource_status: "failed",
                  error: { message: "unsupported format" },
                },
              ],
            },
          ]}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText("unsupported format")).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: "bad.zzz" }),
    ).not.toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(/当前处理状态：加入失败/)).toBeInTheDocument(),
    );
    expect(
      screen.getByText(/当次操作回执：已创建 · 处理中 · 已提交解析/),
    ).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(1);
    get.mockRejectedValueOnce(new Error("network"));
    fireEvent.click(screen.getByRole("button", { name: /刷新当前状态/ }));
    await waitFor(() =>
      expect(screen.getByText(/保留上次查询结果/)).toBeInTheDocument(),
    );
    expect(screen.getByText(/当前处理状态：加入失败/)).toBeInTheDocument();
  });
  it("does not label an existing failed document as newly added", () => {
    expect(
      importStatusLabel({
        status: "exists",
        processing_status: "failed",
      } as ImportItem),
    ).toBe("已存在 · 解析失败");
    expect(
      importStatusLabel({
        status: "added",
        processing_status: "not_requested",
      } as ImportItem),
    ).toBe("已加入 · 仅保存原件");
  });
  const createdLibrary = (id: string): KnowledgeOperation => ({
    call_id: id,
    action: "knowledge.create",
    status: "success",
    items: [
      {
        name: `文献库 ${id}`,
        resource_ref: { kind: "knowledge_base", knowledge_base_id: id },
        resource_status: "created",
      },
    ],
  });
  it("shows one creation receipt without a heading for a single target", () => {
    render(
      <MemoryRouter>
        <KnowledgeOperationCards operations={[createdLibrary("one")]} />
      </MemoryRouter>,
    );
    expect(screen.getAllByRole("link", { name: "文献库 one" })).toHaveLength(1);
    expect(screen.getByText("已创建知识库：")).toBeInTheDocument();
    expect(screen.queryByRole("heading")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "刷新当前状态" }),
    ).not.toBeInTheDocument();
  });
  it("groups multiple targets and folds creation receipts into their headings", () => {
    render(
      <MemoryRouter>
        <KnowledgeOperationCards
          operations={[createdLibrary("one"), createdLibrary("two")]}
        />
      </MemoryRouter>,
    );
    expect(screen.getAllByRole("heading")).toHaveLength(2);
    expect(screen.getAllByText(/本次新建/)).toHaveLength(2);
    expect(screen.getAllByRole("link", { name: "文献库 one" })).toHaveLength(1);
    expect(screen.queryByText("已创建知识库：")).not.toBeInTheDocument();
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
  it("does not invent a creation receipt or heading for imports into an existing target", async () => {
    const refreshImports = vi.fn().mockResolvedValue(undefined);
    render(
      <MemoryRouter>
        <KnowledgeOperationCards
          imports={[importedPaper]}
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
  it("refreshes both document state and import batches with the same button", async () => {
    get.mockResolvedValue({ data: { processing_status: "ready" } });
    const refreshImports = vi.fn().mockResolvedValue(undefined);
    render(
      <MemoryRouter>
        <KnowledgeOperationCards
          operations={[
            {
              call_id: "upload",
              action: "knowledge.document.upload",
              status: "success",
              items: [
                {
                  name: "Paper",
                  resource_ref: {
                    kind: "document",
                    knowledge_base_id: "one",
                    document_id: "doc",
                  },
                  resource_status: "created",
                },
              ],
            },
          ]}
          imports={[importedPaper]}
          onRefreshImports={refreshImports}
        />
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(screen.getAllByText("当前处理状态：已加入")).toHaveLength(2),
    );
    get.mockClear();
    fireEvent.click(
      await screen.findByRole("button", { name: "刷新当前状态" }),
    );
    await waitFor(() => {
      expect(get).toHaveBeenCalledWith(
        "/api/core/datasets/one/documents/doc",
        expect.anything(),
      );
      expect(refreshImports).toHaveBeenCalledTimes(1);
    });
  });

  it("resumes polling after a failed document is reparsed", async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, "hidden", {
      configurable: true,
      value: false,
    });
    get.mockResolvedValueOnce({ data: { processing_status: "failed" } })
      .mockResolvedValueOnce({ data: { processing_status: "running" } })
      .mockResolvedValue({ data: { processing_status: "ready" } });
    post.mockResolvedValue({ data: {} });
    render(
      <MemoryRouter>
        <KnowledgeOperationCards operations={[{
          call_id: "upload",
          action: "knowledge.document.upload",
          status: "success",
          items: [{
            name: "Paper",
            resource_ref: {
              kind: "document",
              knowledge_base_id: "kb",
              document_id: "doc",
            },
            resource_status: "created",
          }],
        }]} />
      </MemoryRouter>,
    );
    await act(async () => {});
    expect(screen.getByText(/当前处理状态：加入失败/)).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
    expect(get).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "重试解析" }));
    await act(async () => {});
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith(
      "/api/core/datasets/kb/documents/doc:reparse",
      {},
      { headers: { "Idempotency-Key": expect.any(String) } },
    );
    expect(get).toHaveBeenCalledTimes(2);
    expect(screen.getByText("当前处理状态：处理中")).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(get).toHaveBeenCalledTimes(3);
    expect(screen.getByText("当前处理状态：已加入")).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
    expect(get).toHaveBeenCalledTimes(3);
  });

  it("deduplicates receipt documents and polls only the remaining active document", async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, "hidden", {
      configurable: true,
      value: false,
    });
    get.mockImplementation(async (url: string) => ({
      data: {
        processing_status: url.endsWith("/doc-19") ? "running" : "ready",
      },
    }));
    const items = Array.from({ length: 20 }, (_, i) => ({
      name: `${i}.txt`,
      resource_ref: {
        kind: "document",
        knowledge_base_id: "kb",
        document_id: `doc-${i}`,
      },
      resource_status: "created",
    }));
    const operations = [
      {
        call_id: "upload",
        action: "knowledge.document.upload",
        status: "success",
        items,
      },
      {
        call_id: "rename",
        action: "knowledge.resource.update_metadata",
        status: "success",
        items: [items[0]],
      },
    ] as KnowledgeOperation[];
    const { unmount } = render(
      <MemoryRouter>
        <KnowledgeOperationCards operations={operations} />
      </MemoryRouter>,
    );
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(get).toHaveBeenCalledTimes(20);
    get.mockClear();
    Object.defineProperty(document, "hidden", {
      configurable: true,
      value: true,
    });
    fireEvent(document, new Event("visibilitychange"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(get).not.toHaveBeenCalled();
    Object.defineProperty(document, "hidden", {
      configurable: true,
      value: false,
    });
    fireEvent(document, new Event("visibilitychange"));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(get).toHaveBeenCalledTimes(20);
    get.mockClear();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(get).toHaveBeenCalledTimes(1);
    expect(get.mock.calls[0][0]).toContain("/doc-19");
    get.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "刷新当前状态" }));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(get).toHaveBeenCalledTimes(20);
    get.mockResolvedValue({ data: { processing_status: "ready" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    get.mockClear();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(get).not.toHaveBeenCalled();
    unmount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(get).not.toHaveBeenCalled();
  });
});
