import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import KnowledgeOperationCards, { importStatusLabel } from "./KnowledgeOperationCards";
import type { ImportItem } from "./SourceImports/api";
import type { KnowledgeOperation } from "../utils/knowledgeOperations";
const get = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get } }));

describe("knowledge operation receipts", () => {
  beforeEach(() => get.mockReset());
  it("keeps immutable receipts separate from current failures and retains status on query errors", async () => {
    get.mockResolvedValueOnce({ data: { processing_status: "failed", processing_error: { message: "解析失败" } } });
    render(<MemoryRouter><KnowledgeOperationCards operations={[{ call_id: "call", action: "knowledge.document.upload", status: "partial", items: [
      { name: "good.txt", resource_ref: { kind: "document", knowledge_base_id: "kb", document_id: "doc" }, resource_status: "created", processing_status: "submitted" },
      { name: "bad.zzz", resource_ref: { kind: "document", knowledge_base_id: "kb" }, resource_status: "failed", error: { message: "unsupported format" } },
    ] }]} /></MemoryRouter>);
    expect(screen.getByText("unsupported format")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "bad.zzz" })).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByText(/当前处理状态：加入失败/)).toBeInTheDocument());
    expect(screen.getByText(/当次操作回执：已创建 · 处理中 · 已提交解析/)).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(1);
    get.mockRejectedValueOnce(new Error("network"));
    fireEvent.click(screen.getByRole("button", { name: /刷新当前状态/ }));
    await waitFor(() => expect(screen.getByText(/保留上次查询结果/)).toBeInTheDocument());
    expect(screen.getByText(/当前处理状态：加入失败/)).toBeInTheDocument();
  });
  it("does not label an existing failed document as newly added", () => {
    expect(importStatusLabel({ status: "exists", processing_status: "failed" } as ImportItem)).toBe("已存在 · 解析失败");
    expect(importStatusLabel({ status: "added", processing_status: "not_requested" } as ImportItem)).toBe("已加入 · 仅保存原件");
  });
  const createdLibrary = (id: string): KnowledgeOperation => ({ call_id: id, action: "knowledge.create", status: "success", items: [{ name: `文献库 ${id}`, resource_ref: { kind: "knowledge_base", knowledge_base_id: id }, resource_status: "created" }] });
  it("shows one creation receipt without a heading for a single target", () => {
    render(<MemoryRouter><KnowledgeOperationCards operations={[createdLibrary("one")]} /></MemoryRouter>);
    expect(screen.getAllByRole("link", { name: "文献库 one" })).toHaveLength(1);
    expect(screen.getByText("已创建知识库：")).toBeInTheDocument();
    expect(screen.queryByRole("heading")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "刷新当前状态" })).not.toBeInTheDocument();
  });
  it("groups multiple targets and folds creation receipts into their headings", () => {
    render(<MemoryRouter><KnowledgeOperationCards operations={[createdLibrary("one"), createdLibrary("two")]} /></MemoryRouter>);
    expect(screen.getAllByRole("heading")).toHaveLength(2);
    expect(screen.getAllByText(/本次新建/)).toHaveLength(2);
    expect(screen.getAllByRole("link", { name: "文献库 one" })).toHaveLength(1);
    expect(screen.queryByText("已创建知识库：")).not.toBeInTheDocument();
  });
  const importedPaper: ImportItem = { item_id: "item", batch_id: "batch", title: "Paper", paper_key: "arxiv:2210.03629", target_dataset_id: "one", target_name: "文献库 one", created_library: false, status: "added", stage: "processing", processing_status: "ready", retryable: false };
  it("does not invent a creation receipt or heading for imports into an existing target", async () => {
    const refreshImports = vi.fn().mockResolvedValue(undefined);
    render(<MemoryRouter><KnowledgeOperationCards imports={[importedPaper]} onRefreshImports={refreshImports} /></MemoryRouter>);
    expect(screen.queryByRole("heading")).not.toBeInTheDocument();
    expect(screen.queryByText(/已创建知识库/)).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "刷新当前状态" })).not.toHaveClass("ant-btn-loading"));
    fireEvent.click(await screen.findByRole("button", { name: "刷新当前状态" }));
    await waitFor(() => expect(refreshImports).toHaveBeenCalledTimes(1));
  });
  it("refreshes both document state and import batches with the same button", async () => {
    get.mockResolvedValue({ data: { processing_status: "ready" } });
    const refreshImports = vi.fn().mockResolvedValue(undefined);
    render(<MemoryRouter><KnowledgeOperationCards operations={[{ call_id: "upload", action: "knowledge.document.upload", status: "success", items: [{ name: "Paper", resource_ref: { kind: "document", knowledge_base_id: "one", document_id: "doc" }, resource_status: "created" }] }]} imports={[importedPaper]} onRefreshImports={refreshImports} /></MemoryRouter>);
    await waitFor(() => expect(screen.getAllByText("当前处理状态：已加入")).toHaveLength(2));
    get.mockClear();
    fireEvent.click(await screen.findByRole("button", { name: "刷新当前状态" }));
    await waitFor(() => {
      expect(get).toHaveBeenCalledWith("/api/core/datasets/one/documents/doc", expect.anything());
      expect(refreshImports).toHaveBeenCalledTimes(1);
    });
  });
});
