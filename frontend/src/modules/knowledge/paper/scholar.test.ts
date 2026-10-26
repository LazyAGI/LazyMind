import { afterEach, describe, expect, it, vi } from "vitest";
import { lookupDoi } from "./scholar";

afterEach(() => vi.unstubAllGlobals());
const respond = (body: string, status = 200) => vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(body, { status })));
describe("paper metadata import failures", () => {
  it("keeps ordinary DOI lookups on Crossref", async () => {
    respond(JSON.stringify({ message: { title: ["Published paper"], DOI: "10.1234/test" } }));
    await expect(lookupDoi("https://doi.org/10.1234/test")).resolves.toMatchObject({ title: "Published paper", source: "doi", doi: "10.1234/test" });
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

});
