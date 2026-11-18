import { act, render, screen, fireEvent, within } from "@testing-library/react";
import type { PaperGuideClaim, PaperGuideInteraction, PaperGuidePlan } from "@/api/generated/core-client";
import { expect, it, vi } from "vitest";
import PaperGuideReader from "./PaperGuideReader";
import { revision } from "./fixtures";

function groundedClaim(id: string, ...evidenceIds: string[]): PaperGuideClaim {
  return {
    id,
    argument_type: null,
    attribution: "paper_statement",
    conditions: [],
    evidence_ids: evidenceIds,
    statement: "The paper supports this statement.",
    support_status: "supported",
  };
}

function planWithClaims(...claims: PaperGuideClaim[]): PaperGuidePlan {
  return {
    claims,
    profiles: { primary: "method", secondary: [], uncertainty_reason: "" },
    questions: [],
    required_assets: [],
    sections: [],
    term_policy: [],
  };
}

it.each([
  { origin: "plan", historical: false },
  { origin: "section", historical: false },
  { origin: "section", historical: true },
])("opens claim-only sources from the $origin (historical: $historical)", ({ origin, historical }) => {
  const r = revision();
  const section = r.sections[0];
  const block = section.blocks[0];
  const claim = groundedClaim("c1", "claim-evidence");
  block.claim_ids = [claim.id];
  block.evidence_ids = [];
  block.text = String.raw`The paper gives \(a = b\).`;
  section.claim_ids = [claim.id];
  if (origin === "plan") r.plan = planWithClaims(claim);
  else section.claims = [claim];
  if (historical) {
    r.status = "superseded";
    r.source_current = false;
  }
  const open = vi.fn();
  render(
    <PaperGuideReader revision={r} title="Paper" language="en" onEvidence={open} onSelection={() => {}} />,
  );

  const passage = within(screen.getByRole("region", { name: section.title }));
  const citation = passage.queryByRole("button", { name: "Source 1" });
  expect(citation).toBeVisible();
  fireEvent.click(citation!);
  expect(open).toHaveBeenLastCalledWith("claim-evidence", citation);
  fireEvent.click(passage.getByLabelText("Passage actions"));
  const viewSource = passage.getByRole("button", { name: "View source" });
  fireEvent.click(viewSource);
  expect(open).toHaveBeenLastCalledWith("claim-evidence", viewSource);
  expect(passage.getByText(/Guide equation explanation/)).toBeVisible();
});

it("numbers direct and inherited sources once and only inherits referenced claims from the plan or current section", () => {
  const r = revision();
  const [first, second] = r.sections;
  r.plan = planWithClaims(
    groundedClaim("p1", "shared", "plan-first"),
    groundedClaim("p2", "plan-second"),
    groundedClaim("unreferenced", "unused-evidence"),
  );
  first.claim_ids = ["p2", "local", "p1"];
  first.claims = [groundedClaim("local", "plan-first", "local-evidence")];
  first.blocks[0].claim_ids = ["p2", "missing", "local", "p1", "other-section"];
  first.blocks[0].evidence_ids = ["direct", "shared"];
  second.claim_ids = ["p1", "other-section"];
  second.claims = [groundedClaim("other-section", "other-evidence")];
  second.blocks[0].claim_ids = ["p1", "other-section"];
  const beforeRender = JSON.stringify(r);
  const open = vi.fn();
  render(
    <PaperGuideReader revision={r} title="Paper" language="en" onEvidence={open} onSelection={() => {}} />,
  );

  const citationsIn = (name: string) => within(screen.getByRole("region", { name }))
    .queryAllByRole("button", { name: /^Source \d+$/ });
  const firstCitations = citationsIn(first.title);
  const secondCitations = citationsIn(second.title);
  expect(firstCitations.map((button) => button.textContent)).toEqual([
    "Source 1", "Source 2", "Source 3", "Source 4", "Source 5",
  ]);
  expect(secondCitations.map((button) => button.textContent)).toEqual([
    "Source 2", "Source 3", "Source 6",
  ]);
  [...firstCitations, ...secondCitations].forEach((button) => fireEvent.click(button));
  expect(open.mock.calls.map(([id]) => id)).toEqual([
    "direct", "shared", "plan-first", "plan-second", "local-evidence",
    "shared", "plan-first", "other-evidence",
  ]);
  expect(JSON.stringify(r)).toBe(beforeRender);
});

