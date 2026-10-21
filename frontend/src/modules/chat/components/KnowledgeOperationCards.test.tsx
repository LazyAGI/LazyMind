import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import KnowledgeOperationCards from "./KnowledgeOperationCards";
const get = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get } }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ i18n: { language: "zh" } }) }));

describe("knowledge operation receipts", () => {
  it("preserves the receipt while separately refreshing current status", async () => {
    get.mockResolvedValueOnce({ data: { data: { processing_status: "ready" } } });
    render(<MemoryRouter><KnowledgeOperationCards operations={[{ call_id: "call", action: "knowledge.document.upload", status: "partial", summary: { total: 2, succeeded: 1, failed: 1, skipped: 0, unconfirmed: 0 }, processing_summary: { submitted: 1 }, items: [
      { name: "good.txt", resource_ref: { kind: "document", knowledge_base_id: "kb", document_id: "doc" }, resource_status: "created", processing_status: "submitted" },
      { name: "bad.zzz", input_index: 1, resource_ref: { kind: "document", knowledge_base_id: "kb" }, resource_status: "failed", error: { code: "UNSUPPORTED_FORMAT", message: "unsupported format" } },
    ] }]} /></MemoryRouter>);
    expect(screen.getByText(/1 \/ 1 \/ 0 \/ 0/)).toBeInTheDocument();
    expect(screen.getByText(/0 \/ 1 \/ 0 \/ 0/)).toBeInTheDocument();
    expect(screen.getByText("unsupported format")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "bad.zzz" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "刷新当前状态" }));
    await waitFor(() => expect(screen.getByText(/本次查询状态: 处理成功/)).toBeInTheDocument());
    expect(screen.getByText(/已提交后台解析/)).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(1);
    get.mockRejectedValueOnce(new Error("deleted concurrently"));
    fireEvent.click(screen.getByRole("button", { name: /刷新当前状态/ }));
    await waitFor(() => expect(screen.getByText("当前状态未核实，请打开资源核对。")).toBeInTheDocument());
  });
});
