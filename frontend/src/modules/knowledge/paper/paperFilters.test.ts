import {paperFixture} from './__tests__/fixtures';
import { describe, expect, it } from "vitest";
import type { Attachment } from "./libraryModel";
import { DEFAULT_PAPER_FILTERS, filterPaperRecords, getFilterBounds, hasPaperFilters, isReadablePdfAttachment } from "./paperFilters";
function paper(id:string,overrides:Parameters<typeof paperFixture>[0]={}) { return paperFixture({id,...overrides}); }
const ids=(papers:ReturnType<typeof paperFixture>[])=>papers.map(p=>p.id);
function attachment(paperId:string,fields:Partial<Attachment>={}):Attachment{return {id:`a-${paperId}`,paperId,name:'Paper.pdf',kind:'pdf',contentRef:`pdf-${paperId}`,source:'arxiv',addedAt:'',...fields};}

describe("paper filters", () => {
    it("combines star, reading, folder, year filters without mutating papers", () => {
        const papers = [
            paper("match", { important: true, status: "reading", folderId: "folder", metadata: {
                    citationCount: 18,
                    year: 2022
                } }),
            paper("unstarred", { status: "reading", folderId: "folder", metadata: {
                    citationCount: 18,
                    year: 2022
                } }),
            paper("outside-year", { important: true, status: "reading", folderId: "folder", metadata: {
                    citationCount: 18,
                    year: 2017
                } }),
            paper("unfiled", { important: true, status: "reading", metadata: {
                    citationCount: 18,
                    year: 2022
                } }),
            paper("unread", { important: true, folderId: "folder", metadata: {
                    citationCount: 18,
                    year: 2022
                } }),
        ];
        const before = JSON.stringify(papers);
        const filtered = filterPaperRecords(papers, [], { ...DEFAULT_PAPER_FILTERS, star: "starred", status: "reading", folder: "filed", yearRange: [2020, 2024] });
        expect(ids(filtered)).toEqual(["match"]);
        expect(JSON.stringify(papers)).toBe(before);
    });
    it("counts stored PDFs separately from external PDF links", () => {
        const papers = [paper("stored"), paper("remote"), paper("placeholder"), paper("snapshot"), paper("material"), paper("none")];
        const attachments = [
            attachment("stored"),
            attachment("material", { kind: "supplement", mimeType: "application/pdf" }),
            attachment("remote", { contentRef: undefined, sourceUrl: "https://arxiv.org/pdf/2404.06654v3" }),
            attachment("placeholder", { contentRef: undefined }),
            attachment("snapshot", { kind: "snapshot", mimeType: "text/html" }),
        ];
        expect(ids(filterPaperRecords(papers, attachments, { ...DEFAULT_PAPER_FILTERS, pdf: "has-pdf" }))).toEqual(["stored"]);
        expect(ids(filterPaperRecords(papers, attachments, { ...DEFAULT_PAPER_FILTERS, pdf: "needs-pdf" }))).toEqual(["remote", "placeholder", "snapshot", "material", "none"]);
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
    it("supports unstarred, unfiled and read filters", () => {
        const papers = [paper("match", { status: "read" }), paper("starred", { important: true, status: "read" }), paper("folder", { folderId: "f", status: "read" })];
        expect(ids(filterPaperRecords(papers, [], { ...DEFAULT_PAPER_FILTERS, star: "unstarred", folder: "unfiled", status: "read" }))).toEqual(["match"]);
    });
    it("derives only known bounds and handles empty or single-year libraries", () => {
        expect(getFilterBounds([])).toEqual({ years: null });
        expect(getFilterBounds([paper("one")])).toEqual({ years: [2024, 2024] });
        expect(getFilterBounds([paper("old", { metadata: {
                    year: 2018,
                    citationCount: 5
                } }), paper("new", { metadata: {
                    year: 2026,
                    citationCount: 120
                } }), paper("unknown", { metadata: {
                    year: 0,
                    citationCount: -10
                } })])).toEqual({ years: [2018, 2026] });
    });
    it("reports each non-default filter and clears them with the default state", () => {
        expect(hasPaperFilters(DEFAULT_PAPER_FILTERS)).toBe(false);
        for (const change of [{ star: "starred" }, { pdf: "uploaded" }, { folder: "filed" }, { status: "read" }, { yearRange: [2020, 2024] }] as const) {
            expect(hasPaperFilters({ ...DEFAULT_PAPER_FILTERS, ...change } as Parameters<typeof hasPaperFilters>[0])).toBe(true);
        }
    });
});
