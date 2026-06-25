import { Avatar, Button, Divider, Flex, message, Spin, Tooltip } from "antd";
import { trim } from "lodash";
import { useEffect, useReducer, useRef } from "react";
import type { MouseEvent } from "react";
import { useTranslation } from "react-i18next";

import "./index.scss";
import {
  CopyOutlined,
  DislikeFilled,
  DislikeOutlined,
  ExclamationCircleOutlined,
  LikeFilled,
  LikeOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import {
  ChatConversationsResponseFinishReasonEnum,
  FeedBackChatHistoryRequestTypeEnum,
  Source,
} from "@/api/generated/chatbot-client";
import { AgentAppsAuth } from "@/components/auth";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import { usePluginStore } from "@/modules/chat/store/pluginPanel";
import { PluginPanel } from "@/modules/chat/components/PluginPanel";
import MultiAnswerDisplay, { type PreferenceType } from "../MultiAnswerDisplay";
import FeedbackModal from "../FeedbackModal";

const BotAvatarIcon = new URL(
  "../../assets/images/bot_avatar.png",
  import.meta.url,
).href;

async function copyTextToClipboard(text: string) {
  const normalizedText = text.trim();
  if (!normalizedText) {
    return;
  }

  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(normalizedText);
      return;
    }
  } catch {
  }

  const textarea = document.createElement("textarea");
  textarea.value = normalizedText;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.top = "0";
  textarea.style.left = "0";
  textarea.style.width = "1px";
  textarea.style.height = "1px";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";

  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, normalizedText.length);

  try {
    const copied = document.execCommand("copy");
    if (!copied) {
      throw new Error("Copy command failed");
    }
  } finally {
    document.body.removeChild(textarea);
  }
}

interface FeedbackState {
  showModal: boolean;
  isSubmitting: boolean;
  localFeedbackType: FeedBackChatHistoryRequestTypeEnum | undefined;
  localFeedbackHistoryId: string | undefined;
  targetHistoryId: string | undefined;
}

type FeedbackAction =
  | { type: "OPEN_MODAL"; historyId: string }
  | { type: "CLOSE_MODAL" }
  | { type: "SUBMIT_START" }
  | {
      type: "SUBMIT_SUCCESS";
      feedbackType: FeedBackChatHistoryRequestTypeEnum;
      historyId: string;
    }
  | { type: "SUBMIT_FAIL" }
  | {
      type: "SYNC_FROM_SERVER";
      feedbackType: FeedBackChatHistoryRequestTypeEnum | undefined;
      historyId: string | undefined;
    };

function normalizeFeedbackType(
  feedbackType: unknown,
): FeedBackChatHistoryRequestTypeEnum | undefined {
  const normalizedFeedbackType =
    typeof feedbackType === "string"
      ? feedbackType.trim().toUpperCase()
      : feedbackType;
  if (
    normalizedFeedbackType ===
      FeedBackChatHistoryRequestTypeEnum.FeedBackTypeLike ||
    normalizedFeedbackType === 1 ||
    normalizedFeedbackType === "1" ||
    normalizedFeedbackType === "LIKE"
  ) {
    return FeedBackChatHistoryRequestTypeEnum.FeedBackTypeLike;
  }
  if (
    normalizedFeedbackType ===
      FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnlike ||
    normalizedFeedbackType === 2 ||
    normalizedFeedbackType === "2" ||
    normalizedFeedbackType === "UNLIKE"
  ) {
    return FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnlike;
  }
  return undefined;
}

// ==================== Reducer ====================

