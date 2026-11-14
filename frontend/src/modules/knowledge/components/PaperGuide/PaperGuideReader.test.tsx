import { render, screen, fireEvent } from "@testing-library/react";
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
