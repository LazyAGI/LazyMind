import { describe, expect, it } from "vitest";
import { isArxivInput } from "./arxivImport";

describe("arXiv candidate routing", () => {
  it.each([
    "2404.06654", "2404.06654v3", "2404.06654v3.pdf", "arXiv:2404.06654v3 [cs.CL]",
    "hep-th/9901001", "math.GT/0309136v1", "https://arxiv.org/abs/2404.06654",
    "https://arxiv.org/pdf/2404.06654v3.pdf?download=1#page=2", "https://arxiv.org/html/2404.06654v3",
    "https://export.arxiv.org/abs/hep-th/9901001", "https://doi.org/10.48550/arXiv.2404.06654",
    "[RULER](https://arxiv.org/abs/2404.06654)", "arxiv.org/pdf/2404.06654",
  ])("routes %s to backend parsing", (input) => {
    expect(isArxivInput(input)).toBe(true);
  });

  it.each(["10.18653/v1/n19-1423", "https://example.com/paper/2404.06654", "https://arxiv.org.evil.test/pdf/2404.06654", "https://evil-arxiv.org/pdf/2404.06654", "ordinary article title", ""]) ("does not mistake %s for arXiv", (input) => {
    expect(isArxivInput(input)).toBe(false);
  });
});