it("keeps a paragraph readable when its referenced claim has no available evidence", () => {
  const r = revision();
  const section = r.sections[0];
  const block = section.blocks[0];
  block.claim_ids = ["unavailable"];
  block.evidence_ids = [];
  section.claim_ids = ["unavailable"];
  // An empty Go slice can arrive as null in a historical payload.
  section.claims = JSON.parse(JSON.stringify([{
    ...groundedClaim("unavailable"),
    support_status: "unavailable",
    evidence_ids: null,
  }]));
  render(
    <PaperGuideReader revision={r} title="Paper" language="en" onEvidence={() => {}} onSelection={() => {}} />,
  );

  const passage = within(screen.getByRole("region", { name: section.title }));
  expect(passage.getByText(block.text)).toBeVisible();
  expect(passage.queryByRole("button", { name: /^Source \d+$/ })).not.toBeInTheDocument();
  fireEvent.click(passage.getByLabelText("Passage actions"));
  expect(passage.queryByRole("button", { name: "View source" })).not.toBeInTheDocument();
});

it("opens inherited evidence from a Unicode selection and sends the original passage reference to chat", () => {
  const r = revision();
  const section = r.sections[0];
  const block = section.blocks[0];
  block.claim_ids = ["c1"];
  block.evidence_ids = [];
  block.text = "甲😀乙𐐀丙";
  section.claim_ids = ["c1"];
  section.claims = [groundedClaim("c1", "selection-evidence")];
  const open = vi.fn();
  const choose = vi.fn();
  render(
    <PaperGuideReader revision={r} title="Paper" language="en" onEvidence={open} onSelection={choose} />,
  );

  const text = screen.getByText("甲😀乙𐐀丙");
  const range = document.createRange();
  range.setStart(text.firstChild!, 1);
  range.setEnd(text.firstChild!, 6);
  window.getSelection()!.removeAllRanges();
  window.getSelection()!.addRange(range);
  fireEvent.mouseUp(text);
  const source = within(screen.getByRole("toolbar")).getByRole("button", { name: "View source" });
  fireEvent.click(source);
  expect(open).toHaveBeenCalledWith("selection-evidence", source);

  fireEvent.mouseUp(text);
  fireEvent.click(within(screen.getByRole("toolbar")).getByRole("button", { name: "Explain this passage" }));
  expect(choose).toHaveBeenCalledWith(expect.objectContaining({
    source: "paper_guide",
    text: "😀乙𐐀",
    guideRef: {
      revision_id: "r1",
      section_key: "section:background:v1",
      block_id: "background-block",
      start: 1,
      end: 4,
      language: "en",
    },
  }));
});

