import { Button, Popconfirm, Space } from "antd";
import { CheckOutlined, CloseOutlined } from "@ant-design/icons";
import { SkillDiffLineContent } from "./SkillDiffLineContent";
import {
  buildDiffHunkBlocks,
  buildInlineChangeRegions,
  isAcceptedHunkDecision,
  isActionableHunkId,
  isPendingHunkDecision,
  isRejectedHunkDecision,
  mapSkillDiffEntryLines,
  type DraftDiffEntryLineInput,
  type SkillDiffEntryLine,
  type SkillDiffHunkBlock,
  type SkillDraftReviewActionDecision,
} from "./skillDiffUtils";

interface SkillDiffHunkPanelProps {
  diffEntryLines: DraftDiffEntryLineInput[];
  hunkReviewActive: boolean;
  hunkSubmitting: Partial<Record<string, SkillDraftReviewActionDecision>>;
  onHunkDecision?: (hunk: SkillDiffHunkBlock, decision: SkillDraftReviewActionDecision) => void;
  t: (key: string, options?: Record<string, unknown>) => string;
}

const renderContextLine = (line: SkillDiffEntryLine, key: string) => (
  <div key={key} className="memory-skill-diff-doc-line">
    <SkillDiffLineContent line={line} />
  </div>
);

const renderChangeLine = (line: SkillDiffEntryLine, key: string) => (
  <div key={key} className={`memory-diff-line is-${line.type}`}>
    <span className="memory-diff-prefix">
      {line.type === "add" ? "+" : line.type === "remove" ? "-" : " "}
    </span>
    <SkillDiffLineContent line={line} />
  </div>
);

const renderHunkActions = (
  hunk: SkillDiffHunkBlock,
  options: {
    canAct: boolean;
    showReviewChrome: boolean;
    isAccepted: boolean;
    isRejected: boolean;
    isPending: boolean;
    isSubmitting: boolean;
    submitting?: SkillDraftReviewActionDecision;
    onHunkDecision?: (hunk: SkillDiffHunkBlock, decision: SkillDraftReviewActionDecision) => void;
    t: (key: string, options?: Record<string, unknown>) => string;
  },
) => {
  const {
    canAct,
    showReviewChrome,
    isAccepted,
    isRejected,
    isPending,
    isSubmitting,
    submitting,
    onHunkDecision,
    t,
  } = options;

  if (!showReviewChrome) {
    return null;
  }

  return (
    <div className="memory-skill-diff-inline-hunk-actions">
      {!isPending ? (
        <span
          className={`memory-diff-change-decision is-${isAccepted ? "accept" : "reject"}`}
        >
          {isAccepted
            ? t("admin.memorySkillHunkAccepted")
            : t("admin.memorySkillHunkRejected")}
        </span>
      ) : null}
      {canAct ? (
        <Space size={6} wrap>
          <Button
            size="small"
            type={isAccepted ? "primary" : "default"}
            icon={<CheckOutlined />}
            loading={submitting === "accept"}
            disabled={isSubmitting}
            onClick={() => onHunkDecision?.(hunk, "accept")}
          >
            {t("admin.memorySkillHunkAccept")}
          </Button>
          <Popconfirm
            title={t("admin.memorySkillHunkRejectConfirmTitle")}
            description={t("admin.memorySkillHunkRejectConfirmContent")}
            okText={t("admin.memorySkillHunkRejectConfirmOk")}
            cancelText={t("common.cancel")}
            onConfirm={() => onHunkDecision?.(hunk, "reject")}
          >
            <Button
              size="small"
              danger={isRejected}
              type={isRejected ? "primary" : "default"}
              icon={<CloseOutlined />}
              loading={submitting === "reject"}
              disabled={isSubmitting}
            >
              {t("admin.memorySkillHunkReject")}
            </Button>
          </Popconfirm>
        </Space>
      ) : null}
    </div>
  );
};

export default function SkillDiffHunkPanel({
  diffEntryLines,
  hunkReviewActive,
  hunkSubmitting,
  onHunkDecision,
  t,
}: SkillDiffHunkPanelProps) {
  const entryLines = mapSkillDiffEntryLines(diffEntryLines);
  const hunks = buildDiffHunkBlocks(entryLines);
  const regions = buildInlineChangeRegions(hunks);
  const actionableHunks = hunks.filter((hunk) => isActionableHunkId(hunk.hunkId));

  if (!regions.length) {
    return (
      <div className="memory-skill-package-diff memory-skill-package-diff-document">
        {entryLines.map((line, index) =>
          renderContextLine(line, `line-${index}-${line.text}`),
        )}
      </div>
    );
  }

  return (
    <div className="memory-skill-package-diff memory-skill-package-diff-document">
      {regions.map((region) => {
        if (region.isContextOnly) {
          return region.lines.map((line, index) =>
            renderContextLine(line, `${region.regionId}-ctx-${index}-${line.text}`),
          );
        }

        const hunk = region.hunk;
        const isAccepted = isAcceptedHunkDecision(hunk.decision);
        const isRejected = isRejectedHunkDecision(hunk.decision);
        const isPending = isPendingHunkDecision(hunk.decision);
        const submitting = hunkSubmitting[hunk.hunkId];
        const isSubmitting = Boolean(submitting);
        const hasRealHunkId = isActionableHunkId(hunk.hunkId);
        const canAct = hunkReviewActive && Boolean(onHunkDecision) && hasRealHunkId;
        const showReviewChrome = hunkReviewActive && (canAct || isAccepted || isRejected);

        return (
          <div
            key={region.regionId}
            className={`memory-skill-diff-inline-hunk is-changed${
              showReviewChrome ? " is-reviewable" : ""
            }${isAccepted ? " is-accepted" : ""}${isRejected ? " is-rejected" : ""}${
              isPending && canAct ? " is-pending" : ""
            }`}
          >
            {renderHunkActions(hunk, {
              canAct,
              showReviewChrome,
              isAccepted,
              isRejected,
              isPending,
              isSubmitting,
              submitting,
              onHunkDecision,
              t,
            })}
            {region.lines.map((line, index) =>
              renderChangeLine(line, `${region.regionId}-${index}-${line.text}`),
            )}
          </div>
        );
      })}
      {hunkReviewActive && actionableHunks.length === 0 ? (
        <div className="memory-skill-diff-hunk-fallback-hint">
          {t("admin.memorySkillHunkActionsUnavailable")}
        </div>
      ) : null}
    </div>
  );
}
