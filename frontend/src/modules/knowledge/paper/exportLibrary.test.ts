import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadPaperCitations } from "./exportLibrary";
import type { PaperItem } from "./model";
import { loadPaperLibrary, savePaperLibrary } from "./store";

const paper = (id: string, title: string): PaperItem => ({
  id, title, authors: "Ada Lovelace", venue: "Research Journal", year: 2026,
  folderId: "folder-one", tags: ["Research"], status: "unread", important: false,
  source: "local", parse: "ready", doi: `10.1234/${id}`,
});

async function readBlob(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(blob);
  });
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("library-level citation downloads", () => {
  it.each(["bib", "ris", "csv"] as const)("exports only the requested library in %s format", async (format) => {
    const browserUrl = URL;
    const createObjectURL = vi.fn<(object: Blob | MediaSource) => string>(() => "blob:library-download");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", class extends browserUrl {
      static createObjectURL = createObjectURL;
      static revokeObjectURL = revokeObjectURL;
    });
    let filename = "";
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      filename = this.download;
    });
    savePaperLibrary("alpha", { folders: [{ id: "folder-one", name: "Alpha Folder" }], papers: [paper("alpha", "Unique Alpha Paper")] });
    savePaperLibrary("beta", { folders: [], papers: [paper("beta", "Unique Beta Paper")] });

    downloadPaperCitations(loadPaperLibrary("alpha"), format, "研究:资料");

    expect(filename).toBe(`研究_资料.${format}`);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    const blob = createObjectURL.mock.calls[0][0] as Blob;
    const content = await readBlob(blob);
    expect(content).toContain("Unique Alpha Paper");
    expect(content).not.toContain("Unique Beta Paper");
    expect(content).toContain("10.1234/alpha");
    if (format === "bib") expect(content).toContain("@article{");
    if (format === "ris") expect(content).toContain("TY  - JOUR");
    if (format === "csv") expect(content).toContain("Alpha Folder");
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:library-download");
    expect(document.querySelector('a[download]')).toBeNull();
  });
});
