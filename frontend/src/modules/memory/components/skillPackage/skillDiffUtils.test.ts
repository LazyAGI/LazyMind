import { describe, expect, it } from "vitest";
import {
  buildDiffHunkBlocks,
  buildInlineChangeRegions,
  getDiffStatusColor,
  isAcceptedHunkDecision,
  isActionableHunkId,
  isPendingHunkDecision,
  isRejectedHunkDecision,
  mapDiffEntryLine,
  mapDiffEntryLines,
  mapSkillDiffEntryLines,
  summarizeSkillReviewFiles,
  toDiffLine,
} from "./skillDiffUtils";

describe("mapDiffEntryLine", () => {
  it("maps DELETION/ADDITION/HUNK types to remove/add/hunk", () => {
    expect(mapDiffEntryLine({ type: "DELETION", text: "old" }).type).toBe("remove");
    expect(mapDiffEntryLine({ type: "ADDITION", text: "new" }).type).toBe("add");
    expect(mapDiffEntryLine({ type: "HUNK", text: "@@ -1,2 +1,2 @@" }).type).toBe("hunk");
  });

  it("defaults unknown types to same", () => {
    expect(mapDiffEntryLine({ type: "CONTEXT", text: "unchanged" }).type).toBe("same");
  });

  it("extracts snake_case and camelCase hunk metadata", () => {
    const mapped = mapDiffEntryLine({
      type: "ADDITION",
      text: "new line",
      hunk_id: "hunk-abc",
      decision: "accepted",
      old_line: 3,
      new_line: 4,
    });
    expect(mapped.hunkId).toBe("hunk-abc");
    expect(mapped.decision).toBe("accepted");
    expect(mapped.oldLine).toBe(3);
    expect(mapped.newLine).toBe(4);
  });

  it("falls back text to a single space when missing", () => {
    expect(mapDiffEntryLine({ type: "CONTEXT" }).text).toBe(" ");
  });
});

describe("mapDiffEntryLines / mapSkillDiffEntryLines", () => {
  it("collapses hunk lines to same for the plain DiffLine mapper", () => {
    const lines = mapDiffEntryLines([
      { type: "HUNK", text: "@@" },
      { type: "ADDITION", text: "added" },
      { type: "DELETION", text: "removed" },
    ]);
    expect(lines).toEqual([
      { type: "same", text: "@@" },
      { type: "add", text: "added" },
      { type: "remove", text: "removed" },
    ]);
  });

  it("preserves hunk metadata for the rich mapper", () => {
    const lines = mapSkillDiffEntryLines([{ type: "HUNK", text: "@@", hunk_id: "hunk-1" }]);
    expect(lines[0].type).toBe("hunk");
    expect(lines[0].hunkId).toBe("hunk-1");
  });

  it("defaults to an empty array when no lines are given", () => {
    expect(mapDiffEntryLines()).toEqual([]);
    expect(mapSkillDiffEntryLines()).toEqual([]);
  });
});

describe("isActionableHunkId", () => {
  it("rejects the generated fallback pattern", () => {
    expect(isActionableHunkId("hunk-3")).toBe(false);
  });

  it("accepts real backend hunk ids", () => {
    expect(isActionableHunkId("hunk-real-id")).toBe(true);
  });

  it("rejects empty/undefined ids", () => {
    expect(isActionableHunkId(undefined)).toBe(false);
    expect(isActionableHunkId("")).toBe(false);
  });
});

describe("hunk decision helpers", () => {
  it("treats missing/pending/pending_accept as pending", () => {
    expect(isPendingHunkDecision(undefined)).toBe(true);
    expect(isPendingHunkDecision("pending")).toBe(true);
    expect(isPendingHunkDecision("pending_accept")).toBe(true);
    expect(isPendingHunkDecision("accepted")).toBe(false);
  });

  it("detects accepted/rejected decisions case-insensitively", () => {
    expect(isAcceptedHunkDecision("Accepted")).toBe(true);
    expect(isAcceptedHunkDecision("rejected")).toBe(false);
    expect(isRejectedHunkDecision("Rejected")).toBe(true);
    expect(isRejectedHunkDecision("accepted")).toBe(false);
  });
});

