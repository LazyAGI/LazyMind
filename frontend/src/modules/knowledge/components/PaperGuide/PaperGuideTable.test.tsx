import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import PaperGuideReader from "./PaperGuideReader";
import { revision } from "./fixtures";
import { rangeAtUnicodeOffset, visibleTextOffset } from "./readingPosition";
import { sourceTextLength } from "./sourceText";
import { parseGuideTable } from "./guideTable";
const table =
  "| 条目 | 催化剂／溶剂 | 转化率（%） | e.r. | d.r. |\n| --- | --- | ---: | :---: | --- |\n| 1 | B1／乙醚，基准条件 | >99 | 78.5:21.5 | 12:1 |\n| 2 | B2／乙醚 | 96 | 85:15 | >20:1 |\n| 3 | B3／乙醚 | >99 | 89:11 | 12:1 |\n| 4 | B4／乙醚 | >99 | 95:5 | >20:1 |\n| 5 | B4／甲苯 | >99 | 96:4 | >20:1 |\n| 6 | B4／叔丁基甲基醚（TBME） | >99 | 96.5:3.5 | >20:1 |";
function renderTable(
  text = `表1：模型反应的催化剂与溶剂优化（均为0.1 mmol）\n\n${table}\n\n当前摘录缺少测定脚注，不能补写测定方法。`,
  kind = "table",
) {
  const r = revision();
  r.sections[1].blocks[0] = {
    ...r.sections[1].blocks[0],
    id: r.sections[0].blocks[0].id,
    kind,
    text,
  };
  const view = render(
    <PaperGuideReader
      revision={r}
      title="Paper"
      language="en"
      onEvidence={() => {}}
    />,
  );
  const root = view.container.querySelector(
    '[data-guide-section="section:question:v1"] [data-guide-text]',
  ) as HTMLElement;
  return { ...view, root };
}
it("renders the real five-column six-row comparison as an accessible table with its caveat", () => {
  const { root } = renderTable();
  const t = screen.getByRole("table");
  expect(within(t).getAllByRole("columnheader")).toHaveLength(5);
  expect(within(t).getAllByRole("row")).toHaveLength(7);
  expect(within(t).getAllByRole("cell")).toHaveLength(30);
  expect(within(t).getByRole("cell", { name: "96.5:3.5" })).toBeVisible();
  expect(within(t).getAllByRole("columnheader")[2]).toHaveAttribute(
    "scope",
    "col",
  );
  expect(root).toHaveTextContent("当前摘录缺少测定脚注，不能补写测定方法。");
  expect(t.closest("p")).toBeNull();
  expect(root).toContainElement(t);
  expect(
    screen.getByRole("region", { name: "Scrollable guide table" }),
  ).toHaveAttribute("tabindex", "0");
});
it("renders HTML and link-like table content as inert text and keeps an escaped pipe in its cell", () => {
  const raw =
    '| A | B |\n| --- | --- |\n| <img src="https://bad.example" onerror="alert(1)"> | [click](https://bad.example) |\n| a\\|b | safe |';
  const { root } = renderTable(raw);
  const t = screen.getByRole("table");
  expect(t).toHaveTextContent(
    '<img src="https://bad.example" onerror="alert(1)">',
  );
  expect(within(t).getByRole("cell", { name: "a|b" })).toBeVisible();
  expect(root.querySelector("img,a,script,iframe")).toBeNull();
});
it.each([
  "| A | B |\n| -- | --- |\n| 1 | 2 |",
  "| A | B |\n| --- | --- |\n| 1 |",
  "| A | B |\n| --- | --- |\n| `x|y` | 1 |",
  "```\n| A | B |\n| --- | --- |\n| 1 | 2 |\n```",
])(
  "falls back to exact raw text for invalid or ambiguous tables: %s",
  (raw) => {
    const { root } = renderTable(raw);
    expect(screen.queryByRole("table")).toBeNull();
    expect(root.textContent).toBe(raw);
  },
);
const fallbackPrefix = " 样😀｜分值\r\n甲｜7 \r\n\r\n前😀 ";
const fallbackFormula = String.raw`\(p^2\)`;
const fallbackSuffix = " 𐐀后 \r\n";
const fallbackText = fallbackPrefix + fallbackFormula + fallbackSuffix;

it("renders math after an unparsed full-width grid and preserves its literal rows and newlines", () => {
  const { root } = renderTable(fallbackText);
  expect(screen.queryByRole("table")).toBeNull();
  expect(root.querySelector("[data-guide-atomic]")).toBeNull();
  const fallback = root.firstElementChild!;
  expect(fallback).toHaveClass("paper-guide-table-fallback");
  const math = fallback.querySelector("[data-guide-math]")!;
  expect(math).toBeVisible();
  expect(math.querySelector(".katex-html")).toBeVisible();
  expect(
    math.querySelector('annotation[encoding="application/x-tex"]')?.textContent,
  ).toBe("p^2");
  expect(math.previousSibling?.textContent).toBe(fallbackPrefix);
  expect(math.nextSibling?.textContent).toBe(fallbackSuffix);
  expect(math).toHaveAttribute("data-guide-source-start", "19");
  expect(math).toHaveAttribute("data-guide-source-end", "26");
  expect(sourceTextLength(root)).toBe(32);
});