it.each([
  ["partial", "writing", true],
  ["partial", "paused", false],
  ["partial", "canceled", false],
  ["partial", "failed", false],
  ["partial", "source_changed", false],
  ["superseded", "completed", false],
  ["complete", "completed", false],
  ["limited", "completed", false],
] as const)("shows the next-section notice only for eligible active revisions (%s, %s)", (status, stage, shown) => {
  const r = revision();
  render(<PaperGuideReader
    revision={{ ...r, status, run: { ...r.run, stage }, sections: r.sections.slice(0, 1) }}
    title="Paper"
    language="en"
    onEvidence={() => {}}
    onSelection={() => {}}
  />);
  const hint = screen.queryByText("The next section will appear here after review.");
  if (shown) expect(hint).toBeVisible();
  else expect(hint).not.toBeInTheDocument();
});
it.each(["method", "theory", "normative", "review"])(
  "renders %s as a continuous article with concrete titles",
  (profile) => {
    const r = revision(profile);
    render(
      <PaperGuideReader
        revision={r}
        title="Paper"
        language="en"
        onEvidence={() => {}}
        onSelection={() => {}}
      />,
    );
    expect(screen.getByRole("article")).toBeInTheDocument();
    for (const s of r.sections)
      expect(
        screen.getByRole("heading", { name: s.title }),
      ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /next chapter/i }),
    ).not.toBeInTheDocument();
  },
);
it("marks exact corrected section and labels teaching examples separately from paper evidence", () => {
  const r = revision();
  r.sections[2].publication_status = "correction_required";
  render(
    <PaperGuideReader
      revision={r}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={() => {}}
    />,
  );
  expect(screen.getByText("Correction pending")).toBeInTheDocument();
  expect(
    screen.getByText("Teaching example · hypothetical"),
  ).toBeInTheDocument();
});
it("offers a keyboard selection action bound to the visible immutable section", () => {
  const choose = vi.fn();
  render(
    <PaperGuideReader
      revision={revision()}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={choose}
    />,
  );
  fireEvent.click(
    screen.getAllByRole("button", { name: "Explain this passage" })[0],
  );
  expect(choose.mock.calls[0][0]).toMatchObject({
    source: "paper_guide",
    guideRef: {
      revision_id: "r1",
      section_key: "section:background:v1",
      block_id: "background-block",
      start: 0,
    },
  });
});

