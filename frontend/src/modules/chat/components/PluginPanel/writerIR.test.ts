import { describe, expect, it } from "vitest";
import {
  applyWriterBlockSpanColor,
  convertWriterBlockToParagraph,
  countWriterBlocks,
  createWriterParagraph,
  deleteWriterBlock,
  findWriterBlock,
  findWriterBlockParent,
  getWriterSpanColor,
  getWriterSpanStyles,
  indentWriterBlock,
  insertWriterChildParagraph,
  insertWriterParagraphAfter,
  isWriterDocument,
  liftWriterBlockAfterParent,
  moveWriterBlock,
  normalizeWriterCodeLanguage,
  normalizeWriterDocumentForSync,
  parseWriterDocument,
  relocateWriterBlock,
  repairWriterCodeToolbarPollution,
  sameWriterDocument,
  sameWriterDocumentForSync,
  splitWriterBlock,
  splitWriterHeadingIntoChild,
  toggleWriterBlockInlineStyle,
  updateWriterBlockContent,
  updateWriterBlockFormat,
  updateWriterCodeLanguage,
  updateWriterDocumentTitle,
  writerBackgroundColorHex,
  writerBlockRangeHasInlineStyle,
  writerBlockRangeSpanColor,
  writerTextColorHex,
  type WriterBlock,
  type WriterDocument,
} from "./writerIR";

function makeDoc(blocks: WriterBlock[]): WriterDocument {
  return {
    document_id: "doc-1",
    stage: "draft",
    title: "Untitled",
    blocks,
  };
}

function paragraph(nodeId: string, content: string, extra: Partial<WriterBlock> = {}): WriterBlock {
  return {
    node_id: nodeId,
    type: "paragraph",
    content,
    spans: [{ text: content, style: {} }],
    editable: true,
    ...extra,
  };
}

describe("normalizeWriterCodeLanguage", () => {
  it("maps known aliases to their canonical prism name", () => {
    expect(normalizeWriterCodeLanguage("py")).toBe("python");
    expect(normalizeWriterCodeLanguage("JS")).toBe("javascript");
    expect(normalizeWriterCodeLanguage("yml")).toBe("yaml");
  });

  it("returns 'text' for non-string or empty input", () => {
    expect(normalizeWriterCodeLanguage(undefined)).toBe("text");
    expect(normalizeWriterCodeLanguage("")).toBe("text");
  });

  it("passes through unknown languages unchanged (lowercased)", () => {
    expect(normalizeWriterCodeLanguage("Rust")).toBe("rust");
  });
});

describe("parseWriterDocument / isWriterDocument", () => {
  it("accepts a well-formed document", () => {
    const doc = makeDoc([paragraph("p1", "hello")]);
    const result = parseWriterDocument(doc);
    expect(result.ok).toBe(true);
    expect(result.issues).toEqual([]);
    expect(isWriterDocument(doc)).toBe(true);
  });

  it("collects issues for missing required fields", () => {
    const result = parseWriterDocument({ blocks: [] });
    expect(result.ok).toBe(false);
    expect(result.issues).toContain("document_id must be a non-empty string");
    expect(result.issues).toContain("stage must be a non-empty string");
  });

  it("flags duplicated node_id values", () => {
    const doc = makeDoc([paragraph("dup", "a"), paragraph("dup", "b")]);
    const result = parseWriterDocument(doc);
    expect(result.ok).toBe(false);
    expect(result.issues.some((issue) => issue.includes("duplicated"))).toBe(true);
  });

  it("rejects non-object input", () => {
    expect(isWriterDocument("not an object")).toBe(false);
  });
});

describe("getWriterSpanStyles", () => {
  it("returns the style array directly when present", () => {
    expect(getWriterSpanStyles({ text: "a", style: ["strong"] })).toEqual(["strong"]);
  });

  it("converts a style map to an array of enabled style keys", () => {
    expect(
      getWriterSpanStyles({ text: "a", style: { bold: true, italic: false, inline_code: true } }),
    ).toEqual(["bold", "code"]);
  });

  it("returns an empty array when no styles are set", () => {
    expect(getWriterSpanStyles({ text: "a" })).toEqual([]);
  });
});

