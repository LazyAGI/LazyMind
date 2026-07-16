import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Button, Empty, Input, Modal, Popconfirm, Space, Spin, message } from "antd";
import { CheckOutlined, CloseOutlined, RollbackOutlined, SaveOutlined } from "@ant-design/icons";
import MarkdownViewer from "@/modules/knowledge/components/MarkdownViewer";
import { getLocalizedErrorMessage } from "@/components/request";
import SkillDiffHunkPanel from "../skillPackage/SkillDiffHunkPanel";
import {
  buildDiffHunkBlocks,
  isActionableHunkId,
  isPendingHunkDecision,
  mapSkillDiffEntryLines,
} from "../skillPackage/skillDiffUtils";
import {
  commitPersonalResourceDraft,
  confirmManagedPreferenceDraft,
  discardPersonalResourceDraft,
  hasPersonalResourceDraftChanges,
  previewManagedPreferenceDraft,
  readPersonalResourceFile,
  resolveManagedPreferenceDraftKind,
  resolvePersonalResourceApiType,
  reviewManagedPreferenceDraftHunks,
  undoManagedPreferenceDraftReview,
  writePersonalResourceDraft,
  type ManagedPreferenceDraftDecision,
  type PersonalResourceApiType,
  type PreferenceDraftPreviewRecord,
} from "../../preferenceApi";

interface PersonalResourceContentEditorProps {
  resourceType?: string;
  canEdit: boolean;
  t: (key: string, options?: Record<string, unknown>) => string;
  onUpdated?: () => void | Promise<void>;
}