it("moves keyboard focus and scrolls to the requested outline heading", () => {
  const scroll = vi.fn();
  Element.prototype.scrollIntoView = scroll;
  const view = render(
    <PaperGuideReader
      revision={revision()}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={() => {}}
    />,
  );
  const scroller = view.container.querySelector(".paper-guide-scroll") as HTMLDivElement;
  const heading = screen.getByRole("heading", { name: "Learning contextual representations" });
  scroller.scrollTop = 40;
  vi.spyOn(scroller, "getBoundingClientRect").mockReturnValue({ top: 80 } as DOMRect);
  vi.spyOn(heading, "getBoundingClientRect").mockReturnValue({ top: 680 } as DOMRect);
  fireEvent.click(
    screen.getByRole("button", { name: "Learning contextual representations" }),
  );
  expect(
    screen.getByRole("heading", {
      name: "Learning contextual representations",
    }),
  ).toHaveFocus();
  expect(scroll).not.toHaveBeenCalled();
  expect(scroller.scrollTop).toBe(616);
});
it("preserves reading position when reviewed sections append and never injects model HTML", () => {
  const scroll = vi.fn();
  Element.prototype.scrollIntoView = scroll;
  const r = revision();
  r.sections[0].blocks[0].text =
    '<img src="https://bad.example/track" onerror="alert(1)">';
  const view = render(
    <PaperGuideReader
      revision={{ ...r, sections: r.sections.slice(0, 2) }}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={() => {}}
    />,
  );
  view.rerender(
    <PaperGuideReader
      revision={r}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={() => {}}
    />,
  );
  expect(scroll).not.toHaveBeenCalled();
  expect(document.querySelector("img")).toBeNull();
});
it("uses the real DOM selection and Unicode offsets rather than character search", () => {
  const choose = vi.fn();
  const r = revision();
  r.sections[0].blocks[0].text = "甲😀乙𐐀丙";
  render(
    <PaperGuideReader
      revision={r}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={choose}
    />,
  );
  const text = screen.getByText("甲😀乙𐐀丙");
  const range = document.createRange();
  range.setStart(text.firstChild!, 1);
  range.setEnd(text.firstChild!, 6);
  window.getSelection()!.removeAllRanges();
  window.getSelection()!.addRange(range);
  fireEvent.mouseUp(text);
  fireEvent.click(screen.getByRole("toolbar").querySelector("button")!);
  expect(choose.mock.calls[0][0]).toMatchObject({
    text: "😀乙𐐀",
    guideRef: { start: 1, end: 4, section_key: "section:background:v1" },
  });
});
it("visibly distinguishes paper paraphrase, interpretation, teaching and unverified external background", () => {
  const r = revision();
  r.sections.forEach((s, i) => {
    s.blocks[0].kind = "paragraph";
    s.blocks[0].attribution = [
      "paper_statement",
      "interpretation",
      "teaching",
      "external_background",
    ][i];
  });
  render(
    <PaperGuideReader
      revision={r}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={() => {}}
    />,
  );
  expect(screen.getByText("Paper statement · guide paraphrase")).toBeVisible();
  expect(screen.getByText("Guide interpretation")).toBeVisible();
  expect(screen.getByText("Teaching explanation")).toBeVisible();
  expect(
    screen.getByText(
      "External background · not verified as evidence from this paper",
    ),
  ).toBeVisible();
});
it("restores a Unicode reading anchor after width reflow and hidden-panel return, but not section append", () => {
  const resizeCallbacks: Array<() => void> = [];
  const frames: Array<FrameRequestCallback> = [];
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(callback: () => void) {
        resizeCallbacks.push(callback);
      }
      observe() {}
      disconnect() {}
    },
  );
  const raf = vi
    .spyOn(window, "requestAnimationFrame")
    .mockImplementation((cb) => {
      frames.push(cb);
      return frames.length;
    });
  const cancel = vi
    .spyOn(window, "cancelAnimationFrame")
    .mockImplementation(() => {});
  let width = 800;
  let scroll!: HTMLElement;
  const rect = vi
    .spyOn(Element.prototype, "getBoundingClientRect")
    .mockImplementation(function (this: Element) {
      if (this.classList.contains("paper-guide-scroll"))
        return new DOMRect(0, 0, width, 300);
      if (
        this.hasAttribute("data-guide-block") ||
        this.hasAttribute("data-guide-text")
      )
        return new DOMRect(
          0,
          100 - (scroll?.scrollTop || 0),
          width,
          width === 0 ? 0 : Math.ceil(40 / (width >= 600 ? 10 : 5)) * 20,
        );
      return new DOMRect();
    });
  const rangeRect = vi
    .spyOn(Range.prototype, "getBoundingClientRect")
    .mockImplementation(function (this: Range) {
      const point = Array.from(
        (this.startContainer.textContent || "").slice(0, this.startOffset),
      ).length;
      return new DOMRect(
        0,
        100 +
          Math.floor(point / (width >= 600 ? 10 : 5)) * 20 -
          scroll.scrollTop,
        10,
        20,
      );
    });
  try {
    const r = revision();
    r.sections[0].blocks[0].text = "😀".repeat(40);
    const onProgress = vi.fn();
    const view = render(
      <PaperGuideReader
        revision={{ ...r, sections: r.sections.slice(0, 1) }}
        title="Paper"
        language="en"
        onEvidence={() => {}}
        onSelection={() => {}}
        onProgress={onProgress}
      />,
    );
    scroll = view.container.querySelector(".paper-guide-scroll")!;
    scroll.scrollTop = 117;
    fireEvent.scroll(scroll);
    expect(onProgress.mock.calls[0][0]).toMatchObject({
      offset: 20,
      section_key: "section:background:v1",
    });
    width = 400;
    act(() => resizeCallbacks.forEach((cb) => cb()));
    act(() => frames.splice(0).forEach((cb) => cb(0)));
    expect(scroll.scrollTop).toBe(156);
    fireEvent.scroll(scroll);
    expect(
      onProgress.mock.calls[onProgress.mock.calls.length - 1][0].offset,
    ).toBe(20);
    // A zero-width hidden pane must not overwrite the character anchor.
    width = 0;
    act(() => resizeCallbacks.forEach((cb) => cb()));
    fireEvent.scroll(scroll);
    act(() => frames.splice(0).forEach((cb) => cb(0)));
    width = 800;
    act(() => resizeCallbacks.forEach((cb) => cb()));
    act(() => frames.splice(0).forEach((cb) => cb(0)));
    expect(scroll.scrollTop).toBe(116);
    scroll.scrollTop = 160;
    view.rerender(
      <PaperGuideReader
        revision={r}
        title="Paper"
        language="en"
        onEvidence={() => {}}
        onSelection={() => {}}
        onProgress={onProgress}
      />,
    );
    act(() => resizeCallbacks.forEach((cb) => cb()));
    act(() => frames.splice(0).forEach((cb) => cb(0)));
    expect(scroll.scrollTop).toBe(160);
  } finally {
    rect.mockRestore();
    rangeRect.mockRestore();
    raf.mockRestore();
    cancel.mockRestore();
    vi.unstubAllGlobals();
  }
});

