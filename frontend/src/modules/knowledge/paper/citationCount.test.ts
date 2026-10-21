import { afterEach, expect, it } from "vitest";
import { parseLibraryBackup, serializeLibraryBackup } from "./exportLibrary";
import { asLegacyLibrary, migratePaperLibrary } from "./libraryModel";
import { normalizeLibrary, type PaperItem } from "./model";
import { loadPreviewPaperLibrary, savePreviewPaperLibrary } from "./store";

afterEach(() => localStorage.clear());

it("preserves known, zero, and unknown citation counts through migration, storage and backup", () => {
  const base: PaperItem = { id: "paper", title: "Paper", authors: "A. Author", venue: "Journal", year: 2024, folderId: null, status: "unread", important: false, tags: [], source: "doi", parse: "ready" };
  const library = normalizeLibrary({ folders: [], papers: [
    { ...base, id: "cited", citationCount: 42 },
    { ...base, id: "uncited", citationCount: 0 },
    { ...base, id: "unknown" },
  ] });
  const modern = migratePaperLibrary(library);
  const backup = parseLibraryBackup(serializeLibraryBackup(modern));
  savePreviewPaperLibrary("citation-roundtrip", backup);
  for (const restored of [asLegacyLibrary(backup), asLegacyLibrary(loadPreviewPaperLibrary("citation-roundtrip"))]) {
    expect(restored.papers.map((paper) => paper.citationCount)).toEqual([42, 0, undefined]);
    expect(restored.papers[2]).not.toHaveProperty("citationCount");
  }
});