it("keeps HTML, link syntax, unsafe and invalid TeX literal beside safe fallback math", () => {
  const start = String.raw`列｜值
甲｜<img src="https://bad.example" onerror="alert(1)">
[click](https://bad.example)
\(\href{https://bad.example}{x}\)
`;
  const end = String.raw`
\(\unsupportedcommand{x}\)
unfinished \(x`;
  const { root } = renderTable(start + fallbackFormula + end);
  expect(screen.queryByRole("table")).toBeNull();
  expect(root.querySelector("a,img,script,iframe,[onerror]")).toBeNull();
  const fallback = root.firstElementChild!;
  expect(fallback.querySelectorAll("[data-guide-math]")).toHaveLength(1);
  const literalText = Array.from(fallback.children)
    .filter((child) => !child.hasAttribute("data-guide-math"))
    .map((child) => child.textContent)
    .join("");
  expect(literalText).toBe(start + end);
});

it("does not reinterpret pipes in ordinary paragraph blocks", () => {
  renderTable(table, "paragraph");
  expect(screen.queryByRole("table")).toBeNull();
});

it("keeps over-limit or multiple tables entirely as raw text", () => {
  const wide =
    Array.from({ length: 17 }, (_, i) => "C" + i).join(" | ") +
    "\n" +
    Array(17).fill("---").join(" | ") +
    "\n" +
    Array(17).fill("v").join(" | ");
  const tall =
    "| A | B |\n| --- | --- |\n" +
    Array.from({ length: 101 }, (_, i) => `| ${i} | value |`).join("\n");
  for (const raw of [
    wide,
    tall,
    table + "\n\n" + table,
    table + "x".repeat(16000),
  ]) {
    const view = renderTable(raw);
    expect(screen.queryByRole("table")).toBeNull();
    expect(view.root.textContent).toBe(raw);
    view.unmount();
  }
});
it("uses whole-table geometry for a source offset inside it and resumes exact prose offsets afterwards", () => {
  const raw = "| A | B |\n| --- | --- |\n| 1 | 2 |",
    prefix = "甲😀\n\n",
    suffix = "\n尾𐐀";
  const { root } = renderTable(prefix + raw + suffix);
  const start = 4,
    end = start + Array.from(raw).length;
  expect(sourceTextLength(root)).toBe(end + 3);
  expect(
    rangeAtUnicodeOffset(root, start + 8)
      .cloneContents()
      .querySelector("table"),
  ).not.toBeNull();
  expect(rangeAtUnicodeOffset(root, end + 2).toString()).toBe("𐐀");
  const rect = vi
    .spyOn(Range.prototype, "getBoundingClientRect")
    .mockImplementation(function (this: Range) {
      if (
        this.startContainer === root &&
        (root.childNodes[this.startOffset] as Element)?.getAttribute?.(
          "data-guide-atomic",
        ) === "table"
      )
        return new DOMRect(0, 20, 500, 200);
      return new DOMRect(
        0,
        root.firstChild!.contains(this.startContainer) ? 0 : 250,
        20,
        10,
      );
    });
  try {
    expect(visibleTextOffset(root, 50)).toBe(start);
    expect(visibleTextOffset(root, 230)).toBe(end);
  } finally {
    rect.mockRestore();
  }
});

it("preserves attribution, linked source assets, evidence", () => {
  const r = revision(),
    block = r.sections[1].blocks[0];
  Object.assign(block, {
    kind: "table",
    text: table,
    asset_id: "table-asset",
    evidence_ids: ["table-evidence"],
    attribution: "paper_statement",
  });
  const onEvidence = vi.fn(),
    renderAsset = vi.fn(() => <figure aria-label="Original source table" />);
  const view = render(
    <PaperGuideReader
      revision={r}
      title="Paper"
      language="en"
      onEvidence={onEvidence}
      renderAsset={renderAsset}
    />,
  );
  const section = view.container.querySelector(
    '[data-guide-section="section:question:v1"]',
  ) as HTMLElement;
  expect(section).toHaveTextContent("Paper statement · guide paraphrase");
  expect(
    within(section).getByRole("figure", { name: "Original source table" }),
  ).toBeVisible();
  expect(renderAsset).toHaveBeenCalledWith(block);
  const citation = within(section).getByRole("button", { name: /Source/ });
  fireEvent.click(citation);
  expect(onEvidence).toHaveBeenCalledWith("table-evidence", citation);
});