it.each(["after", "after-surrogate", "inside", "across", "mathml", "boundary"])(
  "maps %s math selections to exact original Unicode intervals",
  (choice) => {
    const choose = vi.fn();
    const r = revision();
    r.sections[0].blocks[0].text = "甲😀 \\(x_1\\) 后 \\(x_1\\)𐐀尾";
    const view = render(
      <PaperGuideReader
        revision={r}
        title="Paper"
        language="en"
        onEvidence={() => {}}
        onSelection={choose}
      />,
    );
    const block = view.container.querySelector(
      '[data-guide-text="background-block"]',
    )!;
    const maths = block.querySelectorAll("[data-guide-math]");
    expect(maths).toHaveLength(2);
    const range = document.createRange();
    const walker = document.createTreeWalker(
      maths[1].querySelector(".katex-html")!,
      NodeFilter.SHOW_TEXT,
    );
    const formulaText = walker.nextNode()!;
    const textAfter = block.lastChild!.firstChild!;
    let start: number, end: number, text: string;
    if (choice === "after" || choice === "after-surrogate") {
      range.setStart(textAfter, choice === "after-surrogate" ? 1 : 0);
      range.setEnd(textAfter, 2);
      start = 20;
      end = 21;
      text = "𐐀";
    } else if (choice === "across") {
      range.setStart(block.firstChild!.firstChild!, 1);
      range.setEnd(formulaText, 1);
      start = 1;
      end = 20;
      text = "😀 \\(x_1\\) 后 \\(x_1\\)";
    } else if (choice === "boundary") {
      range.setStart(block, 2);
      range.setEnd(block, 3);
      start = 10;
      end = 13;
      text = " 后 ";
    } else {
      const node =
        choice === "mathml"
          ? maths[1].querySelector("annotation")!.firstChild!
          : formulaText;
      range.setStart(node, 0);
      range.setEnd(node, 1);
      start = 13;
      end = 20;
      text = "\\(x_1\\)";
    }
    window.getSelection()!.removeAllRanges();
    window.getSelection()!.addRange(range);
    fireEvent.mouseUp(block);
    expect(screen.getByRole("toolbar").querySelector("p")?.textContent).toBe(
      text,
    );
    fireEvent.click(screen.getByRole("toolbar").querySelector("button")!);
    expect(choose.mock.calls[0][0]).toMatchObject({
      text,
      guideRef: {
        revision_id: "r1",
        section_key: "section:background:v1",
        block_id: "background-block",
        start,
        end,
      },
    });
  },
);
it("typesets display math without interpreting HTML and falls back exactly for invalid or unsafe TeX", () => {
  const r = revision();
  r.sections[0].blocks[0].text = "before \\[\\frac{1}{2}\\] after";
  r.sections[1].blocks[0].text = String.raw`bad \(\frac{\) <img src=x onerror=alert(1)>`;
  r.sections[2].blocks[0].text = String.raw`\(\href{https://bad.example}{click}\) \(\includegraphics{https://bad.example/a}\)`;
  const view = render(
    <PaperGuideReader
      revision={r}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={() => {}}
    />,
  );
  expect(view.container.querySelector(".katex-display")).not.toBeNull();
  expect(
    view.container.querySelector('[data-guide-text="question-block"]'),
  ).toHaveTextContent(r.sections[1].blocks[0].text);
  expect(
    view.container.querySelector('[data-guide-text="contribution-block"]'),
  ).toHaveTextContent(r.sections[2].blocks[0].text);
  expect(view.container.querySelector("img,a,iframe,script")).toBeNull();
});
it("discards a pending selection when its immutable section is replaced", () => {
  const r = revision(),
    choose = vi.fn();
  const view = render(
    <PaperGuideReader
      revision={r}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={choose}
    />,
  );
  const text = view.container.querySelector(
    '[data-guide-text="background-block"]',
  )!;
  const range = document.createRange();
  range.selectNodeContents(text);
  window.getSelection()!.removeAllRanges();
  window.getSelection()!.addRange(range);
  fireEvent.mouseUp(text);
  expect(screen.getByRole("toolbar")).toBeInTheDocument();
  const changed = {
    ...r,
    sections: r.sections.map((s, i) =>
      i ? s : { ...s, key: "section:background:v2" },
    ),
  };
  view.rerender(
    <PaperGuideReader
      revision={changed}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={choose}
    />,
  );
  expect(screen.queryByRole("toolbar")).not.toBeInTheDocument();
  expect(choose).not.toHaveBeenCalled();
});

