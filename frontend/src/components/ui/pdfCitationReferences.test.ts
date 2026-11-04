import { afterEach, describe, expect, it } from "vitest";
import { citationAtPoint, citationForPointerActivation, citationOccurrences, groupPdfCitationTargets } from "./pdfCitationReferences";

const rectDescriptor = Object.getOwnPropertyDescriptor(Range.prototype, "getClientRects");
afterEach(() => {
  if (rectDescriptor) Object.defineProperty(Range.prototype, "getClientRects", rectDescriptor);
  else Reflect.deleteProperty(Range.prototype, "getClientRects");
});

function pageWithEachCharacterInASpan(text: string) {
  const page = document.createElement("div");
  const layer = document.createElement("div");
  layer.className = "react-pdf__Page__textContent";
  for (const character of text) {
    const span = document.createElement("span");
    span.textContent = character;
    layer.append(span);
  }
  page.append(layer);
  return page;
}

function mockRangeRects(text: string, rects = [{ left: 10, right: 60, top: 10, bottom: 30 }]) {
  Object.defineProperty(Range.prototype, "getClientRects", {
    configurable: true,
    value(this: Range) {
      return this.toString() === text ? rects : [];
    },
  });
}

const targets = groupPdfCitationTargets([
  { referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download", page: 8, bbox: [20, 60, 300, 84] },
]);

describe("citation target grouping", () => {
  it("groups all regions for one bibliography key in document order", () => {
    expect(groupPdfCitationTargets([
      { referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download", page: 8, bbox: [20, 60, 300, 84] },
      { referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download", page: 8, bbox: [20, 86, 320, 108] },
    ])[0].regions).toEqual([
      { page: 8, bbox: [20, 60, 300, 84] },
      { page: 8, bbox: [20, 86, 320, 108] },
    ]);
  });

  it("keeps a bibliography entry's left-column ending before its right-column continuation", () => {
    expect(groupPdfCitationTargets([
      { referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download", page: 8, bbox: [50, 700, 280, 730] },
      { referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download", page: 8, bbox: [320, 60, 550, 95] },
    ])[0].regions).toEqual([
      { page: 8, bbox: [50, 700, 280, 730] },
      { page: 8, bbox: [320, 60, 550, 95] },
    ]);
  });
});

describe("cross-span citation resolution", () => {
  it("exposes each real citation occurrence for separate keyboard focus without changing text", () => {
    const page = pageWithEachCharacterInASpan("[12] and [12]");
    const before = page.innerHTML;
    mockRangeRects("[12]");
    expect(citationOccurrences(page, targets).map(hit => hit.target.referenceKey)).toEqual(["12", "12"]);
    expect(page.innerHTML).toBe(before);
  });

  it("leaves every native anchor to its own keyboard and pointer behavior", () => {
    const page = document.createElement("div");
    page.innerHTML = '<div class="react-pdf__Page__textContent"><a href="#reference"><span>[12]</span></a></div>';
    mockRangeRects("[12]");
    expect(citationOccurrences(page, targets)).toEqual([]);
  });
  it.each(["[12]", "【12】", "[ 0012 ]", "【\n12\t】"])("finds %s split across text spans without changing the DOM", marker => {
    const page = pageWithEachCharacterInASpan(`See ${marker} for details`);
    const before = page.innerHTML;
    mockRangeRects(marker);
    expect(citationAtPoint(page, targets, 20, 20)?.referenceKey).toBe("12");
    expect(citationAtPoint(page, targets, 80, 20)).toBeUndefined();
    expect(page.innerHTML).toBe(before);
  });

  it("indexes nested and direct text nodes exactly once", () => {
    const page = document.createElement("div");
    page.innerHTML = '<div class="react-pdf__Page__textContent">See <span>[<span>1</span></span><span>2]</span> now</div>';
    mockRangeRects("[12]");
    expect(citationAtPoint(page, targets, 20, 20)?.referenceId).toBe("r12");
  });

  it("uses every current adjacent range rectangle after layout changes", () => {
    const page = pageWithEachCharacterInASpan("[12]");
    const rects = [
      { left: 10, right: 30, top: 10, bottom: 30 },
      { left: 30, right: 50, top: 10, bottom: 30 },
    ];
    mockRangeRects("[12]", rects);
    expect(citationAtPoint(page, targets, 40, 20)?.referenceKey).toBe("12");
    rects.splice(0, rects.length, { left: 100, right: 150, top: 100, bottom: 130 });
    expect(citationAtPoint(page, targets, 40, 20)).toBeUndefined();
    expect(citationAtPoint(page, targets, 110, 110)?.referenceKey).toBe("12");
  });

  it("does not join citation digits across an explicit BR boundary", () => {
    const page = document.createElement("div");
    page.innerHTML = '<div class="react-pdf__Page__textContent"><span>[1</span><br><span>2]</span></div>';
    const before = page.innerHTML;
    mockRangeRects("[12]");
    expect(citationAtPoint(page, targets, 20, 20)).toBeUndefined();
    expect(page.innerHTML).toBe(before);
  });

  it.each([
    { name: "distant columns on the same line", rect: { left: 500, right: 515, top: 10, bottom: 30 } },
    { name: "different visual lines", rect: { left: 25, right: 40, top: 40, bottom: 60 } },
    { name: "reversed horizontal reading order", rect: { left: -5, right: 10, top: 10, bottom: 30 } },
  ])("does not join citation fragments in $name", ({ rect }) => {
    const page = document.createElement("div");
    page.innerHTML = '<div class="react-pdf__Page__textContent"><span>[1</span><span>2]</span></div>';
    mockRangeRects("[12]", [{ left: 10, right: 25, top: 10, bottom: 30 }, rect]);
    expect(citationAtPoint(page, targets, 20, 20)).toBeUndefined();
    expect(citationAtPoint(page, targets, rect.left + 5, rect.top + 5)).toBeUndefined();
  });

  it.each([0.5, 1, 2])("accepts adjacent cross-span fragments at layout scale %s", scale => {
    const page = document.createElement("div");
    page.innerHTML = '<div class="react-pdf__Page__textContent"><span>[1</span><span>2]</span></div>';
    mockRangeRects("[12]", [
      { left: 10 * scale, right: 25 * scale, top: 10 * scale, bottom: 30 * scale },
      { left: 31 * scale, right: 40 * scale, top: 11 * scale, bottom: 31 * scale },
    ]);
    expect(citationAtPoint(page, targets, 20 * scale, 20 * scale)?.referenceKey).toBe("12");
    expect(citationAtPoint(page, targets, 35 * scale, 20 * scale)?.referenceKey).toBe("12");
  });

  it.each(["[404]", "[12345]", "(12)", "[1, 12]", "[1-12]", "【12]", "[12】"])("ignores unknown or unsupported marker %s", marker => {
    mockRangeRects(marker);
    expect(citationAtPoint(pageWithEachCharacterInASpan(marker), targets, 20, 20)).toBeUndefined();
  });

  it("ignores text outside the PDF text layer", () => {
    const page = document.createElement("div");
    page.textContent = "[12]";
    mockRangeRects("[12]");
    expect(citationAtPoint(page, targets, 20, 20)).toBeUndefined();
  });
});

describe("citation pointer activation", () => {
  const start = { x: 20, y: 20 };
  const click = (options: MouseEventInit = {}) => new MouseEvent("click", { clientX: 20, clientY: 20, ...options });
  const activate = (page: HTMLElement, target: Element, event = click(), selection = "", pointerStart: typeof start | undefined = start) =>
    citationForPointerActivation(page, targets, target, event, selection, pointerStart);

  it("activates a plain left click on a known citation", () => {
    const page = pageWithEachCharacterInASpan("[12]");
    mockRangeRects("[12]");
    expect(activate(page, page.querySelector("span")!)?.referenceKey).toBe("12");
  });

  it("preserves external and internal native annotation link activation", () => {
    const page = pageWithEachCharacterInASpan("[12]");
    page.insertAdjacentHTML("beforeend", '<a href="https://example.com"><span>external</span></a><section data-internal-link><a href="#ref12"><span>internal</span></a></section>');
    mockRangeRects("[12]");
    for (const anchorChild of page.querySelectorAll("a span")) {
      expect(activate(page, anchorChild)).toBeUndefined();
    }
  });

  it.each(["button", "input", "textarea", "select"])("preserves %s controls", tag => {
    const page = pageWithEachCharacterInASpan("[12]");
    const control = document.createElement(tag);
    page.append(control);
    mockRangeRects("[12]");
    expect(activate(page, control)).toBeUndefined();
  });

  it.each<MouseEventInit>([{ button: 1 }, { button: 2 }, { ctrlKey: true }, { metaKey: true }, { shiftKey: true }, { altKey: true }])("preserves non-left or modified clicks %j", options => {
    const page = pageWithEachCharacterInASpan("[12]");
    mockRangeRects("[12]");
    expect(activate(page, page, click(options))).toBeUndefined();
  });

  it("preserves selection, dragging, and clicks without a pointerdown", () => {
    const page = pageWithEachCharacterInASpan("[12]");
    mockRangeRects("[12]");
    expect(activate(page, page, click(), "selected")).toBeUndefined();
    expect(activate(page, page, click(), "", { x: 10, y: 20 })).toBeUndefined();
    expect(citationForPointerActivation(page, targets, page, click(), "")).toBeUndefined();
    expect(activate(page, page, click(), "", { x: 16, y: 20 })?.referenceKey).toBe("12");
  });
});