export default function PersonalResourceContentEditor({
  resourceType,
  canEdit,
  t,
  onUpdated,
}: PersonalResourceContentEditorProps) {
  const apiResourceType: PersonalResourceApiType = resolvePersonalResourceApiType(resourceType);
  const draftKind = resolveManagedPreferenceDraftKind(resourceType);

  const [loading, setLoading] = useState(true);
  const [errorMessage, setErrorMessage] = useState("");
  const [retryKey, setRetryKey] = useState(0);
  const [draftPreview, setDraftPreview] = useState<PreferenceDraftPreviewRecord | null>(null);
  const [draftVersion, setDraftVersion] = useState(0);
  const [headContent, setHeadContent] = useState("");
  const [draftContent, setDraftContent] = useState("");
  const [displayContent, setDisplayContent] = useState("");
  const [originalContent, setOriginalContent] = useState("");
  const [hasLocalDraft, setHasLocalDraft] = useState(false);
  const [isEditing, setIsEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [committing, setCommitting] = useState(false);
  const [hunkSubmitting, setHunkSubmitting] = useState<
    Partial<Record<string, ManagedPreferenceDraftDecision>>
  >({});
  const [reviewUndoing, setReviewUndoing] = useState(false);
  const [bulkDecisionSubmitting, setBulkDecisionSubmitting] =
    useState<ManagedPreferenceDraftDecision | null>(null);

  const reviewMode = Boolean(draftPreview?.reviewId);
  const diffEntryLines = draftPreview?.fileDiff?.diffEntryLines || [];
  const hasDiffView = Boolean(diffEntryLines.length);
  const pendingHunkCount = draftPreview?.pendingCount ?? 0;
  const canConfirmReview = reviewMode && pendingHunkCount === 0;
  const pendingHunkIds = useMemo(() => {
    const hunks = buildDiffHunkBlocks(mapSkillDiffEntryLines(diffEntryLines));
    return hunks
      .filter(
        (hunk) => isActionableHunkId(hunk.hunkId) && isPendingHunkDecision(hunk.decision),
      )
      .map((hunk) => hunk.hunkId);
  }, [diffEntryLines]);
  const isReviewActionBusy = Boolean(
    Object.keys(hunkSubmitting).length || bulkDecisionSubmitting || reviewUndoing,
  );

  const refreshDraftPreview = useCallback(async () => {
    const nextPreview = await previewManagedPreferenceDraft(draftKind);
    setDraftPreview(nextPreview);
    return nextPreview;
  }, [draftKind]);

  const refreshContent = useCallback(async () => {
    setLoading(true);
    setErrorMessage("");
    try {
      const [headFile, nextPreview] = await Promise.all([
        readPersonalResourceFile(apiResourceType, { ref: "head" }),
        previewManagedPreferenceDraft(draftKind),
      ]);

      const nextHeadContent = nextPreview.currentContent || headFile.content || "";
      const nextDraftContent = nextPreview.draftContent || nextHeadContent;
      const nextHasLocalDraft = hasPersonalResourceDraftChanges({
        draftStatus: nextPreview.draftStatus || headFile.draftStatus,
        headContent: nextHeadContent,
        draftContent: nextDraftContent,
      });

      setDraftPreview(nextPreview);
      setDraftVersion(headFile.draftVersion || nextPreview.draftVersion || 0);
      setHeadContent(nextHeadContent);
      setDraftContent(nextDraftContent);
      setDisplayContent(nextHasLocalDraft ? nextDraftContent : nextHeadContent);
      setOriginalContent(nextHasLocalDraft ? nextDraftContent : nextHeadContent);
      setHasLocalDraft(nextHasLocalDraft);
      setIsEditing(false);
    } catch (error) {
      console.error("Load personal resource content failed:", error);
      setErrorMessage(getLocalizedErrorMessage(error));
    } finally {
      setLoading(false);
    }
  }, [apiResourceType, draftKind, t]);

  useEffect(() => {
    void refreshContent();
  }, [refreshContent, retryKey]);

  const handleStartEdit = () => {
    const nextContent = hasLocalDraft ? draftContent : headContent;
    setDisplayContent(nextContent);
    setOriginalContent(nextContent);
    setIsEditing(true);
  };

  const handleCancelEdit = () => {
    setDisplayContent(originalContent);
    setIsEditing(false);
  };

  const handleSaveDraft = async () => {
    if (!canEdit || saving || reviewMode) {
      return;
    }
    if (displayContent === originalContent) {
      setIsEditing(false);
      return;
    }

    setSaving(true);
    try {
      const nextVersion = await writePersonalResourceDraft(apiResourceType, {
        content: displayContent,
        expectedDraftVersion: draftVersion,
      });
      setDraftVersion(nextVersion);
      const nextPreview = await refreshDraftPreview();
      const nextHeadContent = nextPreview.currentContent || headContent;
      const nextDraftContent = nextPreview.draftContent || displayContent;
      setDraftContent(nextDraftContent);
      setOriginalContent(nextDraftContent);
      setDisplayContent(nextDraftContent);
      setHasLocalDraft(
        hasPersonalResourceDraftChanges({
          draftStatus: nextPreview.draftStatus,
          headContent: nextHeadContent,
          draftContent: nextDraftContent,
        }),
      );
      setIsEditing(false);
      message.success(t("common.saveSuccess"));
    } catch (error) {
      console.error("Save personal resource draft failed:", error);
    } finally {
      setSaving(false);
    }
  };

  const handleCommitDraft = async () => {
    if (!canEdit || committing) {
      return;
    }

    setCommitting(true);
    try {
      if (reviewMode) {
        await confirmManagedPreferenceDraft(draftKind);
      } else {
        await commitPersonalResourceDraft(apiResourceType, draftVersion);
      }
      message.success(
        reviewMode
          ? t("admin.memorySkillDraftConfirmSuccess")
          : t("admin.memorySkillDraftCommitSuccess"),
      );
      await refreshContent();
      await onUpdated?.();
    } catch (error) {
      console.error("Commit personal resource draft failed:", error);
    } finally {
      setCommitting(false);
    }
  };

  const handleHunkDecision = async (
    hunkId: string,
    decision: ManagedPreferenceDraftDecision,
  ) => {
    if (
      !canEdit ||
      !draftPreview?.reviewId ||
      !draftPreview.reviewVersion ||
      isReviewActionBusy
    ) {
      return;
    }

    setHunkSubmitting({ [hunkId]: decision });
    try {
      await reviewManagedPreferenceDraftHunks(draftKind, {
        reviewId: draftPreview.reviewId,
        expectedReviewVersion: draftPreview.reviewVersion,
        items: [{ hunkId, decision }],
      });
      const nextPreview = await refreshDraftPreview();
      await applyReviewPreview(nextPreview);
      message.success(
        t(
          decision === "accept"
            ? "admin.memoryDraftHunkAcceptSuccess"
            : "admin.memoryDraftHunkRejectSuccess",
        ),
      );
    } catch (error) {
      console.error("Submit personal resource draft hunk decision failed:", error);
    } finally {
      setHunkSubmitting({});
    }
  };

  const applyReviewPreview = async (nextPreview: PreferenceDraftPreviewRecord) => {
    const nextHeadContent = nextPreview.currentContent || headContent;
    const nextDraftContent = nextPreview.draftContent || draftContent;
    setHeadContent(nextHeadContent);
    setDraftContent(nextDraftContent);
    setDisplayContent(nextDraftContent);
    setOriginalContent(nextDraftContent);
    setHasLocalDraft(
      hasPersonalResourceDraftChanges({
        draftStatus: nextPreview.draftStatus,
        headContent: nextHeadContent,
        draftContent: nextDraftContent,
      }),
    );
  };

  const handleBulkHunkDecision = async (decision: ManagedPreferenceDraftDecision) => {
    if (
      !canEdit ||
      !draftPreview?.reviewId ||
      !draftPreview.reviewVersion ||
      isReviewActionBusy ||
      !pendingHunkIds.length
    ) {
      return;
    }

    setBulkDecisionSubmitting(decision);
    try {
      await reviewManagedPreferenceDraftHunks(draftKind, {
        reviewId: draftPreview.reviewId,
        expectedReviewVersion: draftPreview.reviewVersion,
        items: pendingHunkIds.map((hunkId) => ({ hunkId, decision })),
      });
      const nextPreview = await refreshDraftPreview();
      await applyReviewPreview(nextPreview);
      message.success(
        t(
          decision === "accept"
            ? "admin.memoryDraftReviewAcceptAllSuccess"
            : "admin.memoryDraftReviewRejectAllSuccess",
        ),
      );
    } catch (error) {
      console.error("Submit bulk personal resource draft hunk decision failed:", error);
    } finally {
      setBulkDecisionSubmitting(null);
    }
  };

  const handleUndoReview = async () => {
    if (
      !canEdit ||
      !draftPreview?.reviewId ||
      !draftPreview.reviewVersion ||
      !draftPreview.canUndo ||
      reviewUndoing
    ) {
      return;
    }

    setReviewUndoing(true);
    try {
      await undoManagedPreferenceDraftReview(draftKind, {
        reviewId: draftPreview.reviewId,
        expectedReviewVersion: draftPreview.reviewVersion,
      });
      await refreshContent();
      message.success(t("admin.memoryDraftReviewUndoSuccess"));
    } catch (error) {
      console.error("Undo personal resource draft review failed:", error);
    } finally {
      setReviewUndoing(false);
    }
  };

  const handleDiscardDraft = () => {
    Modal.confirm({
      title: t("admin.memorySkillDraftDiscardConfirmTitle"),
      content: t("admin.memorySkillDraftDiscardConfirmContent"),
      okText: t("admin.memorySkillDraftDiscardConfirmOk"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await discardPersonalResourceDraft(apiResourceType);
          message.success(t("admin.memorySkillDraftDiscardSuccess"));
          await refreshContent();
          await onUpdated?.();
        } catch (error) {
          console.error("Discard personal resource draft failed:", error);
        }
      },
    });
  };

  const renderDiffPanel = () => {
    const fileDiff = draftPreview?.fileDiff;
    if (!fileDiff) {
      return null;
    }

    if (fileDiff.binary) {
      return (
        <Alert type="info" showIcon message={t("admin.memorySkillPackageBinaryDiffHint")} />
      );
    }

    if (fileDiff.tooLarge) {
      return (
        <Alert type="warning" showIcon message={t("admin.memorySkillPackageDiffTooLarge")} />
      );
    }

    if (!diffEntryLines.length) {
      return (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("admin.memorySkillPackageDiffEmpty")}
        />
      );
    }

    return (
      <SkillDiffHunkPanel
        diffEntryLines={diffEntryLines}
        hunkReviewActive={reviewMode}
        hunkSubmitting={hunkSubmitting}
        onHunkDecision={(hunk, decision) => void handleHunkDecision(hunk.hunkId, decision)}
        t={t}
      />
    );
  };

  if (loading) {
    return (
      <div className="memory-experience-content-loading">
        <Spin />
      </div>
    );
  }

  if (errorMessage) {
    return (
      <Alert
        type="error"
        showIcon
        message={errorMessage}
        action={
          <Button size="small" onClick={() => setRetryKey((value) => value + 1)}>
            {t("common.retry")}
          </Button>
        }
      />
    );
  }

  return (
    <div className="memory-experience-content-editor">
      <div className="memory-skill-detail-editor-toolbar">
        <div className="memory-skill-detail-editor-heading">
          <label>{t("admin.memoryExperienceDetailContent")}</label>
          {reviewMode ? (
            <span className="memory-experience-editor-status is-review">
              {t("admin.memoryExperienceReviewStatusShort")}
            </span>
          ) : hasLocalDraft ? (
            <span className="memory-experience-editor-status is-draft">
              {t("admin.memoryExperienceUncommittedStatusShort")}
            </span>
          ) : null}
        </div>
        <Space size={8} wrap className="memory-experience-editor-actions">
          {canEdit && reviewMode ? (
            <>
              <span className="memory-skill-review-stats">
                {t("admin.memorySkillReviewDecisionStats", {
                  accepted: draftPreview?.acceptedCount ?? 0,
                  rejected: draftPreview?.rejectedCount ?? 0,
                  pending: draftPreview?.pendingCount ?? 0,
                })}
              </span>
              {pendingHunkIds.length ? (
                <>
                  <Button
                    icon={<CheckOutlined />}
                    loading={bulkDecisionSubmitting === "accept"}
                    disabled={isReviewActionBusy}
                    onClick={() => void handleBulkHunkDecision("accept")}
                  >
                    {t("admin.memoryDraftReviewAcceptAll")}
                  </Button>
                  <Popconfirm
                    title={t("admin.memoryDraftReviewRejectAllConfirmTitle")}
                    description={t("admin.memoryDraftReviewRejectAllConfirmContent")}
                    okText={t("admin.memoryDraftReviewRejectAllConfirmOk")}
                    cancelText={t("common.cancel")}
                    disabled={isReviewActionBusy}
                    onConfirm={() => void handleBulkHunkDecision("reject")}
                  >
                    <Button
                      danger
                      icon={<CloseOutlined />}
                      loading={bulkDecisionSubmitting === "reject"}
                      disabled={isReviewActionBusy}
                    >
                      {t("admin.memoryDraftReviewRejectAll")}
                    </Button>
                  </Popconfirm>
                </>
              ) : null}
              {draftPreview?.canUndo ? (
                <Button
                  icon={<RollbackOutlined />}
                  loading={reviewUndoing}
                  disabled={isReviewActionBusy && !reviewUndoing}
                  onClick={() => void handleUndoReview()}
                >
                  {t("admin.memoryDraftReviewUndo")}
                </Button>
              ) : null}
              <Button
                type="primary"
                loading={committing}
                disabled={!canConfirmReview}
                onClick={() => void handleCommitDraft()}
              >
                {t("admin.memorySkillDraftConfirm")}
              </Button>
              <Button danger onClick={handleDiscardDraft}>
                {t("admin.memorySkillDraftDiscard")}
              </Button>
            </>
          ) : null}
          {canEdit && hasLocalDraft && !isEditing && !reviewMode ? (
            <>
              <Button
                type="primary"
                icon={<SaveOutlined />}
                loading={committing}
                onClick={() => void handleCommitDraft()}
              >
                {t("admin.memorySkillDraftCommit")}
              </Button>
              <Button danger onClick={handleDiscardDraft}>
                {t("admin.memorySkillDraftDiscard")}
              </Button>
            </>
          ) : null}
          {canEdit && !reviewMode ? (
            isEditing ? (
              <>
                <Button onClick={handleCancelEdit} disabled={saving}>
                  {t("common.cancel")}
                </Button>
                <Button type="primary" loading={saving} onClick={() => void handleSaveDraft()}>
                  {t("common.save")}
                </Button>
              </>
            ) : (
              <Button type="primary" onClick={handleStartEdit}>
                {t("common.edit")}
              </Button>
            )
          ) : null}
        </Space>
      </div>

      <div className="memory-experience-detail-content">
        {isEditing ? (
          <Input.TextArea
            value={displayContent}
            onChange={(event) => setDisplayContent(event.target.value)}
            autoSize={{ minRows: 18, maxRows: 32 }}
            className="memory-skill-detail-textarea"
          />
        ) : hasLocalDraft && hasDiffView ? (
          <div className="memory-diff-unified">{renderDiffPanel()}</div>
        ) : displayContent.trim() ? (
          <MarkdownViewer>{displayContent}</MarkdownViewer>
        ) : (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="-" />
        )}
      </div>
    </div>
  );
}