it("binds duplicate block IDs to the actual immutable section for selection, progress and navigation", () => {
  const r = revision(),
    choose = vi.fn(),
    progress = vi.fn();
  r.sections[0].blocks[0] = {
    ...r.sections[0].blocks[0],
    id: "same-block",
    text: "First $a$ text",
  };
  r.sections[1].blocks[0] = {
    ...r.sections[1].blocks[0],
    id: "same-block",
    text: "Second $b$ tail",
  };
  const rect = vi
    .spyOn(Element.prototype, "getBoundingClientRect")
    .mockImplementation(function (this: Element) {
      if (this.classList.contains("paper-guide-scroll"))
        return new DOMRect(0, 0, 800, 300);
      return new DOMRect(
        0,
        0,
        800,
        this.getAttribute("data-guide-section") === "section:background:v1"
          ? 20
          : 120,
      );
    });
  try {
    const view = render(
      <PaperGuideReader
        revision={r}
        title="Paper"
        language="en"
        onEvidence={() => {}}
        onSelection={choose}
        onProgress={progress}
      />,
    );
    const block = view.container.querySelector(
      '[data-guide-section="section:question:v1"] [data-guide-text]',
    )!;
    const range = document.createRange();
    range.selectNodeContents(block);
    window.getSelection()!.removeAllRanges();
    window.getSelection()!.addRange(range);
    fireEvent.mouseUp(block);
    expect(screen.getByRole("toolbar").querySelector("p")?.textContent).toBe(
      "Second $b$ tail",
    );
    fireEvent.click(screen.getByRole("toolbar").querySelector("button")!);
    expect(choose.mock.calls[0][0]).toMatchObject({
      text: "Second $b$ tail",
      guideRef: {
        revision_id: "r1",
        section_key: "section:question:v1",
        block_id: "same-block",
        start: 0,
        end: 15,
      },
    });
    fireEvent.scroll(view.container.querySelector(".paper-guide-scroll")!);
    expect(progress).toHaveBeenLastCalledWith(
      expect.objectContaining({
        section_key: "section:question:v1",
        block_id: "same-block",
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: r.sections[1].title }));
    expect(
      screen.getByRole("heading", { name: r.sections[1].title }),
    ).toHaveFocus();
  } finally {
    rect.mockRestore();
  }
});
it('keeps historical text readable but disables follow-up selection after positioning changes',()=>{
 const r=revision();r.source_current=false;const choose=vi.fn();
 render(<PaperGuideReader revision={r} title="Paper" language="en" onEvidence={()=>{}} onSelection={choose}/>);
 expect(screen.getByRole('article')).toBeInTheDocument();
 for(const button of screen.getAllByRole('button',{name:'Explain this passage'})){expect(button).toBeDisabled();fireEvent.click(button);}
 for(const button of screen.getAllByRole('button',{name:'Give an example'}))expect(button).toBeDisabled();
 expect(choose).not.toHaveBeenCalled();
});

