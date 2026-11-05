import { useEffect, useRef, useState } from "react";
import { Button, Checkbox, Input, Progress, Radio, Select } from "antd";
import { LeftOutlined, RightOutlined, UploadOutlined, CloseOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { RcFile } from "antd/es/upload";
import ImageUpload, { type ImageUploadImperativeProps } from "../ImageUpload";
import "./index.scss";

export interface AskQuestion {
  id?: string;
  text: string;
  reason?: string;
  required?: boolean;
  choice_policy?: "seed" | "subset" | "fixed";
  boolean_labels?: { yes: string; no: string };
  validation?: {
    format?: "integer" | "positive_integer";
    example?: string;
    min_length?: number;
    max_length?: number;
    min?: number;
    max?: number;
  };
  type: "boolean" | "single" | "multiple" | "text";
  choices?: string[];
  allow_other?: boolean;
  attachment?: {
    when_choice: string;
    material_id: string;
    accept: string[];
    max_files?: number;
    required?: boolean;
  };
}

export interface AskPending {
  workflow?: { workflow_ref: string; revision_id?: string; startup_request_id: string };
  ask_id: string;
  questions: AskQuestion[];
  /** Optional group title shown at the top of the card */
  title?: string;
  /** Translation key used when the producer supplies a fixed application title. */
  title_i18n_key?: string;
  /** Optional subtitle / description shown below the title */
  description?: string;
  user_env_delete?: { id: string; name: string; expected_updated_at: string };
  env_input?: import("../EnvInputCard").EnvironmentInput;
  skill_ambiguity?: { requested_name: string; candidates: string[]; original_query: string };
  mail_draft?: import("@/modules/chat/components/MailDraftCard").MailDraftPreview;
  mail_drafts?: import("@/modules/chat/components/MailDraftCard").MailDraftPreview[];
  mail_draft_only?: boolean;
}

export interface AskAnsweredQuestion {
  id?: string;
  text: string;
  type: string;
  choices: string[]; // original choice list (from AskQuestion.choices)
  custom_choices: string[]; // user-edited choice labels
  answer: AnswerState | null;
}

export interface AskAnswersStructured {
  ask_id: string;
  questions: AskAnsweredQuestion[];
}

export interface AskSubmitPayload {
  /** Formatted text for display in the user message bubble. */
  text: string;
  /** Full structured answers, forwarded to the backend as ask_answers_structured. */
  structured: AskAnswersStructured;
  /** Uploaded through a conditional question and sent as normal chat attachments. */
  files?: (RcFile & { uri: string })[];
}

interface AskCardProps {
  askPending: AskPending;
  /** Called with a payload containing the formatted text and full structured answers. */
  onSubmit: (payload: AskSubmitPayload) => void | boolean | Promise<void | boolean>;
  disabled?: boolean;
  /** Persisted completion, distinct from a temporarily disabled or superseded card. */
  answered?: boolean;
  /** Cached answers to pre-populate (index → serialized answer) */
  savedAnswers?: Record<number, AnswerState>;
  /** Called whenever an answer changes, for external caching */
  onAnswerChange?: (index: number, ans: AnswerState) => void;
}

export type AnswerState =
  | { type: "boolean"; value: string | null }
  | { type: "single"; value: string | null; otherText: string }
  | { type: "multiple"; value: string[]; otherText: string }
  | { type: "text"; value: string };

// Must match algorithm/lazymind/chat/engine/tools/ask_user.py::_OTHER_OPTION.
const OTHER_OPTION = "__ask_user_other__";

function initAnswer(q: AskQuestion): AnswerState {
  switch (q.type) {
    case "boolean":
      return { type: "boolean", value: null };
    case "single":
      return { type: "single", value: null, otherText: "" };
    case "multiple":
      return { type: "multiple", value: [], otherText: "" };
    default:
      return { type: "text", value: "" };
  }
}

function restoreAnswer(q: AskQuestion, saved?: AnswerState): AnswerState {
  if (!saved || saved.type !== q.type) return initAnswer(q);
  const choices = q.choices?.length ? q.choices :
    q.type === "boolean" ? ["__ask_user_yes__", "__ask_user_no__"] : [];
  switch (saved.type) {
    case "text":
      return typeof saved.value === "string" ? saved : initAnswer(q);
    case "boolean":
      return saved.value === null || choices.includes(saved.value) ? saved : initAnswer(q);
    case "single":
      return saved.value === null || choices.includes(saved.value)
        ? { ...saved, otherText: saved.otherText ?? "" } : initAnswer(q);
    case "multiple":
      return Array.isArray(saved.value) && saved.value.every(value => choices.includes(value))
        ? { ...saved, otherText: saved.otherText ?? "" } : initAnswer(q);
  }
}

function isAnswered(ans: AnswerState, otherOption: string): boolean {
  switch (ans.type) {
    case "boolean":
      return ans.value !== null;
    case "single":
      if (!ans.value) return false;
      return ans.value !== otherOption || ans.otherText.trim().length > 0;
    case "multiple":
      if (ans.value.length === 0) return false;
      if (ans.value.includes(otherOption))
        return ans.otherText.trim().length > 0;
      return true;
    case "text":
      return ans.value.trim().length > 0;
  }
}

type AskAttachmentFile = RcFile & { uri?: string };

function attachmentIsActive(q: AskQuestion, ans: AnswerState): boolean {
  return Boolean(
    q.attachment && ans.type === "single" &&
    ans.value === q.attachment.when_choice,
  );
}

function questionIsComplete(
  q: AskQuestion,
  ans: AnswerState,
  files: AskAttachmentFile[],
  otherOption: string,
): boolean {
  if (!isAnswered(ans, otherOption)) return false;
  if (answerError(q, ans)) return false;
  if (!attachmentIsActive(q, ans) || !q.attachment?.required) return true;
  return files.length > 0 && files.every((file) => Boolean(file.uri));
}

function answerError(q: AskQuestion, ans: AnswerState): "required" | "length" | "integer" | null {
  if (!isAnswered(ans, OTHER_OPTION)) {
    const hasOther = (ans.type === "single" && ans.value === OTHER_OPTION) ||
      (ans.type === "multiple" && ans.value.includes(OTHER_OPTION));
    return q.required || hasOther ? "required" : null;
  }
  const text = ans.type === "text" ? ans.value.trim() :
    (ans.type === "single" && ans.value === OTHER_OPTION) ||
    (ans.type === "multiple" && ans.value.includes(OTHER_OPTION)) ? ans.otherText.trim() : null;
  const rule = q.validation;
  if (text === null || !rule) return null;
  const length = Array.from(text).length;
  if (length < (rule.min_length ?? 0) || (rule.max_length && length > rule.max_length)) return "length";
  if (rule.format) {
    const number = Number(text);
    if (!text || !Number.isFinite(number) || !Number.isInteger(number) ||
      (rule.format === "positive_integer" && number <= 0) ||
      (rule.min !== undefined && number < rule.min) || (rule.max !== undefined && number > rule.max)) return "integer";
  }
  return null;
}

function attachmentRequirementSatisfied(
  q: AskQuestion,
  ans: AnswerState,
  files: AskAttachmentFile[],
): boolean {
  if (!attachmentIsActive(q, ans) || !q.attachment?.required) return true;
  return files.length > 0 && files.every((file) => Boolean(file.uri));
}

function formatAnswer(
  q: AskQuestion,
  ans: AnswerState,
  choices: string[],
  otherOption: string,
  answerSeparator: string,
  unansweredLabel: string,
  displayChoice: (value: string) => string,
): string {
  if (!isAnswered(ans, otherOption)) {
    return `${q.text}: ${unansweredLabel}`;
  }
  switch (ans.type) {
    case "boolean":
      return `${q.text}: ${ans.value === "__ask_user_yes__" ?
        q.boolean_labels?.yes || displayChoice(ans.value) : q.boolean_labels?.no || displayChoice(ans.value ?? "")}`;
    case "single": {
      const raw = ans.value ?? "";
      // Resolve the original choice index to get the (possibly edited) label.
      const origChoices = q.choices ?? [];
      const origIdx = origChoices.indexOf(raw);
      const label = origIdx >= 0 ? (choices[origIdx] ?? raw) : raw;
      const val = raw === otherOption ? ans.otherText.trim() : label;
      return `${q.text}: ${val}`;
    }
    case "multiple": {
      const origChoices = q.choices ?? [];
      const parts = ans.value.map((v) => {
        if (v === otherOption) return ans.otherText.trim();
        const origIdx = origChoices.indexOf(v);
        return origIdx >= 0 ? (choices[origIdx] ?? v) : v;
      });
      return `${q.text}: ${parts.join(answerSeparator)}`;
    }
    case "text":
      return `${q.text}: ${ans.value.trim()}`;
  }
}

function hasConfirmationAnswer(answers: AnswerState[]) {
  return answers.length === 1 && answers[0]?.type === "boolean" &&
    (answers[0].value === "__ask_user_yes__" || answers[0].value === "__ask_user_no__");
}

function ChoiceLabel({ value }: { value: string }) {
  return <span className="ask-wizard__choice-label">{value}</span>;
}

function normalizedAskQuestions(
  askPending: AskPending,
  t: (key: string, options?: Record<string, unknown>) => string,
): AskQuestion[] {
  if (askPending.questions.length > 0) return askPending.questions;
  const target = askPending.user_env_delete?.name?.trim();
  if (!target) return askPending.questions;
  return [{
    text: t("settingsPage.envVars.deleteConfirm", { name: target }),
    type: "boolean",
    choices: ["__ask_user_yes__", "__ask_user_no__"],
  }];
}

export default function AskCard(props: AskCardProps) {
  // A new card is a new decision scope, even when it reuses the same component.
  return <AskCardContent key={props.askPending.ask_id} {...props} />;
}

function AskCardContent({
  askPending,
  onSubmit,
  disabled = false,
  answered = false,
  savedAnswers,
  onAnswerChange,
}: AskCardProps) {
  const { t } = useTranslation();
  const otherOption = OTHER_OPTION;
  const otherOptionLabel = t("chat.askCardOtherOption");
  const displayChoice = (value: string) => {
    if (value === OTHER_OPTION) return otherOptionLabel;
    if (value === "__ask_user_yes__") return t("common.yes");
    if (value === "__ask_user_no__") return t("common.no");
    return value;
  };
  const answerSeparator = t("chat.askCardAnswerSeparator");
  const {
    title,
    title_i18n_key: titleI18nKey,
    description,
  } = askPending;
  const questions = normalizedAskQuestions(askPending, t);
  const total = questions.length;
  const displayTitle = title || (titleI18nKey ? t(titleI18nKey) : "");
  const displayDescription = description;

  const [answers, setAnswers] = useState<AnswerState[]>(() =>
    questions.map((q, i) => restoreAnswer(q, savedAnswers?.[i])),
  );
  const [currentIndex, setCurrentIndex] = useState(0);
  const [submitted, setSubmitted] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [attachmentFiles, setAttachmentFiles] = useState<Record<number, AskAttachmentFile[]>>({});
  const attachmentRefs = useRef<Record<number, ImageUploadImperativeProps | null>>({});
  const submittingRef = useRef(false);
  const processed = answered || submitted;
  const isReadOnly = disabled || processed || submitting;
  const isConfirmation = !!askPending.user_env_delete;
  const decision = answers[0]?.type === "boolean" ? answers[0].value : null;
  const statusLabel = t(isConfirmation && decision === "__ask_user_yes__"
    ? "chat.askCardConfirmed"
    : isConfirmation && decision === "__ask_user_no__"
      ? "chat.askCardCancelled" : "chat.askCardAnswered");
  const needsConfirmation = isConfirmation && !hasConfirmationAnswer(answers);

  // Preserve the structured payload shape expected by the backend.
  const [customChoices] = useState<Record<number, string[]>>(() =>
    Object.fromEntries(questions.map((q, i) => [i, [...(q.choices ?? [])]])),
  );

  useEffect(() => {
    setAnswers((prev) =>
      questions.map((q, i) => restoreAnswer(q, savedAnswers?.[i] ?? prev[i])),
    );
  }, [askPending.ask_id, savedAnswers]);

  useEffect(() => {
    setAttachmentFiles({});
    attachmentRefs.current = {};
  }, [askPending.ask_id]);

  const currentQ = questions[currentIndex]!;
  const declaredMaxLength = currentQ.validation?.max_length ?? 0;
  const maxLength = declaredMaxLength > 0 ? declaredMaxLength : undefined;
  const currentAns = answers[currentIndex]!;
  const currentChoices = customChoices[currentIndex] ?? currentQ.choices ?? [];
  const currentError = answerError(currentQ, currentAns);
  const showCurrentError = Boolean(currentError && (currentError !== "required" ||
    (currentAns.type === "single" && currentAns.value === OTHER_OPTION) ||
    (currentAns.type === "multiple" && currentAns.value.includes(OTHER_OPTION))));
  const useSelect = currentQ.choice_policy === "fixed" && (currentQ.choices?.length ?? 0) > 6;
  const selectOptions = (currentQ.choices ?? []).map((value, index) => ({
    value, label: displayChoice(currentChoices[index] ?? value),
  }));

  const progressPercent = Math.round(
    (answers.filter((answer, index) => questionIsComplete(
      questions[index]!, answer, attachmentFiles[index] ?? [], otherOption,
    )).length /
      total) *
      100,
  );

  const updateAnswer = (
    idx: number,
    next: AnswerState,
    autoAdvance = false,
  ) => {
    setAnswers((prev) => {
      const updated = prev.map((a, i) => (i === idx ? next : a));
      onAnswerChange?.(idx, next);
      return updated;
    });
    if (autoAdvance && idx < total - 1) {
      setTimeout(() => setCurrentIndex(idx + 1), 180);
    }
  };

  const handleSubmit = async () => {
    if (isReadOnly || submittingRef.current) return;
    const submittedAnswers = answers;
    if (isConfirmation && !hasConfirmationAnswer(submittedAnswers)) return;
    const invalidIndex = questions.findIndex((question, index) => answerError(question, submittedAnswers[index]!));
    if (invalidIndex >= 0) { setCurrentIndex(invalidIndex); return; }
    if (questions.some((question, index) => !attachmentRequirementSatisfied(
      question, submittedAnswers[index]!, attachmentFiles[index] ?? [],
    ))) return;
    const lines = questions.map((q, i) =>
      formatAnswer(
        q,
        submittedAnswers[i]!,
        customChoices[i] ?? q.choices ?? [],
        otherOption,
        answerSeparator,
        t("chat.askCardUnanswered"),
        displayChoice,
      ),
    );
    const structured: AskAnswersStructured = {
      ask_id: askPending.ask_id,
      questions: questions.map((q, i) => ({
        ...(q.id ? { id: q.id } : {}),
        text: q.text,
        type: q.type,
        choices: q.choices ?? [],
        custom_choices: customChoices[i] ?? q.choices ?? [],
        answer: isAnswered(submittedAnswers[i]!, otherOption) ? submittedAnswers[i]! : null,
      })),
    };
    submittingRef.current = true;
    setSubmitting(true);
    try {
      const files = questions.flatMap((question, index) => (
        attachmentIsActive(question, submittedAnswers[index]!)
          ? (attachmentFiles[index] ?? []).filter((file): file is RcFile & { uri: string } => Boolean(file.uri))
          : []
      ));
      const accepted = await onSubmit({ text: lines.join("\n"), structured, files });
      if (accepted !== false && !askPending.user_env_delete) setSubmitted(true);
    } catch {
      // Ordinary callers report their own request errors; keep the answer for retry.
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  };

  const goTo = (idx: number) => {
    if (idx >= 0 && idx < total) setCurrentIndex(idx);
  };

  const canGoNext = currentIndex < total - 1;
  const canGoPrev = currentIndex > 0;

  return (
    <div
      className={`ask-wizard${isReadOnly ? " ask-wizard--disabled" : ""}`}
      aria-label={t("chat.askCardAria")}
    >
      {processed && (
        <button
          type="button"
          className="ask-wizard__summary"
          aria-expanded={expanded}
          onClick={() => setExpanded(!expanded)}
        >
          <span className="ask-wizard__summary-status">{statusLabel}</span>
          <span className="ask-wizard__summary-title">{displayTitle}</span>
          <span>{t(expanded ? "chat.askCardCollapse" : "chat.askCardExpand")}</span>
          <RightOutlined rotate={expanded ? 90 : 0} />
        </button>
      )}
      {(!processed || expanded) && <>
      {/* Header */}
      <div className="ask-wizard__header">
        <div className="ask-wizard__header-top">
          <div className="ask-wizard__title-area">
            {displayTitle && (
              <h3 className="ask-wizard__title">{displayTitle}</h3>
            )}
            {displayDescription && (
              <p className="ask-wizard__description">{displayDescription}</p>
            )}
          </div>
          <div className="ask-wizard__meta">
            <span className="ask-wizard__count">
              {currentIndex + 1} / {total}
            </span>
          </div>
        </div>
        <Progress
          percent={progressPercent}
          showInfo={false}
          size={["100%", 3]}
          className="ask-wizard__progress"
          strokeColor="#4e6ef2"
          trailColor="#e4e9f5"
        />
        <div className="ask-wizard__progress-label">
          {progressPercent}% {t("chat.askCardCompleted")}
        </div>
      </div>

      {/* Question body */}
      <div className="ask-wizard__body">
        <div className="ask-wizard__question-label">
          <span className="ask-wizard__index-badge">{currentIndex + 1}</span>
          <span className="ask-wizard__question-text">{currentQ.text}</span>
        </div>
        {currentQ.reason && <p className="ask-wizard__description">{currentQ.reason}</p>}
        {currentQ.validation?.example && <p className="ask-wizard__hint">{currentQ.validation.example}</p>}

        <div className="ask-wizard__answer-area">
          {currentQ.type === "boolean" && (
            <div className="ask-wizard__boolean-buttons">
              {(currentChoices.length > 0
                ? currentChoices
                : [t("common.yes"), t("common.no")]
              ).map((c, ci) => (
                <Button
                  key={ci}
                  type={
                    currentAns.type === "boolean" &&
                    currentAns.value === (currentQ.choices?.[ci] ?? c)
                      ? "primary"
                      : "default"
                  }
                  disabled={isReadOnly}
                  onClick={() =>
                    updateAnswer(
                      currentIndex,
                      { type: "boolean", value: currentQ.choices?.[ci] ?? c },
                      true,
                    )
                  }
                  className="ask-wizard__bool-btn"
                >
                  {ci === 0 ? currentQ.boolean_labels?.yes || displayChoice(c) : currentQ.boolean_labels?.no || displayChoice(c)}
                </Button>
              ))}
            </div>
          )}

          {currentQ.type === "single" && (
            <div className="ask-wizard__choices">
              {useSelect ? <Select
                aria-label={currentQ.text}
                showSearch optionFilterProp="label" listHeight={192} listItemHeight={32}
                style={{ width: "100%" }} options={selectOptions}
                value={currentAns.type === "single" ? currentAns.value : null}
                disabled={isReadOnly}
                onChange={(chosen: string) => {
                  if (chosen !== currentQ.attachment?.when_choice) {
                    attachmentRefs.current[currentIndex]?.clear();
                    setAttachmentFiles(previous => ({ ...previous, [currentIndex]: [] }));
                  }
                  updateAnswer(currentIndex, { type: "single", value: chosen,
                    otherText: currentAns.type === "single" ? currentAns.otherText : "" });
                }}
              /> : <>
              <Radio.Group
                value={currentAns.type === "single" ? currentAns.value : null}
                onChange={(e) => {
                  const chosen = e.target.value as string;
                  const activatesAttachment = currentQ.attachment?.when_choice === chosen;
                  if (!activatesAttachment && attachmentFiles[currentIndex]?.length) {
                    attachmentRefs.current[currentIndex]?.clear();
                    setAttachmentFiles((previous) => ({ ...previous, [currentIndex]: [] }));
                  }
                  updateAnswer(
                    currentIndex,
                    {
                      type: "single",
                      value: chosen,
                      otherText:
                        currentAns.type === "single"
                          ? currentAns.otherText
                          : "",
                    },
                    chosen !== otherOption && !activatesAttachment,
                  );
                }}
                disabled={isReadOnly}
              >
                {(currentQ.choices ?? []).map((origVal, ci) => (
                  <Radio
                    key={ci}
                    value={origVal}
                    className="ask-wizard__choice"
                  >
                    <ChoiceLabel
                      value={displayChoice(currentChoices[ci] ?? origVal)}
                    />
                  </Radio>
                ))}
              </Radio.Group>
              </>}
              {currentAns.type === "single" &&
                currentAns.value === otherOption && (
                  <Input
                    value={currentAns.otherText}
                    onChange={(e) =>
                      updateAnswer(currentIndex, {
                        type: "single",
                        value: otherOption,
                        otherText: e.target.value,
                      })
                    }
                    disabled={isReadOnly}
                    maxLength={maxLength}
                    placeholder={t("chat.askCardOtherPlaceholder")}
                    className="ask-wizard__other-input"
                  />
                )}
              {attachmentIsActive(currentQ, currentAns) && currentQ.attachment && (
                <div className="ask-wizard__attachment">
                  <ImageUpload
                    ref={(instance) => { attachmentRefs.current[currentIndex] = instance; }}
                    max={currentQ.attachment.max_files === -1 ? Infinity : (currentQ.attachment.max_files ?? 1)}
                    listNum={0}
                    types={currentQ.attachment.accept}
                    disabled={isReadOnly}
                    updateFiles={(files) => setAttachmentFiles((previous) => ({
                      ...previous,
                      [currentIndex]: files as AskAttachmentFile[],
                    }))}
                    icon={(
                      <Button icon={<UploadOutlined />} disabled={isReadOnly}>
                        {t("chat.askCardUploadFile")}
                      </Button>
                    )}
                  />
                  {(attachmentFiles[currentIndex] ?? []).map((file) => (
                    <div className="ask-wizard__attachment-file" key={file.uid}>
                      <span>{file.name}</span>
                      <Button
                        type="text"
                        size="small"
                        icon={<CloseOutlined />}
                        disabled={isReadOnly}
                        aria-label={t("chat.askCardRemoveFile")}
                        onClick={() => attachmentRefs.current[currentIndex]?.removeFile(file.uid)}
                      />
                    </div>
                  ))}
                  {currentQ.attachment.required && !questionIsComplete(
                    currentQ, currentAns, attachmentFiles[currentIndex] ?? [], otherOption,
                  ) && (
                    <span className="ask-wizard__attachment-hint">
                      {t("chat.askCardUploadRequired")}
                    </span>
                  )}
                </div>
              )}
            </div>
          )}

          {currentQ.type === "multiple" && (
            <div className="ask-wizard__choices">
              {useSelect ? <Select
                aria-label={currentQ.text} mode="multiple" showSearch optionFilterProp="label"
                listHeight={192} listItemHeight={32} style={{ width: "100%" }} options={selectOptions}
                value={currentAns.type === "multiple" ? currentAns.value : []}
                disabled={isReadOnly}
                onChange={(value: string[]) => updateAnswer(currentIndex, { type: "multiple", value,
                  otherText: currentAns.type === "multiple" ? currentAns.otherText : "" })}
              /> : <>
              <Checkbox.Group
                value={currentAns.type === "multiple" ? currentAns.value : []}
                onChange={(vals) =>
                  updateAnswer(currentIndex, {
                    type: "multiple",
                    value: vals as string[],
                    otherText:
                      currentAns.type === "multiple"
                        ? currentAns.otherText
                        : "",
                  })
                }
                disabled={isReadOnly}
              >
                {(currentQ.choices ?? []).map((origVal, ci) => (
                  <Checkbox
                    key={ci}
                    value={origVal}
                    className="ask-wizard__choice"
                  >
                    <ChoiceLabel
                      value={displayChoice(currentChoices[ci] ?? origVal)}
                    />
                  </Checkbox>
                ))}
              </Checkbox.Group>
              </>}
              {currentAns.type === "multiple" &&
                currentAns.value.includes(otherOption) && (
                  <Input
                    value={
                      currentAns.type === "multiple" ? currentAns.otherText : ""
                    }
                    onChange={(e) =>
                      updateAnswer(currentIndex, {
                        type: "multiple",
                        value:
                          currentAns.type === "multiple"
                            ? currentAns.value
                            : [],
                        otherText: e.target.value,
                      })
                    }
                    disabled={isReadOnly}
                    placeholder={t("chat.askCardOtherPlaceholder")}
                    maxLength={maxLength}
                    className="ask-wizard__other-input"
                  />
                )}
            </div>
          )}

          {currentQ.type === "text" && (
            <Input.TextArea
              value={currentAns.type === "text" ? currentAns.value : ""}
              onChange={(e) =>
                updateAnswer(currentIndex, {
                  type: "text",
                  value: e.target.value,
                })
              }
              disabled={isReadOnly}
              status={showCurrentError ? "error" : undefined}
              maxLength={maxLength}
              showCount={Boolean(maxLength)}
              placeholder={t("chat.askCardInputPlaceholder")}
              className="ask-wizard__text-input"
              autoSize={{ minRows: 2, maxRows: 5 }}
            />
          )}
          {showCurrentError && <p role="alert">{t(`chat.askCardValidation.${currentError}`)}</p>}
        </div>
      </div>

      {/* Navigation + quick jump */}
      <div className="ask-wizard__footer">
        <div className="ask-wizard__nav-buttons">
          <Button
            icon={<LeftOutlined />}
            disabled={!canGoPrev}
            onClick={() => goTo(currentIndex - 1)}
            className="ask-wizard__nav-btn"
          >
            {t("chat.askCardPrev")}
          </Button>
          {canGoNext ? (
            <Button
              type="primary"
              onClick={() => goTo(currentIndex + 1)}
              className="ask-wizard__nav-btn"
            >
              {t("chat.askCardNext")}
              <RightOutlined />
            </Button>
          ) : (
            !disabled && !processed && (
              <Button
                type="primary"
                onClick={() => handleSubmit()}
                disabled={needsConfirmation || submitting || questions.some((question, index) =>
                  Boolean(answerError(question, answers[index]!)) || !attachmentRequirementSatisfied(
                    question, answers[index]!, attachmentFiles[index] ?? [],
                  ))}
                loading={submitting}
                className="ask-wizard__submit-btn"
              >
                {t("chat.askCardSubmit")}
              </Button>
            )
          )}
        </div>

        {/* Quick-jump sidebar */}
        <div className="ask-wizard__jump-list">
          {questions.map((_, idx) => (
            <button
              key={idx}
              type="button"
              className={`ask-wizard__jump-item${idx === currentIndex ? " is-current" : ""}${questionIsComplete(questions[idx]!, answers[idx]!, attachmentFiles[idx] ?? [], otherOption) ? " is-done" : ""}`}
              onClick={() => goTo(idx)}
              aria-label={`Go to question ${idx + 1}`}
            >
              {idx + 1}
            </button>
          ))}
        </div>
      </div>

      {!isReadOnly && (
        <p className="ask-wizard__hint">{t("chat.askCardAutoSaveHint")}</p>
      )}
      </>}
    </div>
  );
}
