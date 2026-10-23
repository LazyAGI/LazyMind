import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { lookupArxiv, lookupDoi, lookupIdentifier } from "./scholar";
import { ArxivImportError, requestArxivMetadata, type ArxivMetadata } from "./arxivImport";

vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { post: vi.fn(), get: vi.fn() } }));
vi.mock("./arxivImport", async (importOriginal) => ({
  ...await importOriginal<typeof import("./arxivImport")>(),
  requestArxivMetadata: vi.fn(),
}));

afterEach(() => vi.unstubAllGlobals());
const respond = (body: string, status = 200) => vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(body, { status })));
const arxivMetadata: ArxivMetadata = {
  title: "Research & Evidence", authors: ["A. Author", "B. Author", "C. Author", "D. Author"],
  year: 2024, abstract: "Verified abstract from arXiv", doi: "10.1234/published-paper", venue: "arXiv",
  base_id: "2404.06654", version: "v3", abs_url: "https://arxiv.org/abs/2404.06654v3", pdf_url: "https://arxiv.org/pdf/2404.06654v3",
};
beforeEach(() => {
  vi.mocked(requestArxivMetadata).mockReset().mockResolvedValue(arxivMetadata);
});

describe("paper metadata import failures", () => {
  it("propagates backend arXiv failures instead of returning a placeholder", async () => {
    const failure = new ArxivImportError("arXiv 服务暂时不可用", { code: "ARXIV_UPSTREAM_FAILED", status: 503 });
    vi.mocked(requestArxivMetadata).mockRejectedValueOnce(failure);
    await expect(lookupArxiv("2404.06654")).rejects.toBe(failure);
  });

  it.each([
    "2404.06654", "arXiv:2404.06654v3", "https://arxiv.org/abs/2404.06654",
    "https://arxiv.org/pdf/2404.06654v3.pdf?download=1#page=4", "  https://arxiv.org/html/2404.06654v3  ",
    "https://doi.org/10.48550/arXiv.2404.06654", "10.48550/arXiv.2404.06654",
  ])("passes original arXiv input %s to the backend without losing its version", async (input) => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    await expect(lookupIdentifier(input)).resolves.toMatchObject({
      id: "arxiv-2404.06654", title: "Research & Evidence", arxiv: "2404.06654", arxivVersion: "v3",
      year: 2024, parse: "ready", abstract: arxivMetadata.abstract, sourceUrl: arxivMetadata.pdf_url,
      doi: "10.1234/published-paper", authors: "A. Author、B. Author、C. Author、D. Author",
    });
    expect(requestArxivMetadata).toHaveBeenCalledOnce();
    expect(requestArxivMetadata).toHaveBeenCalledWith(input);
    expect(fetch).not.toHaveBeenCalled();
  });

  it("keeps ordinary DOI lookups on Crossref", async () => {
    respond(JSON.stringify({ message: { title: ["Published paper"], DOI: "10.1234/test" } }));
    await expect(lookupIdentifier("https://doi.org/10.1234/test")).resolves.toMatchObject({ title: "Published paper", source: "doi", doi: "10.1234/test" });
    expect(requestArxivMetadata).not.toHaveBeenCalled();
    expect(fetch).toHaveBeenCalledWith("https://api.crossref.org/works/10.1234%2Ftest");
  });
  it("rejects Crossref missing metadata instead of fabricating a DOI title", async () => {
    respond(JSON.stringify({ message: {} }));
    await expect(lookupDoi("10.1234/test")).rejects.toThrow();
  });
  it("keeps a DOI HTTP error distinct from an empty search", async () => {
    respond("rate limit", 429);
    await expect(lookupDoi("10.1234/test")).rejects.toThrow(/429/);
  });
});

describe("provider citation counts", () => {
  it.each([23, 0])("preserves Crossref inbound citation count %s", async (citationCount) => {
    respond(JSON.stringify({ message: { title: ["A real paper"], "is-referenced-by-count": citationCount, "reference-count": 99 } }));
    expect(await lookupDoi("10.1234/test")).toHaveProperty("citationCount", citationCount);
  });

  it.each([undefined, null, -1, 1.5, "7"])("does not invent a count from missing or invalid provider value %s", async (citationCount) => {
    respond(JSON.stringify({ message: { title: ["A real paper"], "is-referenced-by-count": citationCount, "reference-count": 99 } }));
    expect((await lookupDoi("10.1234/test")).citationCount).toBeUndefined();
  });

  it("keeps an arXiv paper's citation count unknown", async () => {
    expect((await lookupArxiv("2404.06654")).citationCount).toBeUndefined();
  });
});
