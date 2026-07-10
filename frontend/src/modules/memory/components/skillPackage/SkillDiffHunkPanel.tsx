import { Button, Popconfirm, Space } from "antd";
import { CheckOutlined, CloseOutlined } from "@ant-design/icons";
import { SkillDiffLineContent } from "./SkillDiffLineContent";
import {
  buildDiffHunkBlocks,
  isAcceptedHunkDecision,
  isPendingHunkDecision,
  isRejectedHunkDecision,
  mapSkillDiffEntryLines,
  type SkillDiffEntryLine,
  type SkillDiffHunkBlock,
  type SkillDraftReviewActionDecision,
} from "./skillDiffUtils";
import type { DiffEntryLineOpenAPIResponse } from "@/api/generated/core-client";

interface SkillDiffHunkPanelProps {
  diffEntryLines: DiffEntryLineOpenAPIResponse[];
  reviewMode: boolean;
  hunkSubmitting: Record<string, SkillDraftReviewActionDecision>;
  onHunkDecision?: (hunk: SkillDiffHunkBlock, decision: SkillDraftReviewActionDecision) => void;
  t: (key: string, options?: Record<string, unknown>) => string;
}

const renderDiffLine = (line: SkillDiffEntryLine, key: string) => (
  <div key={key} className={`memory-diff-line is-${line.type}`}>
    <span className="memory-diff-prefix">
      {line.type === "add" ? "+" : line.type === "remove" ? "-" : " "}
    </span>
    <SkillDiffLineContent line={line} />
  </div>
);

export default function SkillDiffHunkPanel({
  diffEntryLines,
  reviewMode,
  hunkSubmitting,
  onHunkDecision,
  t,
}: SkillDiffHunkPanelProps) {
  const entryLines = mapSkillDiffEntryLines(diffEntryLines);
  const hunks = buildDiffHunkBlocks(entryLines);
  const actionableHunks = hunks.filter(
    (hunk) => hunk.hunkId && !hunk.hunkId.startsWith("hunk-"),
  );

  if (!hunks.length) {
    return (
      <div className="memory-skill-package-diff">
        {entryLines.map((line, index) =>
          renderDiffLine(line, `line-${index}-${line.text}`),
        )}
      </div>
    );
  }

  return (
    <div className="memory-skill-package-diff memory-skill-package-diff-hunks">
      {hunks.map((hunk, hunkIndex) => {
        const isAccepted = isAcceptedHunkDecision(hunk.decision);
        const isRejected = isRejectedHunkDecision(hunk.decision);
        const isPending = isPendingHunkDecision(hunk.decision);
        const submitting = hunkSubmitting[hunk.hunkId];
        const isSubmitting = Boolean(submitting);
        const hasRealHunkId = Boolean(hunk.hunkId) && !hunk.hunkId.startsWith("hunk-");
        const canAct = reviewMode && Boolean(onHunkDecision) && hasRealHunkId;

        return (
          <div
            key={`${hunk.hunkId}-${hunkIndex}`}
            className={`memory-skill-diff-hunk${isAccepted ? " is-accepted" : ""}${
              isRejected ? " is-rejected" : ""
            }${isPending && canAct ? " is-pending" : ""}`}
          >
            <div className="memory-skill-diff-hunk-toolbar">
              <div className="memory-skill-diff-hunk-toolbar-main">
                <span className="memory-skill-diff-hunk-label">
                  {t("admin.memorySkillHunkBlockTitle", { index: hunkIndex + 1 })}
                </span>
                {!isPending ? (
                  <span
                    className={`memory-diff-change-decision is-${
                      isAccepted ? "accept" : "reject"
                    }`}
                  >
                    {isAccepted
                      ? t("admin.memorySkillHunkAccepted")
                      : t("admin.memorySkillHunkRejected")}
                  </span>
                ) : canAct ? (
                  <span className="memory-skill-diff-hunk-pending">
                    {t("admin.memorySkillHunkPending")}
                  </span>
                ) : null}
              </div>
              {canAct ? (
                <Space size={6} wrap className="memory-skill-diff-hunk-actions">
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
            <div className="memory-skill-diff-hunk-head">
              {renderDiffLine(hunk.header, `${hunk.hunkId}-header`)}
            </div>
            <div className="memory-skill-diff-hunk-body">
              {hunk.lines.map((line, index) =>
                renderDiffLine(line, `${hunk.hunkId}-${index}-${line.text}`),
              )}
            </div>
          </div>
        );
      })}
      {reviewMode && actionableHunks.length === 0 ? (
        <div className="memory-skill-diff-hunk-fallback-hint">
          {t("admin.memorySkillHunkActionsUnavailable")}
        </div>
      ) : null}
    </div>
  );
}
