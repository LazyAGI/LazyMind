import type { DiffEntryLineOpenAPIResponse } from "@/api/generated/core-client";
import type { DiffLine } from "../../shared";

export type SkillDiffEntryType = "HUNK" | "ADDITION" | "DELETION" | "CONTEXT" | string;

export type SkillHunkDecision = "pending" | "accepted" | "rejected" | "pending_accept" | string;

export type SkillDraftReviewActionDecision = "accept" | "reject";

export interface SkillDiffEntryLine {
  rawType: SkillDiffEntryType;
  type: DiffLine["type"] | "hunk";
  text: string;
  html?: string;
  hunkId?: string;
  decision?: SkillHunkDecision;
  displayNoNewLineWarning?: boolean;
  oldLine?: number;
  newLine?: number;
}

export interface SkillDiffHunkBlock {
  hunkId: string;
  header: SkillDiffEntryLine;
  lines: SkillDiffEntryLine[];
  decision: SkillHunkDecision;
}

const readRawField = (line: Record<string, unknown>, keys: string[]): string => {
  for (const key of keys) {
    const value = line[key];
    if (typeof value === "string" && value.trim()) {
      return value.trim();
    }
    if (typeof value === "number" && Number.isFinite(value)) {
      return String(value);
    }
  }
  return "";
};

const normalizeEntryType = (line: DiffEntryLineOpenAPIResponse | Record<string, unknown>) =>
  String(line.type || "").trim().toUpperCase();

const mapEntryLineType = (normalizedType: string): SkillDiffEntryLine["type"] => {
  if (normalizedType === "DELETION") {
    return "remove";
  }
  if (normalizedType === "ADDITION") {
    return "add";
  }
  if (normalizedType === "HUNK") {
    return "hunk";
  }
  return "same";
};

export const mapDiffEntryLine = (
  line: DiffEntryLineOpenAPIResponse | Record<string, unknown>,
): SkillDiffEntryLine => {
  const raw = line as Record<string, unknown>;
  const normalizedType = normalizeEntryType(line);
  const html = readRawField(raw, ["html"]) || undefined;

  return {
    rawType: normalizedType,
    type: mapEntryLineType(normalizedType),
    text: String(line.text ?? " "),
    html,
    hunkId: readRawField(raw, ["hunk_id", "hunkId"]) || undefined,
    decision: readRawField(raw, ["decision", "status"]) || undefined,
    displayNoNewLineWarning: Boolean(raw.displayNoNewLineWarning ?? raw.display_no_new_line_warning),
    oldLine: typeof raw.oldLine === "number" ? raw.oldLine : (raw.old_line as number | undefined),
    newLine: typeof raw.newLine === "number" ? raw.newLine : (raw.new_line as number | undefined),
  };
};

export const mapDiffEntryLines = (
  lines: DiffEntryLineOpenAPIResponse[] = [],
): DiffLine[] =>
  lines.map((line) => {
    const mapped = mapDiffEntryLine(line);
    if (mapped.type === "hunk") {
      return { type: "same", text: mapped.text };
    }
    return { type: mapped.type, text: mapped.text };
  });

export const mapSkillDiffEntryLines = (
  lines: DiffEntryLineOpenAPIResponse[] = [],
): SkillDiffEntryLine[] => lines.map((line) => mapDiffEntryLine(line));

export const isPendingHunkDecision = (decision?: SkillHunkDecision) => {
  const normalized = String(decision || "pending").trim().toLowerCase();
  return !normalized || normalized === "pending" || normalized === "pending_accept";
};

export const isAcceptedHunkDecision = (decision?: SkillHunkDecision) =>
  String(decision || "")
    .trim()
    .toLowerCase() === "accepted";

export const isRejectedHunkDecision = (decision?: SkillHunkDecision) =>
  String(decision || "")
    .trim()
    .toLowerCase() === "rejected";

export const buildDiffHunkBlocks = (lines: SkillDiffEntryLine[]): SkillDiffHunkBlock[] => {
  const blocks: SkillDiffHunkBlock[] = [];
  let current: SkillDiffHunkBlock | null = null;

  const pushCurrent = () => {
    if (current) {
      blocks.push(current);
      current = null;
    }
  };

  lines.forEach((line, index) => {
    if (line.rawType === "HUNK" || line.type === "hunk") {
      pushCurrent();
      const hunkId = line.hunkId || `hunk-${index}`;
      current = {
        hunkId,
        header: line,
        lines: [],
        decision: line.decision || "pending",
      };
      return;
    }

    if (!current) {
      const fallbackId = line.hunkId || `hunk-${index}`;
      current = {
        hunkId: fallbackId,
        header: {
          rawType: "HUNK",
          type: "hunk",
          text: "@@",
          hunkId: fallbackId,
          decision: line.decision || "pending",
        },
        lines: [],
        decision: line.decision || "pending",
      };
    }

    current.lines.push(line);
    if (line.decision) {
      current.decision = line.decision;
    }
  });

  pushCurrent();
  return blocks;
};

export const toDiffLine = (line: SkillDiffEntryLine): DiffLine => ({
  type: line.type === "hunk" ? "same" : line.type,
  text: line.text,
});

export const getDiffStatusColor = (status: string) => {
  switch (status) {
    case "added":
      return "success";
    case "modified":
    case "renamed":
      return "processing";
    case "deleted":
      return "error";
    default:
      return "default";
  }
};