function renderInteractive(
  config: Partial<PaperGuideInteraction> & Pick<PaperGuideInteraction, "kind">,
  text = "Stable static explanation.",
) {
  const r = revision();
  const block = r.sections[0].blocks[0];
  Object.assign(block, {
    kind: "interactive",
    text,
    interaction: {
      title: "Explore the method",
      provenance: "illustrative",
      explanation: "This example assumes a fixed baseline.",
      higher_is_better: false,
      metric: "",
      unit: "",
      steps: [],
      parameters: [],
      outputs: [],
      rows: [],
      ...config,
    } satisfies PaperGuideInteraction,
  });
  const choose = vi.fn();
  const view = render(
    <PaperGuideReader revision={r} title="Paper" language="en" onEvidence={() => {}} onSelection={choose} />,
  );
  return { ...view, choose, r, block };
}

const steps = [
  { label: "Define", text: "Define the reference point." },
  { label: "Measure", text: "Measure the change." },
  { label: "Interpret", text: "Interpret the estimate." },
];

it("shows calculator default substitutions and recalculates when the rank changes", () => {
  renderInteractive({
    kind: "calculator",
    parameters: [{ id: "x", label: "Input", min: 0, max: 4, step: 1, value: 1, unit: "" }],
    outputs: [
      { label: "Variable", tokens: ["x", "2", "mul"], unit: "ms" },
      { label: "Reference", tokens: ["3"], unit: "ms" },
    ],
  });
  expect(screen.getByText("x × 2")).toBeVisible();
  expect(screen.getByText("1 × 2 = 2 ms")).toBeVisible();
  expect(screen.getByText("2 ms")).toBeVisible();
  expect(screen.getByText("3 ms")).toBeVisible();
  fireEvent.change(screen.getByRole("slider", { name: "Input" }), { target: { value: "2" } });
  expect(screen.getByText("4 ms")).toBeVisible();
  expect(screen.getByText("1 × 2 = 2 ms")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Reset" }));
  expect(screen.getByText("2 ms")).toBeVisible();
});

it.each([
  ["division by zero", ["1", "x", "div"]],
  ["nonpositive logarithm", ["x", "log"]],
  ["unbounded intermediate", ["1e15", "x", "div", "1e15", "div"]],
])("marks unavailable calculator values for %s", (_name, tokens) => {
  renderInteractive({
    kind: "calculator",
    parameters: [{ id: "x", label: "Input", min: 0, max: 2, step: 0.5, value: 1, unit: "" }],
    outputs: [{ label: "Value", tokens, unit: "" }],
  });
  fireEvent.change(screen.getByRole("slider", { name: "Input" }), {
    target: { value: _name === "unbounded intermediate" ? "0.5" : "0" },
  });
  expect(screen.getByText("N/A")).toBeVisible();
  expect(screen.getByText(/outside the supported range or domain/i)).toBeVisible();
});

it("navigates steps and resets the selected step", () => {
  renderInteractive({ kind: "stepper", steps });
  expect(screen.getByText(steps[0].text)).toBeVisible();
  expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByText(steps[1].text)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "3 Interpret" }));
  expect(screen.getByText(steps[2].text)).toBeVisible();
  expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Reset" }));
  expect(screen.getByText(steps[0].text)).toBeVisible();
});

it("selects a comparison while retaining its fixed reference", () => {
  renderInteractive({ kind: "comparison", steps });
  fireEvent.click(screen.getByRole("button", { name: "Interpret" }));
  expect(screen.getByText(steps[0].text)).toBeVisible();
  expect(screen.getByText(steps[2].text)).toBeVisible();
  expect(screen.queryByText(steps[1].text)).not.toBeInTheDocument();
});

it("filters only reported rows and shows original values, units and notes in the table", () => {
  renderInteractive({
    kind: "results",
    provenance: "reported",
    metric: "Accuracy",
    higher_is_better: true,
    unit: "%",
    rows: [
      { label: "Base", group: "A", note: "Table 2 · held-out cohort", value: 82.15 },
      { label: "Variant", group: "B", note: "Table 2 · comparison row", value: -0.44 },
    ],
  });
  const table = within(screen.getByRole("table", { name: "Accuracy (%)" }));
  expect(table.getByText("82.15")).toBeVisible();
  expect(table.getByText("-0.44")).toBeVisible();
  expect(table.getByText("Table 2 · comparison row")).toBeVisible();
  expect(screen.getByText("Reported by the paper")).toBeVisible();
  fireEvent.change(screen.getByRole("combobox", { name: "Filter by group" }), { target: { value: "2" } });
  expect(table.queryByText("82.15")).not.toBeInTheDocument();
  expect(table.getByText("-0.44")).toBeVisible();
});

