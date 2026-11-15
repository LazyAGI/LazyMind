import { act, render, screen, fireEvent, within } from "@testing-library/react";
import type { PaperGuideClaim, PaperGuidePlan } from "@/api/generated/core-client";
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
  render(
    <PaperGuideReader
      revision={revision()}
      title="Paper"
      language="en"
      onEvidence={() => {}}
      onSelection={() => {}}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Learning contextual representations" }),
  );
  expect(
    screen.getByRole("heading", {
      name: "Learning contextual representations",
    }),
  ).toHaveFocus();
  expect(scroll).toHaveBeenCalledWith({ block: "start" });
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
