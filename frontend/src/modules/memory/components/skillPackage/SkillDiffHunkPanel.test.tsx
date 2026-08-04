import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import SkillDiffHunkPanel from "./SkillDiffHunkPanel";
import type { DraftDiffEntryLineInput } from "./skillDiffUtils";

const t = (key: string) => key;

const contextOnlyLines: DraftDiffEntryLineInput[] = [
  { type: "CONTEXT", text: "line one" },
  { type: "CONTEXT", text: "line two" },
];

const changedLines: DraftDiffEntryLineInput[] = [
  { type: "HUNK", text: "@@ -1,2 +1,2 @@", hunk_id: "real-hunk-1", decision: "pending" },
  { type: "DELETION", text: "old line", hunk_id: "real-hunk-1" },
  { type: "ADDITION", text: "new line", hunk_id: "real-hunk-1" },
];

describe("SkillDiffHunkPanel", () => {
  it("renders plain context lines when there are no changed regions", () => {
    render(
      <SkillDiffHunkPanel
        diffEntryLines={contextOnlyLines}
        hunkReviewActive={false}
        hunkSubmitting={{}}
        t={t}
      />,
    );
    expect(screen.getByText("line one")).toBeInTheDocument();
    expect(screen.getByText("line two")).toBeInTheDocument();
  });

  it("renders add/remove lines with diff prefixes for a changed hunk", () => {
    const { container } = render(
      <SkillDiffHunkPanel
        diffEntryLines={changedLines}
        hunkReviewActive={false}
        hunkSubmitting={{}}
        t={t}
      />,
    );
    expect(container.querySelector(".memory-diff-line.is-remove")).not.toBeNull();
    expect(container.querySelector(".memory-diff-line.is-add")).not.toBeNull();
    expect(screen.getByText("old line")).toBeInTheDocument();
    expect(screen.getByText("new line")).toBeInTheDocument();
  });

  it("shows accept/reject action buttons when hunkReviewActive with an actionable hunk id", () => {
    render(
      <SkillDiffHunkPanel
        diffEntryLines={changedLines}
        hunkReviewActive
        hunkSubmitting={{}}
        onHunkDecision={vi.fn()}
        t={t}
      />,
    );
    expect(screen.getByText("admin.memorySkillHunkAccept")).toBeInTheDocument();
    expect(screen.getByText("admin.memorySkillHunkReject")).toBeInTheDocument();
  });

  it("calls onHunkDecision with 'accept' when the accept button is clicked", () => {
    const onHunkDecision = vi.fn();
    render(
      <SkillDiffHunkPanel
        diffEntryLines={changedLines}
        hunkReviewActive
        hunkSubmitting={{}}
        onHunkDecision={onHunkDecision}
        t={t}
      />,
    );
    fireEvent.click(screen.getByText("admin.memorySkillHunkAccept"));
    expect(onHunkDecision).toHaveBeenCalledWith(
      expect.objectContaining({ hunkId: "real-hunk-1" }),
      "accept",
    );
  });

  it("shows the fallback hint when review is active but no actionable hunks exist", () => {
    const fallbackLines: DraftDiffEntryLineInput[] = [
      { type: "HUNK", text: "@@", hunk_id: "hunk-0" },
      { type: "DELETION", text: "old", hunk_id: "hunk-0" },
      { type: "ADDITION", text: "new", hunk_id: "hunk-0" },
    ];
    render(
      <SkillDiffHunkPanel
        diffEntryLines={fallbackLines}
        hunkReviewActive
        hunkSubmitting={{}}
        onHunkDecision={vi.fn()}
        t={t}
      />,
    );
    expect(
      screen.getByText("admin.memorySkillHunkActionsUnavailable"),
    ).toBeInTheDocument();
  });

  it("strips leading front matter lines when stripFrontMatter is true and review is inactive", () => {
    const frontMatterLines: DraftDiffEntryLineInput[] = [
      { type: "CONTEXT", text: "---" },
      { type: "CONTEXT", text: "name: skill" },
      { type: "CONTEXT", text: "---" },
      { type: "CONTEXT", text: "body content" },
    ];
    render(
      <SkillDiffHunkPanel
        diffEntryLines={frontMatterLines}
        hunkReviewActive={false}
        hunkSubmitting={{}}
        t={t}
        stripFrontMatter
      />,
    );
    expect(screen.queryByText("name: skill")).not.toBeInTheDocument();
    expect(screen.getByText("body content")).toBeInTheDocument();
  });
});