it.each([
  "说明\n\n    | A | B |\n    | --- | --- |\n    | 1 | 2 |",
  "\t| A | B |\n\t| --- | --- |\n\t| 1 | 2 |",
  "| A | B |\n| --- | --- |\n| 1 | 2 |\n    | 3 | 4 |",
  "| A | B |\n| --- | --- |\n| 1 | 2 |\n3\n| 4 | 5 |",
  "| A | B |\n| --- | --- |\n| 1 | 2 |\n3\nextra\n4 | 5",
])(
  "preserves the entire raw candidate when its context or body is ambiguous: %s",
  (raw) => {
    const { root } = renderTable(raw);
    expect(screen.queryByRole("table")).toBeNull();
    expect(root.textContent).toBe(raw);
    expect(sourceTextLength(root)).toBe(Array.from(raw).length);
  },
);

it("keeps complete multiline mathematics outside a real table and its footnote", () => {
  const prefix = "\\[\na+b\n\\]\n\n",
    suffix = "\n脚注保持原文。\n\n\\[\nc+d\n\\]";
  const { root } = renderTable(prefix + table + suffix);
  expect(screen.getAllByRole("row")).toHaveLength(7);
  expect(root.querySelectorAll("[data-guide-math]")).toHaveLength(2);
  expect(root).toHaveTextContent("脚注保持原文。");
  const atom = root.querySelector('[data-guide-atomic="table"]')!;
  expect(atom).toHaveAttribute(
    "data-guide-source-start",
    String(Array.from(prefix).length),
  );
  expect(atom).toHaveAttribute(
    "data-guide-source-end",
    String(Array.from(prefix + table).length),
  );
});

it.each(["", "甲😀\n\n"])(
  "preserves a table followed immediately by a footnote and complete pipe-containing formula (prefix=%s)",
  (prefix) => {
    const raw = "| A | B |\n| --- | --- |\n| 1 | 2 |",
      formula = "\\[\n|x|\n\\]",
      footnote = "\nFootnote.\n";
    const { root } = renderTable(prefix + raw + footnote + formula);
    expect(screen.getAllByRole("table")).toHaveLength(1);
    const atom = root.querySelector('[data-guide-atomic="table"]')!;
    expect(atom).toHaveAttribute(
      "data-guide-source-start",
      String(Array.from(prefix).length),
    );
    expect(atom).toHaveAttribute(
      "data-guide-source-end",
      String(Array.from(prefix + raw).length),
    );
    expect(root.querySelector("[data-guide-math]")).not.toBeNull();
    expect(root).toHaveTextContent("Footnote.");
  },
);

it.each([
  {
    context: "a math pipe makes cell boundaries ambiguous",
    prefix: "| A | B | C |\n| --- | --- | --- |\n| ",
    formula: "$|x|$",
    latex: "|x|",
    suffix: " | 1 |",
  },
  {
    context: "a complete bracket display wraps the table-shaped lines",
    prefix: "",
    formula: "\\[\n| A | B |\n| --- | --- |\n| 1 | 2 |\n\\]",
    latex: "\n| A | B |\n| --- | --- |\n| 1 | 2 |\n",
    suffix: "",
  },
  {
    context: "a complete dollar display wraps the table-shaped lines",
    prefix: "",
    formula: "$$\n| A | B |\n| --- | --- |\n| 1 | 2 |\n$$",
    latex: "\n| A | B |\n| --- | --- |\n| 1 | 2 |\n",
    suffix: "",
  },
  {
    context: "a broken row resumes outside a complete formula",
    prefix: "| A | B |\n| --- | --- |\n| 1 | 2 |\n3\n",
    formula: "\\[\n|x|\n\\]",
    latex: "\n|x|\n",
    suffix: "\n| 4 | 5 |",
  },
])(
  "preserves the exact rejected table source when $context",
  ({ prefix, formula, latex, suffix }) => {
    const raw = prefix + formula + suffix;
    const { root } = renderTable(raw);
    expect(screen.queryByRole("table")).toBeNull();
    const math = root.querySelector("[data-guide-math]")!;
    expect(math.querySelector("annotation")?.textContent).toBe(latex);
    expect(math.previousSibling?.textContent ?? "").toBe(prefix);
    expect(math.nextSibling?.textContent ?? "").toBe(suffix);
  },
);

it("preserves Unicode and CRLF intervals near the text limit through a long suffix scan", () => {
  const prefix = "甲😀\r\n\r\n";
  const grid = "😀 | B\r\n--- | ---\r\nA | 𐐀";
  const longSuffix = "\r\n" + "x\n".repeat(7950) + "\\[\r\n𐐀|x|\r\n\\]";
  const suffix = longSuffix.padEnd(16000 - prefix.length - grid.length, "x");
  const raw = prefix + grid + suffix;
  expect(parseGuideTable(raw)).toMatchObject({
    start: 6,
    end: 29,
    prefix,
    suffix,
    headers: ["😀", "B"],
    rows: [["A", "𐐀"]],
  });
  expect(parseGuideTable(prefix + grid + longSuffix + "\r\n| 5 | 6 |")).toBeUndefined();
  expect(parseGuideTable(raw + "x")).toBeUndefined();
});
