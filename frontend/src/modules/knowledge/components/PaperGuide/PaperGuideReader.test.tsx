import { act, render, screen, fireEvent } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import PaperGuideReader from "./PaperGuideReader";
import { revision } from "./fixtures";
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
  expect(screen.getByText("Teaching example · hypothetical")).toBeVisible();
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
