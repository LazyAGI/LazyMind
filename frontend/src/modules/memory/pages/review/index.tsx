import { useCallback, useEffect, useRef } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Empty,
  Input,
  Popconfirm,
  Spin,
  Space,
  Steps,
  Tooltip,
} from "antd";
import SendIcon from "@/modules/chat/assets/icons/send_icon.svg?react";
import RouteLoading from "../../components/RouteLoading";
import { useMemoryManagementOutletContext } from "../../context";
import { getSkillBodyContentForDisplay } from "../../shared";
import { DiffLineContent } from "../../components/DiffLineContent";

export default function MemoryReviewPage() {
  const {
    t,
    isReviewRouteRequested,
    activeProposal,
    isBackendSuggestionReviewMode,
    activeReviewStep,
    goToReviewChoose,
    goToReviewPreview,
    closeChangeReview,
    backendDraftSubmitting,
    backendDraftLoading,
    approvedBackendSuggestionIds,
    isAnyBackendSuggestionMutating,
    confirmBackendDraft,
    allBackendSuggestionsSelected,
    hasPartialBackendSuggestionSelection,
    setAllBackendSuggestionsSelected,
    backendRejectedSuggestionCount,
    activeBackendSuggestions,
    activeBackendSuggestionSourceText,
    selectedBackendSuggestionCount,
    backendSuggestionBatchSubmitting,
    handleBackendBatchAccept,
    handleBackendBatchRejectWithConfirm,
    backendSuggestionHasMore,
    backendSuggestionLoadingMore,
    backendSuggestionLoadMoreError,
    loadMoreBackendSuggestions,
    clearSelectedBackendSuggestions,
    backendSuggestionSubmitting,
    selectedBackendSuggestionIds,
    isBackendSuggestionSelectable,
    setBackendSuggestionSelected,
    submitBackendSuggestionDecision,
    backendDraftDiffLines,
    backendDraftReady,
    qaQuestionDraft,
    setQaQuestionDraft,
    handleReviewQuestionKeyDown,
    sendReviewQuestion,
    activeProposalDiff,
    reviewSuggestionSubmitting,
    approveChangeProposal,
    hasEffectiveChange,
    allSelectableFieldsSelected,
    hasPartialFieldSelection,
    setAllFieldsSelected,
    acceptedFieldCount,
    rejectedFieldCount,
    pendingFieldCount,
    handleBatchAcceptAndGoPreview,
    handleBatchRejectWithConfirm,
    clearSelectedFields,
    activeProposalFieldChanges,
    proposalFieldDecisions,
    getFieldDecisionActionKey,
    fieldDecisionSubmitting,
    selectedFieldKeys,
    setFieldSelected,
    submitFieldDecision,
    normalizeSuggestionValue,
    isPreviewContentEditing,
    startPreviewContentEdit,
    savePreviewContentEdit,
    manualPreviewContentDraft,
    setManualPreviewContentDraft,
  } = useMemoryManagementOutletContext();
  const backendSuggestionListRef = useRef<HTMLDivElement | null>(null);
  const maybeLoadMoreBackendSuggestions = useCallback(() => {
    const container = backendSuggestionListRef.current;
    if (
      !container ||
      !backendSuggestionHasMore ||
      backendSuggestionLoadingMore ||
      backendSuggestionBatchSubmitting === "accept" ||
      backendSuggestionBatchSubmitting === "reject"
    ) {
      return;
    }

    const distanceToBottom =
      container.scrollHeight - container.scrollTop - container.clientHeight;
    if (distanceToBottom <= 96) {
      void loadMoreBackendSuggestions();
    }
  }, [
    backendSuggestionBatchSubmitting,
    backendSuggestionHasMore,
    backendSuggestionLoadingMore,
    loadMoreBackendSuggestions,
  ]);

  useEffect(() => {
    maybeLoadMoreBackendSuggestions();
  }, [activeBackendSuggestions.length, maybeLoadMoreBackendSuggestions]);

  const originalSkill = activeProposal?.tab === "skills" ? activeProposal.before : null;
  const originalExperience =
    activeProposal?.tab === "experience" ? activeProposal.before : null;
  const originalSkillTags = Array.isArray(originalSkill?.tags)
    ? originalSkill.tags.filter(Boolean)
    : [];
  const originalSkillBodyText =
    originalSkill && typeof originalSkill.content === "string"
      ? getSkillBodyContentForDisplay(originalSkill.content)
      : "";
  const originalSourceBodyText = originalSkill
    ? originalSkillBodyText
    : originalExperience
      ? originalExperience.content
      : activeProposalDiff?.beforeText || activeBackendSuggestionSourceText;
  const sourceBodyTitle = originalSkill
    ? t("admin.memoryMarkdown")
    : originalExperience
      ? t("admin.memoryContent")
      : "";
  const renderOriginalSkillSummary = () => {
    if (!originalSkill) {
      return null;
    }

    return (
      <div className="memory-diff-skill-summary">
        <div className="memory-diff-skill-title-row">
          <span className="memory-diff-skill-label">{t("admin.memoryName")}</span>
          <strong className="memory-diff-skill-name">{originalSkill.name || "-"}</strong>
        </div>
        {originalSkill.description ? (
          <p className="memory-diff-skill-description">{originalSkill.description}</p>
        ) : null}
        <div className="memory-diff-skill-meta">
          {originalSkill.category ? (
            <div className="memory-diff-skill-meta-item">
              <span className="memory-diff-skill-meta-label">
                {t("admin.memoryCategory")}
              </span>
              <span className="memory-diff-skill-meta-value">{originalSkill.category}</span>
            </div>
          ) : null}
          <div className="memory-diff-skill-meta-item">
            <span className="memory-diff-skill-meta-label">{t("admin.memoryTagSet")}</span>
            {originalSkillTags.length ? (
              <span className="memory-diff-skill-tag-list">
                {originalSkillTags.map((tag: string) => (
                  <span key={tag} className="memory-diff-skill-tag">
                    {tag}
                  </span>
                ))}
              </span>
            ) : (
              <span className="memory-diff-skill-meta-value">-</span>
            )}
          </div>
          <div className="memory-diff-skill-meta-item">
            <span className="memory-diff-skill-meta-label">
              {t("admin.memoryAutoUpdate")}
            </span>
            <span className="memory-diff-skill-meta-value">
              {originalSkill.autoEvo ? t("admin.memoryDiffBoolYes") : t("admin.memoryDiffBoolNo")}
            </span>
          </div>
        </div>
      </div>
    );
  };
  const renderOriginalExperienceSummary = () => {
    if (!originalExperience) {
      return null;
    }

    return (
      <div className="memory-diff-skill-summary">
        <div className="memory-diff-skill-title-row">
          <span className="memory-diff-skill-label">{t("admin.memoryTitle")}</span>
          <strong className="memory-diff-skill-name">{originalExperience.title || "-"}</strong>
        </div>
        <div className="memory-diff-skill-meta">
          <div className="memory-diff-skill-meta-item">
            <span className="memory-diff-skill-meta-label">
              {t("admin.memoryAutoUpdate")}
            </span>
            <span className="memory-diff-skill-meta-value">
              {originalExperience.autoEvo
                ? t("admin.memoryDiffBoolYes")
                : t("admin.memoryDiffBoolNo")}
            </span>
          </div>
        </div>
      </div>
    );
  };

  if (isReviewRouteRequested && !activeProposal) {
    return <RouteLoading title={t("admin.memoryDiffDialogTitle")} />;
  }

  if (activeProposal && isBackendSuggestionReviewMode) {
    const canPreviewBackendDraft = Boolean(activeProposal.backendSuggestions);
    const isDraftPreviewOnly =
      (activeProposal.tab === "skills" || activeProposal.tab === "experience") &&
      canPreviewBackendDraft;
    const effectiveReviewStep = isDraftPreviewOnly ? 1 : activeReviewStep;

    return (
      <div
        className={`memory-review-page ${
          effectiveReviewStep === 0 ? "is-step-choose" : "is-step-preview"
        }`}
      >
        <div className="memory-review-workspace">
          <div className="memory-review-header">
            <div className="memory-review-title">
              <h3>{t("admin.memoryDiffDialogTitle")}</h3>
              {canPreviewBackendDraft ? (
                <Steps
                  current={isDraftPreviewOnly ? 0 : activeReviewStep}
                  className="memory-review-steps"
                  onChange={
                    isDraftPreviewOnly
                      ? undefined
                      : (nextStep) => {
                          if (nextStep === 0) {
                            goToReviewChoose();
                            return;
                          }
                          goToReviewPreview();
                        }
                  }
                  items={
                    isDraftPreviewOnly
                      ? [{ title: t("admin.memoryDiffStepPreviewTitle") }]
                      : [
                          { title: t("admin.memoryDiffStepChooseTitle") },
                          { title: t("admin.memoryDiffStepPreviewTitle") },
                        ]
                  }
                />
              ) : null}
            </div>
            <Space wrap>
              <Button onClick={closeChangeReview}>{t("common.close")}</Button>
              {canPreviewBackendDraft && !isDraftPreviewOnly && activeReviewStep === 0 ? (
                <Button
                  type="primary"
                  loading={backendDraftLoading}
                  disabled={!approvedBackendSuggestionIds.length || isAnyBackendSuggestionMutating}
                  onClick={goToReviewPreview}
                >
                  {t("admin.memoryDiffStepNext")}
                </Button>
              ) : null}
              {canPreviewBackendDraft && effectiveReviewStep === 1 ? (
                <Button
                  type="primary"
                  loading={backendDraftSubmitting === "confirm"}
                  disabled={
                    backendDraftSubmitting === "discard" ||
                    backendDraftLoading ||
                    !backendDraftReady
                  }
                  onClick={() => void confirmBackendDraft()}
                >
                  {t("admin.memoryPreferenceDraftConfirm")}
                </Button>
              ) : null}
            </Space>
          </div>
          <Alert
            type="info"
            showIcon
            message={
              canPreviewBackendDraft
                ? effectiveReviewStep === 0
                  ? isDraftPreviewOnly
                    ? activeProposal.tab === "skills"
                      ? t("admin.memoryDiffSkillDraftPreviewHint")
                      : t("admin.memoryDiffMemoryDraftPreviewHint")
                    : t("admin.memoryDiffBackendChooseHint")
                  : activeProposal.tab === "skills"
                    ? t("admin.memoryDiffSkillDraftPreviewHint")
                    : t("admin.memoryDiffMemoryDraftPreviewHint")
                : t("admin.memoryDiffBackendFallbackHint")
            }
          />
          {!isDraftPreviewOnly && (activeReviewStep === 0 || !canPreviewBackendDraft) ? (
            <div className="memory-review-grid memory-review-grid-step-choose">
              <div className="memory-review-column">
                <div className="memory-diff-raw-card">
                  <div className="memory-diff-raw-card-head">
                    <h4>{t("admin.memoryDiffBefore")}</h4>
                  </div>
                  {renderOriginalSkillSummary()}
                  {renderOriginalExperienceSummary()}
                  {sourceBodyTitle ? (
                    <div className="memory-diff-source-title">{sourceBodyTitle}</div>
                  ) : null}
                  <div className="memory-diff-source-lines">
                    {originalSourceBodyText
                      .split("\n")
                      .map((line: string, index: number) => (
                        <div key={`backend-before-${index}`} className="memory-diff-source-line">
                          {line || " "}
                        </div>
                      ))}
                  </div>
                </div>
              </div>
              <div className="memory-review-column">
                <div className="memory-diff-change-toolbar memory-backend-batch-toolbar">
                  <div className="memory-diff-change-toolbar-left memory-backend-batch-toolbar-left">
                    <Checkbox
                      checked={allBackendSuggestionsSelected}
                      indeterminate={hasPartialBackendSuggestionSelection}
                      disabled={!activeBackendSuggestions.length || isAnyBackendSuggestionMutating}
                      onChange={(event) => setAllBackendSuggestionsSelected(event.target.checked)}
                    >
                      {t("admin.memoryDiffSelectAll")}
                    </Checkbox>
                    <span>
                      {t("admin.memoryDiffDecisionStats", {
                        accepted: approvedBackendSuggestionIds.length,
                        rejected: backendRejectedSuggestionCount,
                        pending: activeBackendSuggestions.length,
                      })}
                    </span>
                  </div>
                  <Space size={8} wrap>
                    <Button
                      size="small"
                      loading={backendSuggestionBatchSubmitting === "accept" || backendDraftLoading}
                      disabled={!selectedBackendSuggestionCount || isAnyBackendSuggestionMutating}
                      onClick={handleBackendBatchAccept}
                    >
                      {t("admin.memoryDiffBatchAcceptAll")}
                    </Button>
                    <Button
                      size="small"
                      loading={backendSuggestionBatchSubmitting === "reject"}
                      disabled={!selectedBackendSuggestionCount || isAnyBackendSuggestionMutating}
                      onClick={handleBackendBatchRejectWithConfirm}
                    >
                      {t("admin.memoryDiffBatchRejectAll")}
                    </Button>
                    <Button
                      size="small"
                      onClick={clearSelectedBackendSuggestions}
                      disabled={!selectedBackendSuggestionCount || isAnyBackendSuggestionMutating}
                    >
                      {t("admin.memoryDiffBatchClear")}
                    </Button>
                  </Space>
                </div>
                <div
                  ref={backendSuggestionListRef}
                  className="memory-diff-change-list"
                  onScroll={maybeLoadMoreBackendSuggestions}
                >
                  {activeBackendSuggestions.length ? (
                    <>
                      {activeBackendSuggestions.map((suggestion: any, index: number) => {
                        const submittingDecision = backendSuggestionSubmitting[suggestion.id];
                        const isSubmitting = Boolean(submittingDecision);
                        const isSelected = selectedBackendSuggestionIds.includes(suggestion.id);
                        const isRemoveSuggestion =
                          activeProposal.tab === "skills" &&
                          String(suggestion.action || "").trim().toLowerCase() === "remove";
                        const isSelectable = isBackendSuggestionSelectable(suggestion);

                        return (
                          <div
                            className={`memory-diff-change-item memory-backend-suggestion-card ${
                              isSelected ? "is-selected" : ""
                            } ${isSubmitting ? "is-submitting" : ""} ${
                              isRemoveSuggestion ? "is-remove-suggestion" : ""
                            } ${!isSelectable ? "is-disabled" : ""}`}
                            key={suggestion.id}
                          >
                            <div className="memory-diff-change-item-head memory-backend-suggestion-card-head">
                              <div className="memory-backend-suggestion-selector">
                                <Checkbox
                                  checked={isSelected}
                                  disabled={isAnyBackendSuggestionMutating || !isSelectable}
                                  onChange={(event) =>
                                    setBackendSuggestionSelected(suggestion.id, event.target.checked)
                                  }
                                />
                                <div className="memory-diff-change-item-title">
                                  <strong>{`${index + 1}. ${suggestion.title || "-"}`}</strong>
                                  {isRemoveSuggestion ? (
                                    <span className="memory-backend-suggestion-delete-badge">
                                      {t("admin.memoryBackendSuggestionDeleteBadge")}
                                    </span>
                                  ) : null}
                                </div>
                              </div>
                              <div className="memory-diff-change-actions">
                                <Button
                                  size="small"
                                  type="primary"
                                  danger={isRemoveSuggestion}
                                  loading={submittingDecision === "accept"}
                                  disabled={isAnyBackendSuggestionMutating || !isSelectable}
                                  onClick={() =>
                                    void submitBackendSuggestionDecision(suggestion, "accept")
                                  }
                                >
                                  {t("admin.memoryDiffAcceptField")}
                                </Button>
                                <Popconfirm
                                  title={t("admin.memoryDiffRejectFieldConfirmTitle")}
                                  description={t("admin.memoryDiffRejectFieldConfirmContent")}
                                  okText={t("admin.memoryDiffRejectFieldConfirmOk")}
                                  cancelText={t("common.cancel")}
                                  okButtonProps={{ disabled: isAnyBackendSuggestionMutating }}
                                  onConfirm={() =>
                                    submitBackendSuggestionDecision(suggestion, "reject")
                                  }
                                >
                                  <Button
                                    size="small"
                                    loading={submittingDecision === "reject"}
                                    disabled={isAnyBackendSuggestionMutating || !isSelectable}
                                  >
                                    {t("admin.memoryDiffRejectField")}
                                  </Button>
                                </Popconfirm>
                              </div>
                            </div>
                            <div className="memory-diff-change-summary memory-backend-suggestion-content">
                              {suggestion.content || "-"}
                            </div>
                          </div>
                        );
                      })}
                      <div className="memory-backend-suggestion-loadmore" aria-live="polite">
                        {backendSuggestionLoadingMore ? (
                          <div className="memory-backend-suggestion-loadmore-state">
                            <Spin size="small" />
                            <span>{t("common.loading")}</span>
                          </div>
                        ) : null}
                        {!backendSuggestionLoadingMore && backendSuggestionLoadMoreError ? (
                          <div className="memory-backend-suggestion-loadmore-state is-error">
                            <span>{backendSuggestionLoadMoreError}</span>
                            <Button
                              type="link"
                              size="small"
                              onClick={() => void loadMoreBackendSuggestions()}
                            >
                              {t("common.retry")}
                            </Button>
                          </div>
                        ) : null}
                        {!backendSuggestionLoadingMore &&
                        !backendSuggestionLoadMoreError &&
                        !backendSuggestionHasMore ? (
                          <span className="memory-backend-suggestion-loadmore-end">
                            {t("admin.memoryBackendSuggestionEnd")}
                          </span>
                        ) : null}
                      </div>
                    </>
                  ) : (
                    <div className="memory-backend-suggestion-empty">
                      <Empty description={t("admin.memoryDiffNoContentChange")} />
                    </div>
                  )}
                </div>
              </div>
            </div>
          ) : (
            <div className="memory-review-grid memory-review-grid-step-preview">
              <div className="memory-review-column memory-review-column-full">
                <div className="memory-diff-preview-body">
                  <div className="memory-diff-unified">
                    {backendDraftLoading ? (
                      <div className="memory-diff-generating-state" aria-live="polite">
                        <Spin />
                        <span>{t("admin.memoryDiffPreviewGenerating")}</span>
                      </div>
                    ) : backendDraftDiffLines.length ? (
                      backendDraftDiffLines.map((line: any, index: number) => (
                        <div
                          key={`backend-diff-${index}`}
                          className={`memory-diff-line is-${line.type}`}
                        >
                          <span className="memory-diff-prefix">
                            {line.type === "add" ? "+" : line.type === "remove" ? "-" : " "}
                          </span>
                          <DiffLineContent line={line} />
                        </div>
                      ))
                    ) : (
                      <Empty description={t("admin.memoryDiffNoContentChange")} />
                    )}
                  </div>
                  <div className="memory-diff-question-box">
                    <div className="memory-diff-question-inner">
                      <Input.TextArea
                        autoSize={{ minRows: 2, maxRows: 5 }}
                        className="memory-diff-question-input"
                        value={qaQuestionDraft}
                        onChange={(event) => setQaQuestionDraft(event.target.value)}
                        onKeyDown={handleReviewQuestionKeyDown}
                        placeholder={t("admin.memoryDiffQaQuestionPlaceholder")}
                      />
                      <div className="memory-diff-question-actions">
                        <Tooltip title={t("chat.send")}>
                          <button
                            type="button"
                            className="memory-diff-send-button"
                            onClick={() => void sendReviewQuestion()}
                            disabled={!qaQuestionDraft.trim().length || backendDraftLoading}
                            aria-label={t("chat.send")}
                          >
                            <SendIcon />
                          </button>
                        </Tooltip>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
    );
  }

  if (!activeProposal || !activeProposalDiff) {
    return null;
  }

  return (
    <div
      className={`memory-review-page ${
        activeReviewStep === 0 ? "is-step-choose" : "is-step-preview"
      }`}
    >
      <div className="memory-review-workspace">
        <div className="memory-review-header">
          <div className="memory-review-title">
            <h3>{t("admin.memoryDiffDialogTitle")}</h3>
            <Steps
              current={activeReviewStep}
              className="memory-review-steps"
              onChange={(nextStep) => {
                if (nextStep === 0) {
                  goToReviewChoose();
                  return;
                }
                goToReviewPreview();
              }}
              items={[
                { title: t("admin.memoryDiffStepChooseTitle") },
                { title: t("admin.memoryDiffStepPreviewTitle") },
              ]}
            />
          </div>
          <Space wrap>
            <Button onClick={closeChangeReview}>{t("common.close")}</Button>
            {activeReviewStep === 1 ? (
              <Button onClick={goToReviewChoose}>{t("admin.memoryDiffStepPrev")}</Button>
            ) : null}
            {activeReviewStep === 1 ? (
              <Button
                type="primary"
                loading={reviewSuggestionSubmitting}
                onClick={() => void approveChangeProposal()}
              >
                {hasEffectiveChange
                  ? t("admin.memoryDiffApprove")
                  : t("admin.memoryDiffKeepOriginal")}
              </Button>
            ) : null}
          </Space>
        </div>
        <Alert
          type="info"
          showIcon
          message={
            activeReviewStep === 0
              ? t("admin.memoryDiffStepChooseHint")
              : t("admin.memoryDiffStepPreviewHint")
          }
        />
        {activeReviewStep === 0 ? (
          <div className="memory-review-grid memory-review-grid-step-choose">
            <div className="memory-review-column">
              <div className="memory-diff-raw-card">
                <div className="memory-diff-raw-card-head">
                  <h4>{t("admin.memoryDiffBefore")}</h4>
                </div>
                {renderOriginalSkillSummary()}
                {renderOriginalExperienceSummary()}
                {sourceBodyTitle ? (
                  <div className="memory-diff-source-title">{sourceBodyTitle}</div>
                ) : null}
                <div className="memory-diff-source-lines">
                  {originalSourceBodyText
                    .split("\n")
                    .map((line: string, index: number) => (
                      <div key={`source-${index}`} className="memory-diff-source-line">
                        {line || " "}
                      </div>
                    ))}
                </div>
              </div>
            </div>
            <div className="memory-review-column">
              <div className="memory-diff-change-toolbar">
                <div className="memory-diff-change-toolbar-left">
                  <Checkbox
                    checked={allSelectableFieldsSelected}
                    indeterminate={hasPartialFieldSelection}
                    onChange={(event) => setAllFieldsSelected(event.target.checked)}
                  >
                    {t("admin.memoryDiffSelectAll")}
                  </Checkbox>
                  <span>
                    {t("admin.memoryDiffDecisionStats", {
                      accepted: acceptedFieldCount,
                      rejected: rejectedFieldCount,
                      pending: pendingFieldCount,
                    })}
                  </span>
                </div>
                <Space size={6} wrap>
                  <Button size="small" onClick={handleBatchAcceptAndGoPreview}>
                    {t("admin.memoryDiffBatchAcceptAll")}
                  </Button>
                  <Button size="small" onClick={handleBatchRejectWithConfirm}>
                    {t("admin.memoryDiffBatchRejectAll")}
                  </Button>
                  <Button size="small" onClick={clearSelectedFields}>
                    {t("admin.memoryDiffBatchClear")}
                  </Button>
                </Space>
              </div>
              <div className="memory-diff-change-list">
                {activeProposalFieldChanges.length ? (
                  activeProposalFieldChanges.map((field: any, index: number) => {
                    const decision = proposalFieldDecisions[field.key] ?? "pending";
                    const isAccepted = decision === "accept";
                    const isRejected = decision === "reject";
                    const fieldActionKey = getFieldDecisionActionKey(field);
                    const submittingDecision = fieldDecisionSubmitting[fieldActionKey];
                    const isSubmittingFieldDecision = Boolean(submittingDecision);
                    const suggestionText = t("admin.memoryDiffSuggestionTemplate", {
                      field: field.label,
                      value: normalizeSuggestionValue(field.after),
                    });

                    return (
                      <div className="memory-diff-change-item" key={field.key}>
                        <div className="memory-diff-change-item-head">
                          <div className="memory-diff-change-item-title">
                            <div className="memory-diff-change-item-check">
                              <Checkbox
                                checked={selectedFieldKeys.includes(field.key)}
                                onChange={(event) =>
                                  setFieldSelected(field.key, event.target.checked)
                                }
                              >
                                {`${index + 1}. ${field.label}`}
                              </Checkbox>
                            </div>
                            {decision !== "pending" ? (
                              <span className={`memory-diff-change-decision is-${decision}`}>
                                {decision === "accept"
                                  ? t("admin.memoryDiffFieldAccepted")
                                  : t("admin.memoryDiffFieldRejected")}
                              </span>
                            ) : null}
                          </div>
                          <div className="memory-diff-change-actions">
                            <Button
                              size="small"
                              type={isAccepted ? "primary" : "default"}
                              loading={submittingDecision === "accept"}
                              disabled={isSubmittingFieldDecision}
                              onClick={() => void submitFieldDecision(field, "accept")}
                            >
                              {t("admin.memoryDiffAcceptField")}
                            </Button>
                            <Popconfirm
                              title={t("admin.memoryDiffRejectFieldConfirmTitle")}
                              description={t("admin.memoryDiffRejectFieldConfirmContent")}
                              okText={t("admin.memoryDiffRejectFieldConfirmOk")}
                              cancelText={t("common.cancel")}
                              onConfirm={() => submitFieldDecision(field, "reject")}
                            >
                              <Button
                                size="small"
                                type={isRejected ? "primary" : "default"}
                                loading={submittingDecision === "reject"}
                                disabled={isSubmittingFieldDecision}
                              >
                                {t("admin.memoryDiffRejectField")}
                              </Button>
                            </Popconfirm>
                          </div>
                        </div>
                        <div className="memory-diff-change-summary">{suggestionText}</div>
                      </div>
                    );
                  })
                ) : (
                  <Empty
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                    description={t("admin.memoryDiffNoContentChange")}
                  />
                )}
              </div>
            </div>
          </div>
        ) : (
          <div className="memory-review-grid memory-review-grid-step-preview">
            <div className="memory-review-column memory-review-column-full">
              <div className="memory-diff-preview-body">
                <div className="memory-diff-preview-toolbar">
                  <Alert
                    type="info"
                    showIcon
                    message={t("admin.memoryDiffManualEditHint")}
                  />
                  <Space size={8}>
                    <Button
                      onClick={startPreviewContentEdit}
                      disabled={isPreviewContentEditing}
                    >
                      {t("admin.memoryDiffManualChange")}
                    </Button>
                    <Button
                      type="primary"
                      onClick={savePreviewContentEdit}
                      disabled={!isPreviewContentEditing}
                    >
                      {t("admin.memoryDiffManualSave")}
                    </Button>
                  </Space>
                </div>
                {isPreviewContentEditing ? (
                  <div className="memory-diff-unified memory-diff-manual-editor">
                    <Input.TextArea
                      value={manualPreviewContentDraft}
                      onChange={(event) => setManualPreviewContentDraft(event.target.value)}
                      autoSize={false}
                      style={{ height: "100%", resize: "none" }}
                      className="memory-diff-manual-editor-input"
                      placeholder={t("admin.memoryDiffManualEditorPlaceholder")}
                    />
                  </div>
                ) : (
                  <>
                    {activeProposalDiff.isPreference ? (
                      <div className="memory-diff-unified">
                        <div className="memory-diff-preference-section">
                          <div className="memory-diff-preference-section-title">
                            {t("admin.memoryDiffPreferenceYamlSection")}
                          </div>
                          <div className="memory-diff-unified">
                            {activeProposalDiff.prefYamlDiffLines.map((line: any, index: number) => (
                              <div
                                key={`yaml-${line.type}-${index}`}
                                className={`memory-diff-line is-${line.type}`}
                              >
                                <span className="memory-diff-prefix">
                                  {line.type === "add" ? "+" : line.type === "remove" ? "-" : " "}
                                </span>
                                <DiffLineContent line={line} />
                              </div>
                            ))}
                          </div>
                        </div>
                        <div className="memory-diff-preference-section">
                          <div className="memory-diff-preference-section-title">
                            {t("admin.memoryDiffPreferenceBodySection")}
                          </div>
                          <div className="memory-diff-unified">
                            {activeProposalDiff.prefBodyDiffLines.map((line: any, index: number) => (
                              <div
                                key={`body-${line.type}-${index}`}
                                className={`memory-diff-line is-${line.type}`}
                              >
                                <span className="memory-diff-prefix">
                                  {line.type === "add" ? "+" : line.type === "remove" ? "-" : " "}
                                </span>
                                <DiffLineContent line={line} />
                              </div>
                            ))}
                          </div>
                        </div>
                      </div>
                    ) : (
                      <div className="memory-diff-unified">
                        {activeProposalDiff.lines.map((line: any, index: number) => (
                          <div
                            key={`${line.type}-${index}`}
                            className={`memory-diff-line is-${line.type}`}
                          >
                            <span className="memory-diff-prefix">
                              {line.type === "add" ? "+" : line.type === "remove" ? "-" : " "}
                            </span>
                            <DiffLineContent line={line} />
                          </div>
                        ))}
                      </div>
                    )}
                  </>
                )}
                <div className="memory-diff-question-box">
                  <div className="memory-diff-question-inner">
                    <Input.TextArea
                      autoSize={{ minRows: 2, maxRows: 5 }}
                      className="memory-diff-question-input"
                      value={qaQuestionDraft}
                      onChange={(event) => setQaQuestionDraft(event.target.value)}
                      onKeyDown={handleReviewQuestionKeyDown}
                      placeholder={t("admin.memoryDiffQaQuestionPlaceholder")}
                    />
                    <div className="memory-diff-question-actions">
                      <Tooltip title={t("chat.send")}>
                        <button
                          type="button"
                          className="memory-diff-send-button"
                          onClick={sendReviewQuestion}
                          disabled={!qaQuestionDraft.trim().length}
                          aria-label={t("chat.send")}
                        >
                          <SendIcon />
                        </button>
                      </Tooltip>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
