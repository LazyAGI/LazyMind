import { beforeEach, describe, expect, it, vi } from "vitest";
import { pdfjs } from "react-pdf";
import { lookupDoi } from "./scholar";
import { paperFromPdf } from "./pdfMeta";

vi.mock("react-pdf", () => ({ pdfjs: { GlobalWorkerOptions: { workerSrc: "test-worker" }, getDocument: vi.fn() } }));
vi.mock("./scholar", () => ({ lookupDoi: vi.fn() }));
const file = { name: "research.pdf", arrayBuffer: async () => new ArrayBuffer(0) } as File;
beforeEach(() => vi.resetAllMocks());

describe("PDF import errors", () => {
  it("keeps a locally readable PDF available for manual review when DOI verification fails", async () => {
    vi.mocked(pdfjs.getDocument).mockReturnValue({ promise: Promise.resolve({
      destroy: vi.fn(),
      getMetadata: async () => ({ info: { Title: "Research" } }),
      getPage: async () => ({ getTextContent: async () => ({ items: [{ str: "10.1234/research 2024" }] }) }),
    }) } as never);
    const failure = new Error("Crossref HTTP 503");
    vi.mocked(lookupDoi).mockRejectedValue(failure);
    await expect(paperFromPdf(file)).resolves.toMatchObject({ title: "Research", identityVerified: false, identificationError: "Crossref HTTP 503" });
  });
  it("propagates corrupt PDF errors", async () => {
    const failure = new Error("Invalid PDF structure");
    vi.mocked(pdfjs.getDocument).mockReturnValue({ promise: Promise.reject(failure) } as never);
    await expect(paperFromPdf(file)).rejects.toBe(failure);
  });
});
