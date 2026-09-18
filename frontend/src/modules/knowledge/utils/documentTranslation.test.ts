import JSZip from "@progress/jszip-esm";
import { describe, expect, it } from "vitest";
import { prepareDocumentTranslation, translatableDocumentExtensions } from "./documentTranslation";

describe("document translation", () => {
  it("preserves text paragraph separators", async () => {
    const source = new TextEncoder().encode("First paragraph\n\nSecond paragraph").buffer;
    const prepared = await prepareDocumentTranslation(source, "txt");
    expect(prepared.units.map((unit) => unit.text)).toEqual(["First paragraph", "Second paragraph"]);
    const output = await prepared.build(new Map(prepared.units.map((unit, index) => [unit.id, `译文${index + 1}`])));
    await expect(output.text()).resolves.toBe("译文1\n\n译文2");
  });

  it("replaces DOCX paragraph text while retaining package entries", async () => {
    const zip = new JSZip();
    zip.file("word/document.xml", `<?xml version="1.0"?><w:document xmlns:w="urn:w"><w:body><w:p><w:r><w:t>Hello </w:t></w:r><w:r><w:t>world</w:t></w:r></w:p></w:body></w:document>`);
    zip.file("word/media/image.png", new Uint8Array([1, 2, 3]));
    const source = await zip.generateAsync({ type: "arraybuffer" });
    const prepared = await prepareDocumentTranslation(source, "docx");
    expect(prepared.units).toEqual([expect.objectContaining({ text: "Hello world" })]);
    const output = await prepared.build(new Map([[prepared.units[0].id, "你好世界"]]));
    const result = await new JSZip().loadAsync(await output.arrayBuffer());
    await expect(result.file("word/document.xml")?.async("string")).resolves.toContain("你好世界");
    expect(result.file("word/media/image.png")).toBeTruthy();
  });

  it("advertises the supported mainstream formats", () => {
    expect(translatableDocumentExtensions).toEqual(expect.arrayContaining(["pdf", "docx", "pptx", "xlsx", "md", "txt", "html"]));
  });
});
