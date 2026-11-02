import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PdfRenderJob } from "@/modules/knowledge/api/pdfArtifacts";
import PdfTranslationNotice from "./PdfTranslationNotice";

afterEach(() => { cleanup(); sessionStorage.clear(); });

describe("PDF translation notice", () => {
  it("dismisses a failed job and keeps it closed when the status refreshes", () => {
    const job: PdfRenderJob = { id: "failed-1", kind: "TRANSLATION_PDF", cache_key: "cache", status: "FAILED", stage: "TRANSLATING", progress: 40, error_message: "provider failed" };
    const props = { documentId: "paper-1", job, preparing: false, detail: "", providerLabel: "Baidu", onCancel: vi.fn() };
    const { rerender, unmount } = render(<PdfTranslationNotice {...props} />);
    expect(screen.getByText("翻译失败")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "关闭翻译提示" }));
    expect(screen.queryByText("翻译失败")).not.toBeInTheDocument();
    rerender(<PdfTranslationNotice {...props} job={{ ...job }} />);
    expect(screen.queryByText("翻译失败")).not.toBeInTheDocument();
    unmount();
    const next = render(<PdfTranslationNotice {...props} />);
    expect(screen.queryByText("翻译失败")).not.toBeInTheDocument();
    next.rerender(<PdfTranslationNotice {...props} job={{ ...job, id: "failed-2" }} />);
    expect(screen.getByText("翻译失败")).toBeInTheDocument();
  });
});
