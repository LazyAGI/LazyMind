import { describe, expect, it } from "vitest";
import type { Attachment } from "./libraryModel";
import type { PaperItem } from "./model";
import { DEFAULT_PAPER_FILTERS, filterPaperRecords, getFilterBounds, hasPaperFilters, isReadablePdfAttachment } from "./paperFilters";

const paper = (id: string, extra: Partial<PaperItem> = {}): PaperItem => ({
  id, title: id, authors: "Author", venue: "Journal", year: 2024, folderId: null,
  status: "unread", important: false, tags: [], source: "doi", parse: "ready", ...extra,
});
const attachment = (paperId: string, extra: Partial<Attachment> = {}): Attachment => ({
  id: `attachment-${paperId}`, paperId, name: "paper.pdf", kind: "pdf", source: "arxiv",
  addedAt: "2026-10-21", contentRef: `document-${paperId}`, ...extra,
});
const ids = (papers: PaperItem[]) => papers.map((item) => item.id);

describe("paper filters", () => {
  it("combines star, reading, folder, year and citation filters without mutating papers", () => {
    const papers = [
      paper("match", { important: true, status: "reading", folderId: "folder", citationCount: 18, year: 2022 }),
      paper("unstarred", { status: "reading", folderId: "folder", citationCount: 18, year: 2022 }),
      paper("outside-year", { important: true, status: "reading", folderId: "folder", citationCount: 18, year: 2017 }),
      paper("unfiled", { important: true, status: "reading", citationCount: 18, year: 2022 }),
      paper("unread", { important: true, folderId: "folder", citationCount: 18, year: 2022 }),
    ];
    const before = JSON.stringify(papers);
    const filtered = filterPaperRecords(papers, [], { ...DEFAULT_PAPER_FILTERS, star: "starred", status: "reading", folder: "filed", yearRange: [2020, 2024], minCitations: 10 });
    expect(ids(filtered)).toEqual(["match"]);
    expect(JSON.stringify(papers)).toBe(before);
  });

  it("only counts usable PDF attachments, including PDFs classified as supplements", () => {
    const papers = [paper("stored"), paper("remote"), paper("placeholder"), paper("snapshot"), paper("none")];
    const attachments = [
      attachment("stored", { kind: "supplement", mimeType: "application/pdf" }),
      attachment("remote", { contentRef: undefined, sourceUrl: "https://arxiv.org/pdf/2404.06654v3" }),
      attachment("placeholder", { contentRef: undefined }),
      attachment("snapshot", { kind: "snapshot", mimeType: "text/html" }),
    ];
    expect(ids(filterPaperRecords(papers, attachments, { ...DEFAULT_PAPER_FILTERS, pdf: "has-pdf" }))).toEqual(["stored", "remote"]);
    expect(ids(filterPaperRecords(papers, attachments, { ...DEFAULT_PAPER_FILTERS, pdf: "needs-pdf" }))).toEqual(["placeholder", "snapshot", "none"]);
  });

  it("rejects missing and unsafe remote attachment destinations", () => {
    expect(isReadablePdfAttachment(attachment("stored"))).toBe(true);
    for (const sourceUrl of ["", "https://", "javascript:alert(1)", "file:///tmp/paper.pdf", "data:application/pdf,content"]) {
      expect(isReadablePdfAttachment(attachment("remote", { contentRef: undefined, sourceUrl }))).toBe(false);
    }
  });

  it("checks an uploaded PDF attachment rather than the paper metadata source", () => {
    const papers = [paper("uploaded", { source: "doi" }), paper("arxiv", { source: "local" }), paper("placeholder", { source: "local" })];
    const attachments = [attachment("uploaded", { source: "local" }), attachment("arxiv"), attachment("placeholder", { source: "local", contentRef: undefined })];
    expect(ids(filterPaperRecords(papers, attachments, { ...DEFAULT_PAPER_FILTERS, pdf: "uploaded" }))).toEqual(["uploaded"]);
  });

  it("keeps unknown citations in the default view but excludes them when a minimum is set", () => {
    const papers = [paper("unknown"), paper("zero", { citationCount: 0 }), paper("ten", { citationCount: 10 }), paper("invalid", { citationCount: Number.NaN })];
    expect(ids(filterPaperRecords(papers, [], DEFAULT_PAPER_FILTERS))).toEqual(["unknown", "zero", "ten", "invalid"]);
    expect(ids(filterPaperRecords(papers, [], { ...DEFAULT_PAPER_FILTERS, minCitations: 1 }))).toEqual(["ten"]);
  });

  it("supports unstarred, unfiled and read filters", () => {
    const papers = [paper("match", { status: "read" }), paper("starred", { important: true, status: "read" }), paper("folder", { folderId: "f", status: "read" })];
    expect(ids(filterPaperRecords(papers, [], { ...DEFAULT_PAPER_FILTERS, star: "unstarred", folder: "unfiled", status: "read" }))).toEqual(["match"]);
  });

  it("counts collection memberships when filtering papers without a primary folder", () => {
    const papers = [
      { ...paper("collection-only"), collectionIds: ["child"] },
      { ...paper("folder", { folderId: "first" }), collectionIds: [] },
      { ...paper("unfiled"), collectionIds: [] },
    ];
    expect(ids(filterPaperRecords(papers, [], { ...DEFAULT_PAPER_FILTERS, folder: "filed" }))).toEqual(["collection-only", "folder"]);
    expect(ids(filterPaperRecords(papers, [], { ...DEFAULT_PAPER_FILTERS, folder: "unfiled" }))).toEqual(["unfiled"]);
  });

  it("derives only known bounds and handles empty or single-year libraries", () => {
    expect(getFilterBounds([])).toEqual({ years: null, maxCitations: 0 });
    expect(getFilterBounds([paper("one")])).toEqual({ years: [2024, 2024], maxCitations: 0 });
    expect(getFilterBounds([paper("old", { year: 2018, citationCount: 5 }), paper("new", { year: 2026, citationCount: 120 }), paper("unknown", { year: 0, citationCount: -10 })])).toEqual({ years: [2018, 2026], maxCitations: 120 });
  });

  it("reports each non-default filter and clears them with the default state", () => {
    expect(hasPaperFilters(DEFAULT_PAPER_FILTERS)).toBe(false);
    for (const change of [{ star: "starred" }, { pdf: "uploaded" }, { folder: "filed" }, { status: "read" }, { yearRange: [2020, 2024] }, { minCitations: 1 }] as const) {
      expect(hasPaperFilters({ ...DEFAULT_PAPER_FILTERS, ...change } as Parameters<typeof hasPaperFilters>[0])).toBe(true);
    }
  });
});
