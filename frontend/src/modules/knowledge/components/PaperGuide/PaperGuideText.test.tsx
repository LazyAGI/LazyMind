import { render } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import PaperGuideText, { guideTextSegments } from "./PaperGuideText";
import {
  rangeAtUnicodeOffset,
  restoreTextPosition,
  visibleTextOffset,
} from "./readingPosition";
import { sourceTextLength, sourceBoundary } from "./sourceText";
afterEach(() => vi.restoreAllMocks());
it("preserves ordinary prose, escaped delimiters and malformed formulas exactly", () => {
  const text = String.raw`foo_bar costs \$5; escaped \\(x\\); unfinished \(x and 😀𐐀`;
  const { container } = render(
    <p>
      <PaperGuideText text={text} />
    </p>,
  );
  expect(container.textContent).toBe(text);
  expect(container.querySelector(".katex")).toBeNull();
  expect(guideTextSegments("")).toEqual([]);
});
it("renders both dollar forms while retaining their delimiters in source intervals", () => {
  const { container } = render(
    <p>
      <PaperGuideText text={"😀 $x$ $$x^2$$"} />
    </p>,
  );
  expect(container.querySelectorAll(".katex")).toHaveLength(2);
  expect(container.querySelectorAll(".katex-display")).toHaveLength(1);
  expect(
    guideTextSegments("😀 $x$ $$x^2$$").map(({ text, start, end }) => ({
      text,
      start,
      end,
    })),
  ).toEqual([
    { text: "😀 ", start: 0, end: 2 },
    { text: "$x$", start: 2, end: 5 },
    { text: " ", start: 5, end: 6 },
    { text: "$$x^2$$", start: 6, end: 13 },
  ]);
});
it.each([
  String.raw`\(\htmlStyle{background:url(https://bad.example)}{x}\)`,
  String.raw`\(\url{https://bad.example}\)`,
  String.raw`\(\def\a{\a}\a\)`,
  String.raw`\(\unsupportedcommand{x}\)`,
  "\\(" + "x".repeat(4097) + "\\)",
])("keeps rejected math as literal text without injected elements", (text) => {
  const { container } = render(
    <p>
      <PaperGuideText text={text} />
    </p>,
  );
  expect(container.textContent).toBe(text);
  expect(
    container.querySelector(".katex,a,img,script,iframe,[style]"),
  ).toBeNull();
});
it("caps excessive visual dimensions of otherwise valid TeX", () => {
  const { container } = render(
    <p>
      <PaperGuideText text={String.raw`\(\rule{99999em}{99999em}\)`} />
    </p>,
  );
  expect(container.querySelector(".katex")).not.toBeNull();
  expect(container.innerHTML).not.toContain("99999em;");
});
it("maps progress inside a formula atom and after it without counting MathML or HTML glyphs", () => {
  const view = render(
    <div>
      <p>
        <PaperGuideText text={"甲😀 \\(x_1\\)𐐀尾"} />
      </p>
    </div>,
  );
  const root = view.container.querySelector("p")!;
  const math = root.querySelector("[data-guide-math]")!;
  expect(sourceTextLength(root)).toBe(12);
  const inside = rangeAtUnicodeOffset(root, 7);
  expect(inside.cloneContents().querySelector(".katex")).not.toBeNull();
  expect(rangeAtUnicodeOffset(root, 10).toString()).toBe("𐐀");
  const rangeRect = vi
    .spyOn(Range.prototype, "getBoundingClientRect")
    .mockImplementation(function (this: Range) {
      if (
        this.startContainer === root &&
        root.childNodes[this.startOffset] === math
      )
        return new DOMRect(0, 20, 20, 10);
      return new DOMRect(
        0,
        this.startContainer === root.firstChild!.firstChild ? 0 : 40,
        10,
        10,
      );
    });
  expect(visibleTextOffset(root, 15)).toBe(3);
  expect(visibleTextOffset(root, 35)).toBe(10);
  const scroll = root.parentElement!;
  scroll.scrollTop = 100;
  restoreTextPosition(scroll, root, 7);
  expect(scroll.scrollTop).toBe(96);
  expect(rangeRect).toHaveBeenCalled();
});

it.each(["$$x$", "$$$x$", "甲😀 $$x$ tail", "$$x$ then $y$"])(
  "preserves an unclosed display token and its original selection interval: %s",
  (text) => {
    const { container } = render(
      <p>
        <PaperGuideText text={text} />
      </p>,
    );
    const root = container.querySelector("p")!;
    expect(root.textContent).toBe(text);
    expect(root.querySelector(".katex")).toBeNull();
    expect(guideTextSegments(text)).toEqual([
      {
        text,
        start: 0,
        end: Array.from(text).length,
        latex: undefined,
        display: undefined,
      },
    ]);
    const range = document.createRange();
    range.selectNodeContents(root);
    expect(
      sourceBoundary(root, range.startContainer, range.startOffset, "start"),
    ).toBe(0);
    expect(
      sourceBoundary(root, range.endContainer, range.endOffset, "end"),
    ).toBe(Array.from(text).length);
  },
);
