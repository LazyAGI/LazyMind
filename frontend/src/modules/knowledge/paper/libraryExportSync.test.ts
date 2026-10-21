import { afterEach, describe, expect, it, vi } from "vitest";
import type { ArxivImportResult } from "./arxivImport";
import { loadPaperLibraryForExport } from "./exportLibrary";
import { emptyLibraryV3 } from "./libraryModel";
import { mergeArxivImports } from "./arxivLibrary";
import { loadPaperLibraryV3, savePaperLibraryV3 } from "./store";

const api = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: api }));

const imported = (baseId = "2404.06654"): ArxivImportResult => ({
  paper: {
    title: `Server Paper ${baseId}`, authors: ["A. Author"], year: 2024, venue: "arXiv", abstract: "Research abstract",
    base_id: baseId, version: "v1", abs_url: `https://arxiv.org/abs/${baseId}v1`, pdf_url: `https://arxiv.org/pdf/${baseId}v1`,
  },
  work_id: `work-${baseId}`, document_id: `doc-${baseId}`, filename: `${baseId}.pdf`, size: 4096,
  source_url: `https://arxiv.org/pdf/${baseId}v1`, source_version: "v1", content_sha256: "a".repeat(64),
  status: "imported", processing_status: "ready",
});

afterEach(() => { vi.clearAllMocks(); localStorage.clear(); });

describe("server synchronization before library export", () => {
  it("includes server papers in a fresh browser with no cached library", async () => {
    api.get.mockResolvedValue({ data: { items: [imported()] } });

    const library = await loadPaperLibraryForExport("server-library");

    expect(library.papers).toHaveLength(1);
    expect(library.papers[0]).toMatchObject({ title: "Server Paper 2404.06654", arxiv: "2404.06654" });
    expect(api.get).toHaveBeenCalledWith("/api/core/academic/arxiv/library", expect.objectContaining({ params: { target_dataset_id: "server-library" } }));
  });

  it("adds newly saved server papers while preserving local edits and organization", async () => {
    const cached = mergeArxivImports(emptyLibraryV3(), [imported()]);
    cached.folders = [{ id: "reading", name: "My Reading" }];
    Object.assign(cached.papers[0], { title: "My edited title", folderId: "reading", tags: ["RAG"], important: true });
    savePaperLibraryV3("library", cached);
    api.get.mockResolvedValue({ data: { items: [imported(), imported("2404.06655")] } });

    const library = await loadPaperLibraryForExport("library");

    expect(library.papers).toHaveLength(2);
    expect(library.papers.find((paper) => paper.arxiv === "2404.06654")).toMatchObject({ title: "My edited title", folderId: "reading", tags: ["RAG"], important: true });
    expect(library.papers.find((paper) => paper.arxiv === "2404.06655")?.title).toBe("Server Paper 2404.06655");
    expect(library.folders).toEqual([{ id: "reading", name: "My Reading" }]);
  });

  it("keeps locally removed server papers out of the export", async () => {
    const cached = emptyLibraryV3();
    cached.hiddenArxivWorkIds = ["work-2404.06654"];
    savePaperLibraryV3("library", cached);
    api.get.mockResolvedValue({ data: { items: [imported()] } });

    expect((await loadPaperLibraryForExport("library")).papers).toEqual([]);
  });

  it("rejects server query failures instead of exporting a stale cached library", async () => {
    const cached = mergeArxivImports(emptyLibraryV3(), [imported()]);
    savePaperLibraryV3("library", cached);
    api.get.mockRejectedValue({ response: { status: 503, data: { message: "论文库同步服务不可用" } } });

    await expect(loadPaperLibraryForExport("library")).rejects.toThrow("论文库同步服务不可用");
    expect(loadPaperLibraryV3("library")).toEqual(cached);
  });

  it("rejects malformed server data instead of treating it as an empty library", async () => {
    api.get.mockResolvedValue({ data: {} });
    await expect(loadPaperLibraryForExport("library")).rejects.toThrow("arXiv 服务返回的数据不完整或格式无效");
  });
});
