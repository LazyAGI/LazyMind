import { describe, expect, it } from "vitest";
import {
  applyPaperEdits, assignFolders, classifyIdentifier, duplicateOf, filterLibraryView, filterPapers,
  formatCitation, identifiersInText, movePapers, normalizeLibrary, paperLink, papersInView,
  parseBibTeX, parseCSV, parseRIS, planImport, removePapers, sampleLibrary, sortPapers,
  toBibTeX, toCSV, toRIS,
} from "./model";

describe("paper library model", () => {
  it("keeps papers that satisfy every filter and the search box", () => {
    const library = sampleLibrary();
    const matched = filterPapers(library.papers, "unread", [{ field: "tag", value: "RAG", label: "标签包含 RAG" }, { field: "source", value: "semantic_scholar", label: "来源 Semantic Scholar" }], "all", "self-rag");
    expect(matched.map((item) => item.id)).toEqual(["p4"]);
  });
  it("preserves nested folder hierarchy", () => {
    const flat = normalizeLibrary({ folders: [{ id: "llm", name: "大模型", parentId: null }, { id: "rag", name: "检索增强", parentId: "llm" }], papers: [sampleLibrary().papers[2]] });
    expect(flat.folders.map((folder) => folder.name)).toEqual(["大模型", "检索增强"]);
    expect(flat.papers[0].folderId).toBe("rag");
    expect(flat.folders[1].parentId).toBe("llm");
  });
  it("round-trips BibTeX, RIS and CSV", () => {
    const parsed = parseBibTeX(`@article{lewis2020,
  title = {Retrieval-Augmented Generation}, author = {Lewis and Perez}, journal = {NeurIPS}, year = {2020}, doi = {10.0000/rag}
}`, "google_scholar");
    expect(parsed[0]).toMatchObject({ title: "Retrieval-Augmented Generation", year: 2020, source: "google_scholar", doi: "10.0000/rag" });
    expect(toBibTeX(parsed)).toContain("title = {Retrieval-Augmented Generation}");
    const ris = parseRIS("TY  - JOUR\nTI  - Retrieval-Augmented Generation\nAU  - Lewis\nAU  - Perez\nJO  - NeurIPS\nPY  - 2020\nDO  - 10.0000/rag\nER  - ", "local");
    expect(ris[0]).toMatchObject({ authors: "Lewis、Perez", doi: "10.0000/rag" });
    expect(parseCSV(toCSV(parsed, [])).papers[0]).toMatchObject({ title: "Retrieval-Augmented Generation", doi: "10.0000/rag" });
  });
  it("skips duplicates by DOI, then arXiv, then title and year", () => {
    const existing = sampleLibrary().papers;
    expect(duplicateOf(existing, { ...existing[0], id: "new", title: "另一篇", doi: "https://doi.org/10.48550/arXiv.1706.03762v1" })?.id).toBe("p1");
    expect(duplicateOf(existing, { ...existing[0], id: "new", title: "另一篇", doi: undefined, arxiv: "1706.03762v7" })?.id).toBe("p1");
    expect(planImport(existing, [{ ...existing[1], id: "copy", doi: undefined }]).skipped).toHaveLength(1);
    expect(planImport(existing, [{ ...existing[1], id: "fresh", title: "全新论文", year: 2024, doi: undefined }]).fresh).toHaveLength(1);
  });
  it("formats citations and exports RIS", () => {
    const item = sampleLibrary().papers[0];
    expect(formatCitation(item, "gbt")).toBe("Vaswani 等. Attention Is All You Need[J]. NeurIPS, 2017.");
    expect(formatCitation({ ...item, venue: "Proceedings of The Web Conference 2017" }, "gbt")).toBe("Vaswani 等. Attention Is All You Need[J]. Proceedings of The Web Conference, 2017.");
    expect(formatCitation(item, "apa")).toBe("Vaswani 等 (2017). Attention Is All You Need. NeurIPS.");
    expect(formatCitation(item, "mla")).toBe('Vaswani 等. "Attention Is All You Need." NeurIPS, 2017.');
    expect(parseRIS(toRIS([item]), "zotero")[0]).toMatchObject({ title: item.title, doi: item.doi, arxiv: item.arxiv, year: 2017 });
  });
  it("places CSV rows into named single-level folders", () => {
    const parsed = parseCSV("题名,作者,出处,年份,DOI,arXiv,标签,文件夹\n新论文,作者,NeurIPS,2024,,,RAG,评测\n");
    const placed = assignFolders(sampleLibrary(), parsed.papers, parsed.folderNames, null);
    expect(placed.folders.some((folder) => folder.name === "评测")).toBe(true);
    expect(placed.papers[0].folderId).toBe("eval");
  });
  it("reads identifiers out of PDF text", () => {
    expect(identifiersInText("Attention Is All You Need 10.48550/arXiv.1706.03762 2017")).toMatchObject({ doi: "10.48550/arxiv.1706.03762", arxiv: "1706.03762", year: 2017 });
  });
  it("keeps a folder view to that folder only", () => {
    const library = sampleLibrary();
    expect(papersInView(library, "rag").map((item) => item.id)).toEqual(["p3", "p4"]);
    expect(papersInView(library, null)).toHaveLength(library.papers.length);
    expect(normalizeLibrary({ folders: [{ id: "ml", name: "机器学习" }], papers: [{ ...library.papers[0], folderId: "missing" }] }).papers[0].folderId).toBeNull();
  });
  it("classifies identifiers and links back to the source", () => {
    expect(classifyIdentifier("https://doi.org/10.18653/v1/n19-1423")).toEqual({ kind: "doi", value: "10.18653/v1/n19-1423" });
    expect(classifyIdentifier("https://arxiv.org/pdf/1706.03762v3.pdf")?.value).toBe("1706.03762");
    expect(classifyIdentifier("https://arxiv.org/pdf/2404.06654") ).toEqual({ kind: "arxiv", value: "2404.06654" });
    expect(classifyIdentifier("https://arxiv.org/abs/2404.06654") ).toEqual({ kind: "arxiv", value: "2404.06654" });
    expect(classifyIdentifier("不是编号")).toBeNull();
    expect(paperLink(sampleLibrary().papers[0])).toBe("https://doi.org/10.48550/arxiv.1706.03762");
    expect(paperLink({ ...sampleLibrary().papers[3], doi: undefined })).toBeNull();
  });
  it("matches any filter, sorts by year, and dedupes inside one batch", () => {
    const papers = sampleLibrary().papers;
    const either = filterPapers(papers, "all", [{ field: "tag", value: "视觉", label: "标签包含 视觉" }, { field: "status", value: "unread", label: "阅读状态为未读" }], "any");
    expect(either.some((item) => item.id === "p6")).toBe(true);
    expect(either.some((item) => item.id === "p4")).toBe(true);
    expect(sortPapers(papers, "newest")[0].year).toBeGreaterThanOrEqual(sortPapers(papers, "oldest")[0].year);
    expect(sortPapers(papers, "oldest")[0].id).toBe("p6");
    const planned = planImport(papers, [papers[0], { ...papers[0], id: "again" }, { ...papers[4], id: "fresh", title: "另一篇长文", year: 2025, doi: undefined, arxiv: undefined }]);
    expect(planned.skipped.map((item) => item.id)).toEqual(["p1", "p1"]);
    expect(planned.fresh).toHaveLength(1);
  });
  it("moves, removes, and completes a record without creating a duplicate", () => {
    const papers = sampleLibrary().papers;
    expect(movePapers(papers, ["p5"], "eval").find((item) => item.id === "p5")?.folderId).toBe("eval");
    expect(removePapers(papers, ["p5", "p6"]).map((item) => item.id)).not.toContain("p5");
    const completed = applyPaperEdits({ ...papers[4], incomplete: true, title: "notes.pdf" }, { title: "Lost in the Middle", authors: "Liu", venue: "TACL", year: 2024, doi: "https://doi.org/10.1162/tacl_a_00600", arxiv: "", tags: ["长上下文", " "] });
    expect(completed).toMatchObject({ title: "Lost in the Middle", incomplete: false, doi: "10.1162/tacl_a_00600", tags: ["长上下文"] });
    expect(applyPaperEdits(papers[0], { title: "", authors: "", venue: "", year: 0, tags: [] }).incomplete).toBe(true);
  });
  it("searches venue, author and tag text", () => {
    const papers = sampleLibrary().papers;
    expect(filterPapers(papers, "all", [], "all", "neurips").map((item) => item.id)).toEqual(["p1", "p3"]);
    expect(filterPapers(papers, "reading", [], "all", "vaswani").map((item) => item.id)).toEqual(["p1"]);
    expect(filterPapers(papers, "all", [], "all", "长上下文").map((item) => item.id)).toEqual(["p5"]);
  });
  it("keeps only important papers in the important smart view", () => {
    expect(filterLibraryView(sampleLibrary().papers, "important").every((paper) => paper.important)).toBe(true);
  });
  it("keeps incomplete and failed papers in the needs-completion smart view", () => {
    expect(filterLibraryView(sampleLibrary().papers, "incomplete").map((paper) => paper.id)).toEqual(["p5"]);
  });
});