describe("normalizeWriterDocumentForSync / sameWriterDocument(ForSync)", () => {
  it("converts legacy array styles into the wire style-map contract", () => {
    const doc = makeDoc([
      { node_id: "p1", type: "paragraph", content: "hi", spans: [{ text: "hi", style: ["strong", "code"] }] },
    ]);
    const normalized = normalizeWriterDocumentForSync(doc);
    expect(normalized.blocks[0].spans?.[0].style).toEqual({ bold: true, inline_code: true });
    expect(normalized.blocks[0].spans?.[0].stype).toBeUndefined();
  });

  it("sameWriterDocument compares by deep equality, not identity", () => {
    const a = makeDoc([paragraph("p1", "x")]);
    const b = makeDoc([paragraph("p1", "x")]);
    expect(sameWriterDocument(a, b)).toBe(true);
    expect(sameWriterDocument(a, null)).toBe(false);
  });

  it("sameWriterDocumentForSync treats legacy-array and normalized-map spans as equal", () => {
    const arrayStyleDoc = makeDoc([
      { node_id: "p1", type: "paragraph", content: "hi", spans: [{ text: "hi", style: ["strong"] }] },
    ]);
    const mapStyleDoc = makeDoc([
      { node_id: "p1", type: "paragraph", content: "hi", spans: [{ text: "hi", style: { bold: true } }] },
    ]);
    expect(sameWriterDocumentForSync(arrayStyleDoc, mapStyleDoc)).toBe(true);
  });
});

describe("countWriterBlocks / findWriterBlock / findWriterBlockParent", () => {
  const child = paragraph("child", "nested");
  const parent = paragraph("parent", "top", { children: [child] });
  const doc = makeDoc([parent]);

  it("counts blocks including nested children", () => {
    expect(countWriterBlocks(doc.blocks)).toBe(2);
  });

  it("finds a block by node_id at any depth", () => {
    expect(findWriterBlock(doc.blocks, "child")).toBe(child);
    expect(findWriterBlock(doc.blocks, "missing")).toBeUndefined();
  });

  it("finds the parent of a nested block", () => {
    expect(findWriterBlockParent(doc.blocks, "child")).toBe(parent);
    expect(findWriterBlockParent(doc.blocks, "parent")).toBeUndefined();
  });
});

describe("updateWriterCodeLanguage", () => {
  it("normalizes and updates the language of an editable code block", () => {
    const doc = makeDoc([{ node_id: "c1", type: "code", content: "print(1)", language: "text", editable: true }]);
    const updated = updateWriterCodeLanguage(doc, "c1", "py");
    expect(findWriterBlock(updated.blocks, "c1")?.language).toBe("python");
  });

  it("leaves the document untouched when the block is not editable", () => {
    const doc = makeDoc([{ node_id: "c1", type: "code", content: "x", language: "text", editable: false }]);
    const updated = updateWriterCodeLanguage(doc, "c1", "py");
    expect(updated).toBe(doc);
  });
});

describe("updateWriterBlockContent", () => {
  it("preserves the leading styled span while typing plain text after it", () => {
    const doc = makeDoc([
      { node_id: "p1", type: "paragraph", content: "AB", spans: [{ text: "A", style: { bold: true } }, { text: "B", style: {} }] },
    ]);
    const updated = updateWriterBlockContent(doc, "p1", "ABC");
    const block = findWriterBlock(updated.blocks, "p1")!;
    expect(block.content).toBe("ABC");
    expect(block.spans?.[0]).toMatchObject({ text: "A", style: { bold: true } });
  });

  it("returns the same document when content is unchanged", () => {
    const doc = makeDoc([paragraph("p1", "same")]);
    expect(updateWriterBlockContent(doc, "p1", "same")).toBe(doc);
  });
});

describe("repairWriterCodeToolbarPollution", () => {
  it("clears code blocks whose content matches the toolbar-pollution pattern", () => {
    const pollutedText = `代码块${[
      "Plain text", "JavaScript", "TypeScript", "JSX", "TSX", "Python", "Java", "Go", "Rust",
      "C", "C++", "C#", "SQL", "Shell", "JSON", "YAML", "HTML / XML", "CSS", "Markdown", "Dockerfile",
    ].join("")}自动换行复制`;
    const doc = makeDoc([{ node_id: "c1", type: "code", content: pollutedText, spans: [{ text: pollutedText }] }]);
    const repaired = repairWriterCodeToolbarPollution(doc);
    expect(findWriterBlock(repaired.blocks, "c1")?.content).toBe("");
  });

  it("leaves normal code content untouched", () => {
    const doc = makeDoc([{ node_id: "c1", type: "code", content: "console.log(1)" }]);
    expect(repairWriterCodeToolbarPollution(doc)).toBe(doc);
  });
});

