import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn() }));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: mocks }));

import { completePdfRenderJob, createSearchablePdfJob, createTranslationPdfJob, deletePdfArtifact, getPdfArtifactData, getPdfArtifactLayout, isActivePdfJob, isRasterLayoutBlock, latestActiveTranslationJob, latestTranslationJob, listPdfLayoutBlocks, pdfArtifactContentUrl, splitTranslationText, translateTextWithLLM, validatePdfLayoutBlocks } from "./pdfArtifacts";

describe("PDF artifacts API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("normalizes MinerU zero-based pages and preserves bbox dimensions", async () => {
    mocks.get.mockResolvedValueOnce({ data: { data: { segments: [{
      segment_id: "s1", content: "Caption", meta: JSON.stringify({ page: 2, bbox: [10, 20, 110, 60], page_width: 600, page_height: 800, type: "caption" }),
    }] } } });
    await expect(listPdfLayoutBlocks("d", "doc")).resolves.toEqual([expect.objectContaining({
      id: "s1", page: 3, bbox: [10, 20, 110, 60], pageWidth: 600, pageHeight: 800, type: "caption",
    })]);
    expect(mocks.get).toHaveBeenCalledWith(expect.stringContaining("/segments"), expect.objectContaining({
      params: expect.objectContaining({ group: "line" }),
    }));
  });

  it("keeps MinerU image regions even when they contain no OCR text", async () => {
    mocks.get.mockResolvedValueOnce({ data: { data: { segments: [{
      segment_id: "image-1", content: "", image_key: "figure.png",
      meta: JSON.stringify({ page: 0, bbox: [50, 80, 300, 260], page_width: 600, page_height: 800 }),
    }] } } });
    await expect(listPdfLayoutBlocks("d", "doc")).resolves.toEqual([expect.objectContaining({
      id: "image-1", type: "image", bbox: [50, 80, 300, 260],
    })]);
    expect(isRasterLayoutBlock({ type: "figure" })).toBe(true);
    expect(isRasterLayoutBlock({ type: "paragraph" })).toBe(false);
  });

  it("rejects retrieval chunks that cannot safely reconstruct page layout", () => {
    expect(() => validatePdfLayoutBlocks([{ id: "bad", page: 1, text: "字".repeat(1000), bbox: [10, 10, 500, 30] }], 19))
      .toThrow("Reader 布局结果不完整");
    expect(() => validatePdfLayoutBlocks([
      { id: "p1", page: 1, text: "第一页", bbox: [10, 10, 100, 40] },
      { id: "p2", page: 2, text: "第二页", bbox: [10, 10, 100, 40] },
    ], 2)).not.toThrow();
  });

  it("uses a non-streaming ephemeral LazyMind conversation for LLM translation", async () => {
    mocks.post.mockResolvedValueOnce({ data: { data: { message: "译文" } } });
    await expect(translateTextWithLLM("source", "zh")).resolves.toBe("译文");
    expect(mocks.post).toHaveBeenCalledWith(expect.stringContaining("conversations:chat"), expect.objectContaining({
      stream: false,
      surface: "knowledge_pdf_translation",
      initial_conversation_settings: expect.objectContaining({ ephemeral: true }),
    }), expect.anything());
  });

  it("builds an artifact URL with exactly one origin", () => {
    expect(pdfArtifactContentUrl("dataset", "document", "artifact")).toBe(
      "/api/core/datasets/dataset/documents/document/pdf-artifacts/artifact:content",
    );
  });

  it("can bypass cache and delete generated versions", async () => {
    mocks.post.mockResolvedValue({ data: { data: { cache_status: "miss" } } });
    mocks.delete.mockResolvedValue({ data: { data: { deleted: "a1" } } });
    await createSearchablePdfJob("d", "doc", true);
    await createTranslationPdfJob("d", "doc", { target_language: "zh", provider_type: "api", provider: "Tencent", force: true });
    await deletePdfArtifact("d", "doc", "a1");
    expect(mocks.post.mock.calls[0][1]).toEqual({ force: true });
    expect(mocks.post.mock.calls[1][1]).toEqual(expect.objectContaining({ force: true }));
    expect(mocks.delete).toHaveBeenCalledWith(expect.stringContaining("/pdf-artifacts/a1"));
  });

  it("restores the newest unfinished translation job after refresh", () => {
    const jobs = [
      { id: "ready", kind: "TRANSLATION_PDF", status: "READY" },
      { id: "search", kind: "SEARCHABLE_PDF", status: "RUNNING" },
      { id: "active", kind: "TRANSLATION_PDF", status: "RUNNING" },
    ] as never;
    expect(latestActiveTranslationJob(jobs)?.id).toBe("active");
    expect(isActivePdfJob({ status: "WAITING_DEPENDENCY" })).toBe(true);
    expect(isActivePdfJob({ status: "FAILED" })).toBe(false);
  });

  it("restores the newest failed or cancelled translation job after refresh", () => {
    const jobs = [
      { id: "active-old", kind: "TRANSLATION_PDF", status: "RUNNING" },
      { id: "search", kind: "SEARCHABLE_PDF", status: "FAILED" },
      { id: "failed-new", kind: "TRANSLATION_PDF", status: "FAILED", error_message: "后端任务不存在" },
    ] as never;
    expect(latestTranslationJob(jobs)?.id).toBe("failed-new");
  });

  it("uploads the reusable layout manifest with a searchable PDF", async () => {
    mocks.post.mockResolvedValueOnce({ data: { data: { id: "a1" } } });
    await completePdfRenderJob("d", "doc", "j", new Blob(["pdf"]), "file.pdf", 0, [{ id: "b1", page: 1, text: "Text" }]);
    const body = mocks.post.mock.calls[0][1] as FormData;
    expect(body.get("layout_manifest")).toBeInstanceOf(Blob);
  });

  it("submits every document format as a backend translation job", async () => {
    mocks.post.mockResolvedValueOnce({ data: { data: { cache_status: "miss", job: { id: "j1" } } } });
    await createTranslationPdfJob("d", "doc", {
      target_language: "zh", provider_type: "api", provider: "Tencent",
      source: new Blob(["paragraph"]), source_filename: "notes.md",
    });
    const body = mocks.post.mock.calls[0][1] as FormData;
    expect(body.get("source")).toBeInstanceOf(Blob);
    expect(body.get("target_language")).toBe("zh");
    expect(body.get("layout_manifest")).toBeNull();
  });

  it("leaves PDF region extraction to the backend", async () => {
    mocks.post.mockResolvedValueOnce({ data: { data: { cache_status: "miss", job: { id: "j-pdf" } } } });
    await createTranslationPdfJob("d", "doc", {
      target_language: "zh", provider_type: "api", provider: "Tencent",
      source: new Blob(["pdf"]), source_filename: "paper.pdf",
    });
    const body = mocks.post.mock.calls[0][1] as FormData;
    expect(body.get("source")).toBeInstanceOf(Blob);
    expect(body.get("layout_manifest")).toBeNull();
  });

  it("loads translation inputs from the cached artifact instead of DocNode", async () => {
    const pdf = new ArrayBuffer(4);
    mocks.get
      .mockResolvedValueOnce({ data: pdf })
      .mockResolvedValueOnce({ data: { version: 1, blocks: [{ id: "b1", page: 1, text: "Text" }] } });
    await expect(getPdfArtifactData("d", "doc", "a1")).resolves.toBe(pdf);
    await expect(getPdfArtifactLayout("d", "doc", "a1")).resolves.toEqual([{ id: "b1", page: 1, text: "Text" }]);
    expect(mocks.get.mock.calls.every(([url]) => !String(url).includes("/segments"))).toBe(true);
  });

  it("splits long OCR text without truncating surrogate pairs or content", () => {
    const source = `${"中文😀".repeat(1300)}tail`;
    const chunks = splitTranslationText(source, 1800);
    expect(chunks.length).toBeGreaterThan(1);
    expect(chunks.every((chunk) => Array.from(chunk).length <= 1800)).toBe(true);
    expect(chunks.join("")).toBe(source);
  });
});