describe("buildDiffHunkBlocks", () => {
  it("groups lines under their preceding hunk header", () => {
    const lines = mapSkillDiffEntryLines([
      { type: "HUNK", text: "@@ -1 +1 @@", hunk_id: "hunk-1", decision: "pending" },
      { type: "CONTEXT", text: "line1" },
      { type: "ADDITION", text: "line2" },
    ]);
    const blocks = buildDiffHunkBlocks(lines);
    expect(blocks).toHaveLength(1);
    expect(blocks[0].hunkId).toBe("hunk-1");
    expect(blocks[0].lines).toHaveLength(2);
  });

  it("creates a fallback hunk when lines appear before any HUNK marker", () => {
    const lines = mapSkillDiffEntryLines([{ type: "ADDITION", text: "no header" }]);
    const blocks = buildDiffHunkBlocks(lines);
    expect(blocks).toHaveLength(1);
    expect(blocks[0].hunkId).toBe("hunk-0");
  });

  it("updates the block decision from line-level decisions", () => {
    const lines = mapSkillDiffEntryLines([
      { type: "HUNK", text: "@@", hunk_id: "hunk-1" },
      { type: "ADDITION", text: "line", decision: "rejected" },
    ]);
    const blocks = buildDiffHunkBlocks(lines);
    expect(blocks[0].decision).toBe("rejected");
  });
});

describe("buildInlineChangeRegions", () => {
  it("splits change lines and context lines into separate regions", () => {
    const lines = mapSkillDiffEntryLines([
      { type: "HUNK", text: "@@", hunk_id: "hunk-1" },
      { type: "CONTEXT", text: "context1" },
      { type: "DELETION", text: "removed" },
      { type: "ADDITION", text: "added" },
      { type: "CONTEXT", text: "context2" },
    ]);
    const hunks = buildDiffHunkBlocks(lines);
    const regions = buildInlineChangeRegions(hunks);

    expect(regions).toHaveLength(3);
    expect(regions[0].isContextOnly).toBe(true);
    expect(regions[1].isContextOnly).toBe(false);
    expect(regions[2].isContextOnly).toBe(true);
  });
});

describe("summarizeSkillReviewFiles", () => {
  it("counts accepted/rejected/pending hunks and collects actionable pending hunks", () => {
    const files = [
      {
        path: "SKILL.md",
        diffEntryLines: [
          { type: "HUNK", text: "@@", hunk_id: "real-hunk-1", decision: "accepted" },
          { type: "HUNK", text: "@@", hunk_id: "real-hunk-2", decision: "rejected" },
          { type: "HUNK", text: "@@", hunk_id: "real-hunk-3" },
        ],
      },
    ];

    const { stats, pendingHunks } = summarizeSkillReviewFiles(files);
    expect(stats).toEqual({ accepted: 1, rejected: 1, pending: 1 });
    expect(pendingHunks).toEqual([{ path: "SKILL.md", hunkId: "real-hunk-3" }]);
  });

  it("excludes fallback hunk ids from pending hunks", () => {
    const files = [
      {
        path: "SKILL.md",
        diffEntryLines: [{ type: "ADDITION", text: "no header" }],
      },
    ];

    const { stats, pendingHunks } = summarizeSkillReviewFiles(files);
    expect(stats.pending).toBe(1);
    expect(pendingHunks).toEqual([]);
  });
});

describe("toDiffLine", () => {
  it("converts hunk lines to same type DiffLine", () => {
    expect(toDiffLine({ rawType: "HUNK", type: "hunk", text: "@@" })).toEqual({
      type: "same",
      text: "@@",
    });
  });

  it("preserves add/remove/same types", () => {
    expect(toDiffLine({ rawType: "ADDITION", type: "add", text: "x" })).toEqual({
      type: "add",
      text: "x",
    });
  });
});

describe("getDiffStatusColor", () => {
  it("maps known statuses to colors", () => {
    expect(getDiffStatusColor("added")).toBe("success");
    expect(getDiffStatusColor("modified")).toBe("processing");
    expect(getDiffStatusColor("renamed")).toBe("processing");
    expect(getDiffStatusColor("deleted")).toBe("error");
  });

  it("defaults unknown statuses", () => {
    expect(getDiffStatusColor("unchanged")).toBe("default");
  });
});