function feedbackReducer(
  state: FeedbackState,
  action: FeedbackAction,
): FeedbackState {
  switch (action.type) {
    case "OPEN_MODAL":
      return {
        ...state,
        showModal: true,
        targetHistoryId: action.historyId,
      };

    case "CLOSE_MODAL":
      return {
        ...state,
        showModal: false,
        targetHistoryId: undefined,
      };

    case "SUBMIT_START":
      return {
        ...state,
        isSubmitting: true,
      };

    case "SUBMIT_SUCCESS":
      return {
        ...state,
        isSubmitting: false,
        localFeedbackType: action.feedbackType,
        localFeedbackHistoryId: action.historyId,
        showModal: false,
        targetHistoryId: undefined,
      };

    case "SUBMIT_FAIL":
      return {
        ...state,
        isSubmitting: false,
      };

    case "SYNC_FROM_SERVER":
      return {
        ...state,
        localFeedbackType: action.feedbackType,
        localFeedbackHistoryId: action.historyId,
      };

    default:
      return state;
  }
}

const AssistantMessage = (props: any) => {
  const { t } = useTranslation();
  const {
    item,
    index,
    length,
    sendMessage,
    regenerate,
    stopGeneration,
    renderText,
    updateMessage,
    sessionId,
    onPreferenceSelect,
    isLatestDualAnswer,
    onCiteMessage,
  } = props;
  const citeButtonRef = useRef<HTMLButtonElement | null>(null);
  const citeSelectionTextRef = useRef("");
  const [feedbackState, dispatch] = useReducer(feedbackReducer, {
    showModal: false,
    isSubmitting: false,
    localFeedbackType: normalizeFeedbackType(item?.feed_back),
    localFeedbackHistoryId: item?.history_id,
    targetHistoryId: undefined,
  });

  const loadActiveSession = usePluginStore((s) => s.loadActiveSession);
  // Eagerly load the plugin session so the panel appears without waiting for component mount.
  const isLast = index === length - 1;
  useEffect(() => {
    if (isLast && sessionId) {
      loadActiveSession(sessionId);
    }
  }, [isLast, sessionId, loadActiveSession]);

  const pluginSession = usePluginStore((s) =>
    sessionId ? s.sessionByConversation[sessionId] ?? null : null,
  );

  useEffect(() => {
    dispatch({
      type: "SYNC_FROM_SERVER",
      feedbackType: normalizeFeedbackType(item?.feed_back),
      historyId: item?.history_id,
    });
  }, [item?.feed_back, item?.history_id]);

  const handleCopy = async (text?: string) => {
    try {
      await copyTextToClipboard(text || "");
      message.success(t("chat.copySuccess"));
    } catch {
      message.error(t("chat.copyFailedManual"));
    }
  };

  const hideCiteButton = () => {
    citeButtonRef.current?.remove();
    citeButtonRef.current = null;
    citeSelectionTextRef.current = "";
  };

  useEffect(() => {
    return hideCiteButton;
  }, []);

  const handleCiteSelectedText = () => {
    const selectedText = citeSelectionTextRef.current.trim();
    if (!selectedText) {
      hideCiteButton();
      return;
    }
    onCiteMessage?.(selectedText);
    window.getSelection()?.removeAllRanges();
    hideCiteButton();
  };

  const showCiteButton = (text: string, top: number, left: number) => {
    let button = citeButtonRef.current;
    if (!button) {
      button = document.createElement("button");
      button.type = "button";
      button.className = "chat-cite-selection-btn";
      button.addEventListener("mousedown", (event) => {
        event.preventDefault();
      });
      button.addEventListener("click", handleCiteSelectedText);
      document.body.appendChild(button);
      citeButtonRef.current = button;
    }

    citeSelectionTextRef.current = text;
    button.textContent = t("chat.cite");
    button.style.top = `${top}px`;
    button.style.left = `${left}px`;
  };

  const handleMouseUp = (event: MouseEvent<HTMLDivElement>) => {
    const selection = window.getSelection();
    const selectedText = selection?.toString().trim() || "";
    if (!selection || !selectedText || selection.rangeCount < 1) {
      hideCiteButton();
      return;
    }

    const range = selection.getRangeAt(0);
    const currentTarget = range.commonAncestorContainer;
    const element =
      currentTarget.nodeType === Node.ELEMENT_NODE
        ? (currentTarget as Element)
        : currentTarget.parentElement;

    const messageBody = event.currentTarget.querySelector(".chat-bot");
    if (!element || !messageBody?.contains(element)) {
      hideCiteButton();
      return;
    }

    const rect = range.getBoundingClientRect();
    if (rect.width <= 0 && rect.height <= 0) {
      hideCiteButton();
      return;
    }
    showCiteButton(
      selectedText,
      Math.max(8, rect.top - 42),
      rect.left + rect.width / 2,
    );
  };

  function renderLoading() {
    return (
      <div className="chat-assistant-msg-chat-loading">
        <Spin size="small" />
        <span>{t("chat.generatingAnswer")}</span>
      </div>
    );
  }

  function renderOnboardingInfo(info: any) {
    return (
      <div className="onboarding-info">
        <div>{info.prologue}</div>
        <ul>
          {info.suggested_questions?.map((question: any, index: any) => {
            if (!question) {
              return null;
            }
            return (
              <li key={index}>
                <a onClick={() => sendMessage(question, false)}>{question}</a>
              </li>
            );
          })}
        </ul>
      </div>
    );
  }

  function renderError() {
    return (
      <div style={{ color: "#b8c3d7" }}>
        <ExclamationCircleOutlined style={{ fontSize: 20 }} />
      </div>
    );
  }

  function getSourceDisplayIndex(source: any) {
    const index = source?.index;
    if (source?.display_index !== undefined && source?.display_index !== null) {
      return source.display_index;
    }
    if (source?.document_index !== undefined && source?.document_index !== null) {
      return source.document_index;
    }
    if (typeof index === "string" && index.includes(".")) {
      return index.split(".")[0];
    }
    return index;
  }

  function getSourceDocumentKey(source: any, sourceIndex: number) {
    const displayIndex = getSourceDisplayIndex(source);
    if (displayIndex !== undefined && displayIndex !== null) {
      return `${source?.dataset_id || ""}:${source?.file_name || source?.document_id || ""}:${displayIndex}`;
    }
    if (source?.document_id) {
      return `${source?.dataset_id || ""}:${source.document_id}`;
    }
    return `source-${sourceIndex}`;
  }

  function getDocumentSources(sources: Source[]) {
    const seen = new Set<string>();
    return Object.values(sources).filter((source: any, sourceIndex: number) => {
      const key = getSourceDocumentKey(source, sourceIndex);
      if (seen.has(key)) {
        return false;
      }
      seen.add(key);
      return true;
    });
  }

  function openSource(source: any) {
    if (source?.dataset_id === "default") {
      message.error(t("chat.tempFileNotSupportJump"));
      return;
    }
    const url = `/lib/knowledge/knowledge/${source.dataset_id}/${source.document_id}?group_name=${source.group_name}&segement_id=${source.segement_id}&number=${source.segment_number}&from=chat`;
    window.open(url, "_blank");
  }

  function renderKnowledgeBase() {
    const sources = item.sources as Source[];
    if (!sources || sources.length < 1) {
      return <></>;
    }
    return (
      <div className="chat-assistant-msg-knowledge-info">
        {getDocumentSources(sources).map((source: Source, sourceIndex: number) => {
          return (
            <div
              className="chat-assistant-msg-knowledge"
              key={getSourceDocumentKey(source, sourceIndex)}
            >
              <span style={{ marginRight: "8px" }}>
                {getSourceDisplayIndex(source)}
              </span>
              <span
                className="knowledgeName"
                onClick={() => openSource(source)}
              >
                {source.file_name}
              </span>
            </div>
          );
        })}
      </div>
    );
  }

  function getCurrentFeedback(historyId?: string) {
    const resolvedHistoryId = historyId || item?.history_id;
    if (
      resolvedHistoryId &&
      feedbackState.localFeedbackHistoryId === resolvedHistoryId &&
      feedbackState.localFeedbackType
    ) {
      return feedbackState.localFeedbackType;
    }

    if (resolvedHistoryId && item?.answers) {
      const answer = item.answers.find(
        (ans: any) => ans.history_id === resolvedHistoryId,
      );
      if (answer && answer.feed_back !== undefined && answer.feed_back !== null) {
        return normalizeFeedbackType(answer.feed_back);
      }
    }

    if (!historyId || resolvedHistoryId === item?.history_id) {
      return normalizeFeedbackType(item?.feed_back);
    }

    return undefined;
  }

  const createUpdatedItem = (
    feedbackType: FeedBackChatHistoryRequestTypeEnum,
    targetHistoryId?: string,
  ) => {
    const resolvedHistoryId = targetHistoryId || item?.history_id;
    if (resolvedHistoryId && item?.answers) {
      const hasTargetAnswer = item.answers.some(
        (ans: any) => ans.history_id === resolvedHistoryId,
      );
      const updatedAnswers = item.answers.map((ans: any) =>
        ans.history_id === resolvedHistoryId
          ? { ...ans, feed_back: feedbackType }
          : { ...ans, feed_back: undefined },
      );
      return {
        ...item,
        feed_back:
          resolvedHistoryId === item?.history_id || !hasTargetAnswer
            ? feedbackType
            : undefined,
        answers: updatedAnswers,
      };
    }
    return { ...item, feed_back: feedbackType };
  };

  
  function onFeedBack(
    type: FeedBackChatHistoryRequestTypeEnum,
    historyId?: string,
  ) {
    if (feedbackState.isSubmitting) {
      return;
    }

    const targetHistoryId = historyId || item?.history_id;
    if (!targetHistoryId) {
      message.error(t("chat.historyIdMissingFeedback"));
      return;
    }

    const currentFeedBack = getCurrentFeedback(historyId);

    if (currentFeedBack === type) {
      return;
    }

    dispatch({ type: "SUBMIT_START" });

    ChatServiceApi()
      .conversationServiceFeedBackChatHistory({
        feedBackChatHistoryRequest: { history_id: targetHistoryId, type },
      })
      .then(() => {
        const updatedItem = createUpdatedItem(type, targetHistoryId);
        updateMessage(updatedItem);

        dispatch({
          type: "SUBMIT_SUCCESS",
          feedbackType: type,
          historyId: targetHistoryId,
        });
      })
      .catch(() => {
        message.error(t("chat.feedbackFailedRetry"));
        dispatch({ type: "SUBMIT_FAIL" });
      });
  }

  
  function handleDislikeClick(historyId?: string) {
    if (feedbackState.isSubmitting) {
      return;
    }

    const currentFeedBack = getCurrentFeedback(historyId);

    if (
      currentFeedBack === FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnlike
    ) {
      return;
    }

    const targetHistoryId = historyId || item?.history_id;
    if (!targetHistoryId) {
      message.error(t("chat.historyIdMissingFeedback"));
      return;
    }

    if (AgentAppsAuth.getUserInfo()?.chatUnlikeSwitch === true) {
      dispatch({ type: "OPEN_MODAL", historyId: targetHistoryId });
      return;
    }

    onFeedBack(
      FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnlike,
      historyId,
    );
  }

  
  function handleFeedbackSubmit(_reasons: string[], _comment: string) {
    const targetHistoryId = feedbackState.targetHistoryId || item?.history_id;
    if (!targetHistoryId) {
      message.error(t("chat.historyIdMissingFeedback"));
      dispatch({ type: "CLOSE_MODAL" });
      return;
    }

    if (feedbackState.isSubmitting) {
      return;
    }

    dispatch({ type: "SUBMIT_START" });

    ChatServiceApi()
      .conversationServiceFeedBackChatHistory({
        feedBackChatHistoryRequest: {
          history_id: targetHistoryId,
          type: FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnlike,
          reason: _reasons.join(","),
          expected_answer: _comment,
        } as any,
      })
      .then(() => {
        const updatedItem = createUpdatedItem(
          FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnlike,
          targetHistoryId,
        );
        updateMessage(updatedItem);

        dispatch({
          type: "SUBMIT_SUCCESS",
          feedbackType: FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnlike,
          historyId: targetHistoryId,
        });
        message.success(t("chat.thanksFeedback"));
      })
      .catch(() => {
        message.error(t("chat.feedbackSubmitFailedRetry"));
        dispatch({ type: "SUBMIT_FAIL" });
      });
  }

  function onSelectAnswer(selectedIndex: number, preference: PreferenceType) {
    const allAnswers = item.answers || [];
    const selectedAnswer = allAnswers[selectedIndex];
    const selectedHistoryId = selectedAnswer.history_id;

    const deletedHistoryIds = allAnswers
      .filter((_: any, idx: number) => idx !== selectedIndex)
      .map((answer: any) => answer.history_id);

    const promises = deletedHistoryIds.map((deletedHistoryId: string) => {
      return ChatServiceApi().conversationServiceSetChatHistory({
        setChatHistoryRequest: {
          deleted_history_id: deletedHistoryId,
          set_history_id: selectedHistoryId,
        } as any,
      });
    });

    Promise.all(promises)
      .then(() => {
        item.answer_preference = preference;
        item.selected_answer_index = selectedIndex;
        if (selectedAnswer) {
          item.delta = selectedAnswer.content || "";
          item.raw_delta = selectedAnswer.raw_content || item.raw_delta;
          item.reasoning_content = selectedAnswer.reasoning_content || "";
          item.sources = selectedAnswer.sources || item.sources;
          item.history_id = selectedAnswer.history_id || item.history_id;
          item.thinking_duration_s = selectedAnswer.thinking_duration_s;
        }
        updateMessage(item);
        onPreferenceSelect?.(preference, sessionId);
      })
      .catch(() => {
        message.error(t("chat.feedbackFailedRetry"));
      });
  }

  function renderAnswerKnowledgeBase(answerIndex: number) {
    const answer = item.answers?.[answerIndex];
    if (!answer) {
      return null;
    }

    const sources = answer.sources as Source[];
    if (!sources || sources.length < 1) {
      return null;
    }

    return (
      <div className="chat-assistant-msg-knowledge-info">
        {getDocumentSources(sources).map((source: Source, sourceIndex: number) => {
          return (
            <div
              className="chat-assistant-msg-knowledge"
              key={getSourceDocumentKey(source, sourceIndex)}
            >
              <span style={{ marginRight: "8px" }}>
                {getSourceDisplayIndex(source)}
              </span>
              <span
                className="knowledgeName"
                onClick={() => openSource(source)}
              >
                {source.file_name}
              </span>
            </div>
          );
        })}
      </div>
    );
  }

  function renderAnswerFooter(answerIndex: number, showFullToolbar = false) {
    const answer = item.answers?.[answerIndex];
    if (!answer) {
      return null;
    }

    const answerHistoryId = answer.history_id;
    const answerFeedBack = getCurrentFeedback(answerHistoryId);

    return (
      <>
        <Divider
          className="chat-assistant-msg-tool-divider"
          style={{ margin: "12px 0" }}
        />
        <div className="chat-assistant-msg-tool-chat-toolbar">
          <div>
            <Tooltip title={t("chat.copy")}>
              <Button
                className="tool-btn"
                icon={<CopyOutlined />}
                onClick={() => handleCopy(answer.content)}
              />
            </Tooltip>
            {showFullToolbar && index === length - 1 && (
              <Tooltip title={t("chat.regenerate")}>
                <Button
                  className="tool-btn"
                  icon={<ReloadOutlined />}
                  onClick={regenerate}
                />
              </Tooltip>
            )}
          </div>
          {showFullToolbar && (
            <Flex>
              {answerFeedBack ===
              FeedBackChatHistoryRequestTypeEnum.FeedBackTypeLike ? (
                <LikeFilled
                  className="tool-btn"
                  style={{
                    cursor: "not-allowed",
                    opacity: 0.6,
                    pointerEvents: "none",
                  }}
                />
              ) : (
                <LikeOutlined
                  className="tool-btn"
                  onClick={() =>
                    onFeedBack(
                      FeedBackChatHistoryRequestTypeEnum.FeedBackTypeLike,
                      answerHistoryId,
                    )
                  }
                />
              )}
              {answerFeedBack ===
              FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnlike ? (
                <DislikeFilled
                  className="tool-btn"
                  style={{
                    cursor: "not-allowed",
                    opacity: 0.6,
                    pointerEvents: "none",
                  }}
                />
              ) : (
                <DislikeOutlined
                  className="tool-btn"
                  onClick={() => handleDislikeClick(answerHistoryId)}
                />
              )}
            </Flex>
          )}
        </div>
      </>
    );
  }

  function renderFooter() {
    const currentFeedback = getCurrentFeedback();

    return (
      <>
        <Divider className="chat-assistant-msg-tool-divider" />
        <div className="chat-assistant-msg-tool-chat-toolbar">
          <div>
            <Tooltip title={t("chat.copy")}>
              <Button
                className="tool-btn"
                icon={<CopyOutlined />}
                onClick={() => handleCopy(item.delta)}
              />
            </Tooltip>
            {index === length - 1 && (
              <Tooltip title={t("chat.regenerate")}>
                <Button
                  className="tool-btn"
                  icon={<ReloadOutlined />}
                  onClick={regenerate}
                />
              </Tooltip>
            )}
          </div>
          <Flex>
            {currentFeedback ===
            FeedBackChatHistoryRequestTypeEnum.FeedBackTypeLike ? (
              <LikeFilled
                className="tool-btn"
                style={{
                  cursor: "not-allowed",
                  opacity: 0.6,
                  pointerEvents: "none",
                }}
              />
            ) : (
              <LikeOutlined
                className="tool-btn"
                onClick={() =>
                  onFeedBack(
                    FeedBackChatHistoryRequestTypeEnum.FeedBackTypeLike,
                  )
                }
              />
            )}
            {currentFeedback ===
            FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnlike ? (
              <DislikeFilled
                className="tool-btn"
                style={{
                  cursor: "not-allowed",
                  opacity: 0.6,
                  pointerEvents: "none",
                }}
              />
            ) : (
              <DislikeOutlined
                className="tool-btn"
                onClick={() => handleDislikeClick()}
              />
            )}
          </Flex>
        </div>
      </>
    );
  }

  function renderBottom() {
    if (
      item.finish_reason ===
      ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
    ) {
      return (
        <Button className="stop-btn" onClick={stopGeneration}>
          {t("chat.stopGenerate")}
        </Button>
      );
    }
    if (
      item.finish_reason ===
      ChatConversationsResponseFinishReasonEnum.FinishReasonUnknown
    ) {
      return (
        <>
          <span style={{ color: "#b8c3d7" }}>{item.errMessage}</span>
          <Button
            className="stop-btn"
            style={{ marginLeft: 10 }}
            onClick={regenerate}
          >
            {t("chat.regenerate")}
          </Button>
        </>
      );
    }
    return null;
  }

  const hasMultipleAnswers =
    item.answers && Array.isArray(item.answers) && item.answers.length >= 2;

  const hasMultipleAnswersContent =
    hasMultipleAnswers &&
    item.answers.some(
      (answer: any) =>
        (answer.content && trim(answer.content)?.length > 0) ||
        (answer.reasoning_content &&
          trim(answer.reasoning_content)?.length > 0),
    );

  const shouldShowLoading =
    !(item.delta && trim(item.delta)?.length > 0) &&
    !(item.reasoning_content && trim(item.reasoning_content)?.length > 0) &&
    !hasMultipleAnswersContent &&
    item.finish_reason ===
      ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified;

  const shouldUseMultiAnswerStyle =
    hasMultipleAnswers &&
    (item.selected_answer_index === undefined ||
      item.selected_answer_index === null);

  if (shouldUseMultiAnswerStyle) {
    return (
      <div
        className="chat-assistant-msg-multi-answer-wrap"
        onMouseUp={handleMouseUp}
      >
        <Avatar
          className="chat-avatar"
          size={"small"}
          icon={<img src={BotAvatarIcon} />}
        />
        <div className="chat-bot-box-multi">
          <div className="chat-bot">
            {shouldShowLoading
              ? renderLoading()
              : renderText({ ...item, delta: "" })}
            {item.finish_reason ===
              ChatConversationsResponseFinishReasonEnum.FinishReasonUnknown &&
              renderError()}

            {}
            <MultiAnswerDisplay
              key={item.history_id || item.id || `multi-answer-${index}`}
              answers={item.answers}
              showPreference={isLatestDualAnswer}
              renderText={(
                content: string,
                reasoningContent?: string,
                answerIndex?: number,
              ) => {
                const answer = item.answers[answerIndex || 0];
                const uniqueKey = answer?.history_id || `answer_${answerIndex}`;

                return renderText(
                  {
                    ...item,
                    delta: content,
                    reasoning_content: reasoningContent,
                    sources: answer?.sources || [],
                    thinking_duration_s: answer?.thinking_duration_s,
                  },
                  uniqueKey,
                );
              }}
              onSelectAnswer={onSelectAnswer}
              disabled={
                item.finish_reason !==
                ChatConversationsResponseFinishReasonEnum.FinishReasonStop
              }
              renderFooter={
                item.finish_reason ===
                ChatConversationsResponseFinishReasonEnum.FinishReasonStop
                  ? renderAnswerFooter
                  : undefined
              }
              renderKnowledgeBase={
                item.finish_reason ===
                ChatConversationsResponseFinishReasonEnum.FinishReasonStop
                  ? renderAnswerKnowledgeBase
                  : undefined
              }
              initialSelectedIndex={item.selected_answer_index}
              initialPreference={item.answer_preference}
              isStreaming={
                item.finish_reason ===
                ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
              }
            />
          </div>
          {index === length - 1 && renderBottom()}
          {index === length - 1 && pluginSession && sessionId && (
            <PluginPanel
              key={sessionId}
              conversationId={sessionId}
              onSendMessage={(text) => props.sendMessage?.(text)}
            />
          )}
        </div>
        <FeedbackModal
          visible={feedbackState.showModal}
          onCancel={() => dispatch({ type: "CLOSE_MODAL" })}
          onSubmit={handleFeedbackSubmit}
          submitLoading={feedbackState.isSubmitting}
        />
      </div>
    );
  }

  return (
    <div
      className="chat-assistant-msg-single-answer-wrap"
      onMouseUp={handleMouseUp}
    >
      <Avatar
        className="chat-avatar"
        size={"small"}
        icon={<img src={BotAvatarIcon} />}
      />
      <div className="chat-bot-box-single">
        <div className="chat-bot">
          {shouldShowLoading
            ? renderLoading()
            : item.onboardingInfo
              ? renderOnboardingInfo(item.onboardingInfo)
              : renderText(item)}
          {item.finish_reason ===
            ChatConversationsResponseFinishReasonEnum.FinishReasonUnknown &&
            renderError()}

          {}
          {item.finish_reason ===
            ChatConversationsResponseFinishReasonEnum.FinishReasonStop &&
            !item.onboardingInfo &&
            renderKnowledgeBase()}

          {}
          {item.finish_reason ===
            ChatConversationsResponseFinishReasonEnum.FinishReasonStop &&
            !item.onboardingInfo &&
            renderFooter()}
        </div>
        {index === length - 1 && renderBottom()}
        {index === length - 1 && pluginSession && sessionId && (
          <PluginPanel
            key={sessionId}
            conversationId={sessionId}
            onSendMessage={(text) => props.sendMessage?.(text)}
          />
        )}
      </div>
      <FeedbackModal
        visible={feedbackState.showModal}
        onCancel={() => dispatch({ type: "CLOSE_MODAL" })}
        onSubmit={handleFeedbackSubmit}
        submitLoading={feedbackState.isSubmitting}
      />
    </div>
  );
};

export default AssistantMessage;