it("keeps static prose and its Unicode selection separate from interaction content", () => {
  const { choose, block } = renderInteractive({ kind: "stepper", steps }, "甲😀乙𐐀丙");
  expect(screen.getByText("Illustrative example")).toBeVisible();
  expect(screen.getByText("This example assumes a fixed baseline.")).toBeVisible();
  const prose = screen.getByText(block.text).closest("[data-guide-text]")!;
  expect(prose).toHaveTextContent(/^甲😀乙𐐀丙$/);
  expect(prose).not.toContainElement(screen.getByRole("button", { name: "Next" }));
  const range = document.createRange();
  range.setStart(prose.firstChild!.firstChild!, 1);
  range.setEnd(prose.firstChild!.firstChild!, 6);
  window.getSelection()!.removeAllRanges();
  window.getSelection()!.addRange(range);
  fireEvent.mouseUp(prose);
  fireEvent.click(within(screen.getByRole("toolbar")).getByRole("button", { name: "Explain this passage" }));
  expect(choose).toHaveBeenCalledWith(expect.objectContaining({
    text: "😀乙𐐀",
    guideRef: expect.objectContaining({ start: 1, end: 4 }),
  }));
});

it("navigates legacy subsection headings by immutable section and block IDs", () => {
  const r = revision();
  for (const [index, section] of r.sections.slice(0, 2).entries()) {
    section.blocks.push({ ...section.blocks[0], id: "duplicate-heading-id", kind: "heading", text: `Subsection ${index + 1}` });
  }
  render(<PaperGuideReader revision={r} title="Paper" language="en" onEvidence={() => {}} onSelection={() => {}} />);
  const outline = within(screen.getByRole("navigation", { name: "Article outline" }));
  fireEvent.click(outline.getByRole("button", { name: "Subsection 2" }));
  expect(screen.getByRole("heading", { name: "Subsection 2" })).toHaveFocus();
  expect(screen.getByRole("heading", { name: "Subsection 1" })).not.toHaveFocus();
});

it("preserves an authored calculator default that is off the step grid", () => {
  renderInteractive({
    kind: "calculator",
    parameters: [{ id: "amount", label: "Amount", min: 0, max: 10, step: 3, value: 5, unit: "" }],
    outputs: [{ label: "Doubled", tokens: ["amount", "2", "mul"], unit: "" }],
  });
  const input = screen.getByRole("spinbutton", { name: "Amount" });
  expect(input).toHaveValue(5);
  expect(screen.getByLabelText("Doubled")).toHaveTextContent(/^10$/);
  fireEvent.change(input, { target: { value: "6" } });
  expect(screen.getByLabelText("Doubled")).toHaveTextContent(/^12$/);
  fireEvent.change(input, { target: { value: "4" } });
  expect(input).toHaveAttribute("aria-invalid", "true");
  expect(screen.getByLabelText("Doubled")).toHaveTextContent(/^N\/A$/);
});

it("groups a negated base in both the power formula and its default substitution", () => {
  renderInteractive({
    kind: "calculator",
    parameters: [{ id: "x", label: "Input", min: 0, max: 4, step: 1, value: 3, unit: "" }],
    outputs: [{ label: "Squared negative", tokens: ["x", "neg", "2", "pow"], unit: "" }],
  });
  expect(screen.getByText("(−(x)) ^ 2")).toBeVisible();
  expect(screen.getByText("(−(3)) ^ 2 = 9")).toBeVisible();
  expect(screen.getByLabelText("Squared negative")).toHaveTextContent(/^9$/);
});