describe("toggleWriterBlockInlineStyle / writerBlockRangeHasInlineStyle", () => {
  it("enables bold on a plain-text range and reports it as active afterwards", () => {
    const doc = makeDoc([paragraph("p1", "hello world")]);
    const updated = toggleWriterBlockInlineStyle(doc, "p1", 0, 5, "strong");
    const block = findWriterBlock(updated.blocks, "p1")!;
    expect(writerBlockRangeHasInlineStyle(block, 0, 5, "strong")).toBe(true);
  });

  it("toggles bold back off when the whole range is already styled", () => {
    const doc = makeDoc([
      { node_id: "p1", type: "paragraph", content: "hello", spans: [{ text: "hello", style: { bold: true } }] },
    ]);
    const updated = toggleWriterBlockInlineStyle(doc, "p1", 0, 5, "strong");
    const block = findWriterBlock(updated.blocks, "p1")!;
    expect(writerBlockRangeHasInlineStyle(block, 0, 5, "strong")).toBe(false);
  });

  it("ignores an invalid (empty/negative) range", () => {
    const doc = makeDoc([paragraph("p1", "hello")]);
    expect(toggleWriterBlockInlineStyle(doc, "p1", 3, 3, "strong")).toBe(doc);
  });
});

describe("span color helpers", () => {
  it("applies and reads back a uniform color across a range", () => {
    const doc = makeDoc([paragraph("p1", "hello world")]);
    const updated = applyWriterBlockSpanColor(doc, "p1", 0, 5, "text_color", 2);
    const block = findWriterBlock(updated.blocks, "p1")!;
    expect(getWriterSpanColor(block.spans![0], "text_color")).toBe(2);
    expect(writerBlockRangeSpanColor(block, 0, 5, "text_color")).toBe(2);
  });

  it("returns undefined from writerBlockRangeSpanColor when colors are mixed", () => {
    const doc = makeDoc([
      {
        node_id: "p1",
        type: "paragraph",
        content: "AB",
        spans: [
          { text: "A", style: { text_color: 1 } },
          { text: "B", style: { text_color: 2 } },
        ],
      },
    ]);
    const block = findWriterBlock(doc.blocks, "p1")!;
    expect(writerBlockRangeSpanColor(block, 0, 2, "text_color")).toBeUndefined();
  });

  it("maps known palette ids to hex values and returns undefined for invalid ids", () => {
    expect(writerTextColorHex(1)).toBe("#D83931");
    expect(writerTextColorHex(undefined)).toBeUndefined();
    expect(writerBackgroundColorHex(7)).toBe("#DEE0E3");
  });
});

describe("updateWriterBlockFormat", () => {
  it("converts a leaf paragraph into a heading with the requested level", () => {
    const doc = makeDoc([paragraph("p1", "Title")]);
    const updated = updateWriterBlockFormat(doc, "p1", "heading", { headingLevel: 2 });
    const block = findWriterBlock(updated.blocks, "p1")!;
    expect(block.type).toBe("heading");
    expect(block.numbering?.level).toBe(2);
  });

  it("does not convert a block that has children", () => {
    const doc = makeDoc([paragraph("p1", "parent", { children: [paragraph("c1", "child")] })]);
    const updated = updateWriterBlockFormat(doc, "p1", "heading", { headingLevel: 1 });
    expect(updated).toBe(doc);
  });
});

describe("updateWriterDocumentTitle", () => {
  it("updates the title and syncs the editable document root block content", () => {
    const doc: WriterDocument = {
      ...makeDoc([{ node_id: "root", type: "document", content: "Old", editable: true }]),
      metadata: { title: "Old" },
    };
    const updated = updateWriterDocumentTitle(doc, "New Title");
    expect(updated.title).toBe("New Title");
    expect(updated.metadata?.title).toBe("New Title");
    expect(findWriterBlock(updated.blocks, "root")?.content).toBe("New Title");
  });

  it("returns the same document when the title is unchanged", () => {
    const doc = makeDoc([paragraph("p1", "x")]);
    expect(updateWriterDocumentTitle(doc, doc.title)).toBe(doc);
  });
});

describe("createWriterParagraph", () => {
  it("creates an empty, editable paragraph tagged with the given stage", () => {
    const block = createWriterParagraph("draft");
    expect(block.type).toBe("paragraph");
    expect(block.stage).toBe("draft");
    expect(block.editable).toBe(true);
    expect(block.node_id).toBeTruthy();
  });
});

describe("splitWriterBlock", () => {
  it("splits a paragraph's content at the caret into two sibling blocks", () => {
    const doc = makeDoc([paragraph("p1", "hello world")]);
    const { document: updated, insertedNodeId } = splitWriterBlock(doc, "p1", 5);
    expect(insertedNodeId).toBeTruthy();
    expect(updated.blocks).toHaveLength(2);
    expect(updated.blocks[0].content).toBe("hello");
    expect(updated.blocks[1].content).toBe(" world");
  });

  it("does nothing for block types that cannot be split", () => {
    const doc = makeDoc([{ node_id: "c1", type: "code", content: "x" }]);
    const { document: updated } = splitWriterBlock(doc, "c1", 0);
    expect(updated).toBe(doc);
  });
});

