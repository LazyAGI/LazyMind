import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import PaperGuideReader from "./PaperGuideReader";
import { revision } from "./fixtures";
import { rangeAtUnicodeOffset, visibleTextOffset } from "./readingPosition";
import { sourceTextLength } from "./sourceText";
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
  const select = vi.fn();
  const view = render(
    <PaperGuideReader
      revision={r}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={select}
    />,
  );
  const root = view.container.querySelector(
    '[data-guide-section="section:question:v1"] [data-guide-text]',
  ) as HTMLElement;
  return { ...view, root, select };
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
it.each([
  "cell",
  "math",
  "across",
  "after",
  "before",
  "before-math",
  "after-math",
])(
  "preserves exact raw Unicode source when selecting %s around a table",
  (choice) => {
    const raw = "| A | B |\n| --- | --- |\n| 😀 | \\(x_1\\) |\n| 😀 | same |";
    const prefix = "甲😀 \\(a\\)\n\n",
      suffix = "\n\n尾𐐀文 \\(z\\)";
    const { root, select } = renderTable(prefix + raw + suffix);
    const t = screen.getByRole("table"),
      range = document.createRange();
    let start: number, end: number, expected: string;
    const pre = Array.from(prefix).length,
      size = Array.from(raw).length;
    if (choice === "before-math" || choice === "after-math") {
      const parent =
        choice === "before-math"
          ? (root.firstChild as Element)
          : (root.lastChild as Element);
      const walker = document.createTreeWalker(
          parent.querySelector(".katex-html")!,
          NodeFilter.SHOW_TEXT,
        ),
        node = walker.nextNode()!;
      range.setStart(node, 0);
      range.setEnd(node, 1);
      start = choice === "before-math" ? 3 : pre + size + 6;
      end = start + 5;
      expected = choice === "before-math" ? "\\(a\\)" : "\\(z\\)";
    } else if (choice === "after") {
      const node = root.lastChild!.firstChild!.firstChild!;
      range.setStart(node, 3);
      range.setEnd(node, 5);
      start = pre + size + 3;
      end = start + 1;
      expected = "𐐀";
    } else if (choice === "before") {
      const node = root.firstChild!.firstChild!.firstChild!;
      range.setStart(node, 1);
      range.setEnd(node, 3);
      start = 1;
      end = 2;
      expected = "😀";
    } else if (choice === "across") {
      range.setStart(root.firstChild!.firstChild!.firstChild!, 1);
      range.setEnd(root.lastChild!.firstChild!.firstChild!, 3);
      start = 1;
      end = pre + size + 3;
      expected = Array.from(prefix + raw + suffix)
        .slice(start, end)
        .join("");
    } else {
      const cell =
        choice === "cell"
          ? within(t).getAllByRole("cell")[2]
          : t.querySelector(".katex-html")!;
      const walker = document.createTreeWalker(cell, NodeFilter.SHOW_TEXT),
        node = walker.nextNode()!;
      range.setStart(node, 0);
      range.setEnd(node, 1);
      start = pre;
      end = pre + size;
      expected = raw;
    }
    window.getSelection()!.removeAllRanges();
    window.getSelection()!.addRange(range);
    fireEvent.mouseUp(root);
    const toolbar = screen.getByRole("toolbar");
    expect(
      toolbar.querySelector(".paper-guide-selection-preview")?.textContent,
    ).toBe(expected);
    if (["cell", "math", "across"].includes(choice))
      expect(toolbar).toHaveTextContent(
        "The full table is included to preserve row and column context.",
      );
    fireEvent.click(
      within(toolbar).getByRole("button", { name: "Explain this passage" }),
    );
    expect(select.mock.calls[0][0]).toMatchObject({
      text: expected,
      guideRef: {
        revision_id: "r1",
        section_key: "section:question:v1",
        block_id: "background-block",
        start,
        end,
      },
    });
    expect(t.querySelector("[data-guide-source-start]")).toBeNull();
  },
);
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
  "| A | B | C |\n| --- | --- | --- |\n| $|x|$ | 1 |",
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
it("does not reinterpret pipes in ordinary paragraph blocks", () => {
  renderTable(table, "paragraph");
  expect(screen.queryByRole("table")).toBeNull();
});

it("preserves CRLF and optional outer pipes as a single source interval", () => {
  const raw = "A | B\r\n:--- | ---:\r\n1 | 2";
  const { root, select } = renderTable(raw);
  const range = document.createRange();
  range.selectNodeContents(screen.getAllByRole("cell")[0]);
  window.getSelection()!.removeAllRanges();
  window.getSelection()!.addRange(range);
  fireEvent.mouseUp(root);
  fireEvent.click(
    within(screen.getByRole("toolbar")).getByRole("button", {
      name: "Explain this passage",
    }),
  );
  expect(select.mock.calls[0][0]).toMatchObject({
    text: raw,
    guideRef: { start: 0, end: raw.length },
  });
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

it("preserves attribution, linked source assets, evidence and whole-block keyboard actions", () => {
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
    onSelection = vi.fn(),
    renderAsset = vi.fn(() => <figure aria-label="Original source table" />);
  const view = render(
    <PaperGuideReader
      revision={r}
      title="Paper"
      language="en"
      onEvidence={onEvidence}
      onSelection={onSelection}
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
  fireEvent.click(within(section).getByRole("button", { name: /Source/ }));
  expect(onEvidence).toHaveBeenCalledWith("table-evidence");
  const menu = section.querySelector(".paper-guide-passage-actions")!;
  fireEvent.click(menu.querySelector("summary")!);
  fireEvent.click(
    within(menu as HTMLElement).getByText("Explain this passage"),
  );
  expect(onSelection.mock.calls[0][0]).toMatchObject({
    text: table,
    guideRef: {
      section_key: "section:question:v1",
      start: 0,
      end: Array.from(table).length,
    },
  });
});
