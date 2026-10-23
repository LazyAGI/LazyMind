import { describe, expect, it } from "vitest";
import { emptyLibraryV3 } from "./libraryModel";
import { mergeArxivImports } from "./arxivLibrary";
import type { ArxivImportResult } from "./arxivImport";

const result = (version = "v3"): ArxivImportResult => ({
  paper: { title: `RULER ${version}`, authors: ["A. Author", "B. Author"], year: 2024, venue: "arXiv", abstract: "Real abstract", base_id: "2404.06654", version, abs_url: `https://arxiv.org/abs/2404.06654${version}`, pdf_url: `https://arxiv.org/pdf/2404.06654${version}` },
  work_id: "work-ruler", document_id: `doc-${version}`, filename: `RULER-${version}.pdf`, size: 4096,
  source_url: `https://arxiv.org/pdf/2404.06654${version}`, source_version: version, content_sha256: "a".repeat(64), status: "imported", processing_status: "ready",
});

describe("arXiv library synchronization", () => {
  it("restores a server paper and its real PDF reference without a local cache", () => {
    const data = mergeArxivImports(emptyLibraryV3(), [result()]);
    expect(data.papers).toHaveLength(1);
    expect(data.papers[0]).toMatchObject({ arxiv: "2404.06654", arxivVersion: "v3", academicWorkId: "work-ruler", title: "RULER v3", parse: "ready" });
    expect(data.papers[0].metadata.abstract).toBe("Real abstract");
    expect(data.attachments[0]).toMatchObject({ contentRef: "doc-v3", sourceVersion: "v3", size: 4096 });
    expect(data.papers[0].attachmentIds).toEqual([data.attachments[0].id]);
  });
  it("deduplicates repeat imports and preserves local organization and notes", () => {
    const before = mergeArxivImports(emptyLibraryV3(), [result()], "folder-one");
    before.papers[0].title = "My edited title";
    before.papers[0].tags = ["RAG"];
    before.papers[0].important = true;
    before.papers[0].noteIds = ["note-one"];
    before.notes = [{ id: "note-one", paperId: before.papers[0].id, title: "Notes", content: "Keep", kind: "note", createdAt: "now", updatedAt: "now" }];
    const data = mergeArxivImports(before, [result()], "different-folder");
    expect(data.papers).toHaveLength(1);
    expect(data.attachments).toHaveLength(1);
    expect(data.papers[0]).toMatchObject({ title: "My edited title", folderId: "folder-one", tags: ["RAG"], important: true, noteIds: ["note-one"] });
    expect(data.notes).toEqual(before.notes);
  });
  it("stores versions as attachments of one paper and retains newer metadata", () => {
    const before = mergeArxivImports(emptyLibraryV3(), [result()]);
    const data = mergeArxivImports(before, [result("v1")]);
    expect(data.papers).toHaveLength(1);
    expect(data.papers[0]).toMatchObject({ title: "RULER v3", arxivVersion: "v3" });
    expect(data.attachments.map((item) => item.sourceVersion)).toEqual(["v3", "v1"]);
    expect(data.attachments.filter((item) => item.isPrimary).map((item) => item.sourceVersion)).toEqual(["v3"]);
  });
  it("shows failed or pending processing honestly", () => {
    const failed = mergeArxivImports(emptyLibraryV3(), [{ ...result(), processing_status: "failed", processing_error: "Parser failed" }]);
    expect(failed.papers[0]).toMatchObject({ parse: "failed", processingError: "Parser failed" });
    const pending = mergeArxivImports(failed, [{ ...result(), processing_status: "parsing" }]);
    expect(pending.papers[0]).toMatchObject({ parse: "parsing", processingError: undefined });
  });
  it("does not restore locally removed papers during background synchronization", () => {
    const before = mergeArxivImports(emptyLibraryV3(), [result()]);
    const removed = { ...before, papers: [], hiddenArxivWorkIds: [result().work_id], hiddenArxivPapers: before.papers };
    const data = mergeArxivImports(removed, [result(), result("v4")]);
    expect(data).toEqual(removed);
  });
  it("explicitly restores removed paper identity, metadata and reading material", () => {
    const before = mergeArxivImports(emptyLibraryV3(), [result()], "folder-one");
    before.folders = [{ id: "folder-one", name: "Reading" }];
    const archived = { ...before.papers[0], id: "original-paper", title: "My title", important: true, tags: ["RAG"], noteIds: ["original-note"] };
    before.attachments = before.attachments.map((item) => ({ ...item, paperId: archived.id }));
    before.notes = [{ id: "original-note", paperId: archived.id, title: "Notes", content: "Keep me", kind: "note", createdAt: "now", updatedAt: "now" }];
    const removed = { ...before, papers: [], hiddenArxivWorkIds: [result().work_id], hiddenArxivPapers: [archived] };
    const data = mergeArxivImports(removed, [result()], null, true);
    expect(data.papers).toHaveLength(1);
    expect(data.papers[0]).toMatchObject({ id: archived.id, title: "My title", folderId: "folder-one", important: true, tags: ["RAG"], noteIds: ["original-note"] });
    expect(data.notes).toEqual(before.notes);
    expect(data.attachments).toHaveLength(1);
    expect(data.attachments[0].paperId).toBe(archived.id);
    expect(data.hiddenArxivWorkIds).toEqual([]);
    expect(data.hiddenArxivPapers).toEqual([]);
  });
  it("does not restore deleted folders or collections with an archived paper", () => {
    const before = mergeArxivImports(emptyLibraryV3(), [result()], "deleted-folder");
    before.papers[0].collectionIds = ["deleted-collection"];
    const removed = { ...before, papers: [], hiddenArxivWorkIds: [result().work_id], hiddenArxivPapers: before.papers };
    const data = mergeArxivImports(removed, [result()], null, true);
    expect(data.papers[0]).toMatchObject({ folderId: null, collectionIds: [] });
  });
  it.each([undefined, "10.1000/shared"])("keeps different arXiv identities separate despite matching titles and DOI %s", (doi) => {
    const first = { ...result(), paper: { ...result().paper, doi } };
    const second = {
      ...first, work_id: "work-other", document_id: "doc-other",
      paper: { ...first.paper, base_id: "2404.06655", abs_url: "https://arxiv.org/abs/2404.06655v3", pdf_url: "https://arxiv.org/pdf/2404.06655v3" },
      source_url: "https://arxiv.org/pdf/2404.06655v3",
    };
    const data = mergeArxivImports(emptyLibraryV3(), [first, second]);
    expect(data.papers).toHaveLength(2);
    expect(data.papers.map((paper) => paper.arxiv)).toEqual(expect.arrayContaining(["2404.06654", "2404.06655"]));
    expect(data.papers.find((paper) => paper.arxiv === "2404.06654")?.academicWorkId).toBe(first.work_id);
    expect(data.papers.find((paper) => paper.arxiv === "2404.06655")?.academicWorkId).toBe(second.work_id);
    expect(new Set(data.attachments.map((attachment) => attachment.paperId)).size).toBe(2);
  });
});