describe("indentWriterBlock / liftWriterBlockAfterParent", () => {
  it("nests a block as the last child of its previous sibling", () => {
    const doc = makeDoc([paragraph("p1", "first"), paragraph("p2", "second")]);
    const { document: updated } = indentWriterBlock(doc, "p2");
    expect(updated.blocks).toHaveLength(1);
    expect(updated.blocks[0].children?.[0].node_id).toBe("p2");
  });

  it("lifts a nested block back out to be a sibling of its parent", () => {
    const doc = makeDoc([paragraph("p1", "parent", { children: [paragraph("c1", "child")] })]);
    const { document: updated } = liftWriterBlockAfterParent(doc, "c1");
    expect(updated.blocks.map((b) => b.node_id)).toEqual(["p1", "c1"]);
    expect(updated.blocks[0].children).toEqual([]);
  });
});

describe("insertWriterParagraphAfter / insertWriterChildParagraph", () => {
  it("inserts an empty paragraph right after the given sibling", () => {
    const doc = makeDoc([paragraph("p1", "first")]);
    const { document: updated, insertedNodeId } = insertWriterParagraphAfter(doc, "p1");
    expect(updated.blocks.map((b) => b.node_id)).toEqual(["p1", insertedNodeId]);
  });

  it("inserts an empty paragraph as the first child of the target block", () => {
    const doc = makeDoc([paragraph("p1", "heading text", { type: "heading" } as Partial<WriterBlock>)]);
    const { document: updated, insertedNodeId } = insertWriterChildParagraph(doc, "p1");
    expect(updated.blocks[0].children?.[0].node_id).toBe(insertedNodeId);
  });
});

describe("splitWriterHeadingIntoChild", () => {
  it("keeps the leading text as heading and moves the trailing text into a child paragraph", () => {
    const doc = makeDoc([
      { node_id: "h1", type: "heading", content: "Hello World", spans: [{ text: "Hello World" }] },
    ]);
    const { document: updated } = splitWriterHeadingIntoChild(doc, "h1", 5);
    const heading = findWriterBlock(updated.blocks, "h1")!;
    expect(heading.content).toBe("Hello");
    expect(heading.children?.[0].content).toBe(" World");
  });
});

describe("convertWriterBlockToParagraph / deleteWriterBlock / moveWriterBlock", () => {
  it("converts a heading into a plain paragraph, clearing numbering", () => {
    const doc = makeDoc([{ node_id: "h1", type: "heading", content: "x", numbering: { level: 2 } }]);
    const { document: updated } = convertWriterBlockToParagraph(doc, "h1");
    const block = findWriterBlock(updated.blocks, "h1")!;
    expect(block.type).toBe("paragraph");
    expect(block.numbering?.level).toBeUndefined();
  });

  it("deletes a block from the tree", () => {
    const doc = makeDoc([paragraph("p1", "a"), paragraph("p2", "b")]);
    const updated = deleteWriterBlock(doc, "p1");
    expect(updated.blocks.map((b) => b.node_id)).toEqual(["p2"]);
  });

  it("refuses to delete the document root block", () => {
    const doc = makeDoc([{ node_id: "root", type: "document", content: "x" }]);
    expect(deleteWriterBlock(doc, "root")).toBe(doc);
  });

  it("swaps a block with its next sibling when moved down", () => {
    const doc = makeDoc([paragraph("p1", "a"), paragraph("p2", "b")]);
    const updated = moveWriterBlock(doc, "p1", "down");
    expect(updated.blocks.map((b) => b.node_id)).toEqual(["p2", "p1"]);
  });

  it("does nothing when moving the first block up", () => {
    const doc = makeDoc([paragraph("p1", "a"), paragraph("p2", "b")]);
    expect(moveWriterBlock(doc, "p1", "up")).toBe(doc);
  });
});

describe("relocateWriterBlock", () => {
  it("moves a block to become a child of another block", () => {
    const doc = makeDoc([paragraph("p1", "a"), paragraph("p2", "b")]);
    const updated = relocateWriterBlock(doc, "p2", { type: "child", parentId: "p1" });
    expect(updated.blocks.map((b) => b.node_id)).toEqual(["p1"]);
    expect(updated.blocks[0].children?.[0].node_id).toBe("p2");
  });

  it("moves a block to be a sibling placed after another block", () => {
    const doc = makeDoc([paragraph("p1", "a"), paragraph("p2", "b"), paragraph("p3", "c")]);
    const updated = relocateWriterBlock(doc, "p3", { type: "after", afterId: "p1" });
    expect(updated.blocks.map((b) => b.node_id)).toEqual(["p1", "p3", "p2"]);
  });

  it("refuses to move a block into its own descendant", () => {
    const doc = makeDoc([paragraph("p1", "a", { children: [paragraph("c1", "b")] })]);
    const updated = relocateWriterBlock(doc, "p1", { type: "child", parentId: "c1" });
    expect(updated).toBe(doc);
  });
});
