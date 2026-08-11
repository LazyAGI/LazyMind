import { Button, Divider, Flex, message, Spin, Tooltip } from "antd";
import { trim, debounce } from "lodash";
import { useCallback, useEffect, useReducer, useRef } from "react";
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
import { isAskPendingReadOnly } from "@/modules/chat/utils/message";
import { ChatServiceApi, decideToolLimit } from "@/modules/chat/utils/request";
import { useWorkflowStore } from "@/modules/chat/store/workflowPanel";
import { WorkflowPanel } from "@/modules/chat/components/WorkflowPanel";
import MultiAnswerDisplay, { type PreferenceType } from "../MultiAnswerDisplay";
import FeedbackModal from "../FeedbackModal";
import AskCard from "@/modules/chat/components/AskCard";
import ToolLimitCard from "@/modules/chat/components/ToolLimitCard";
import ArtifactDownloadButton from "@/modules/chat/components/ArtifactCollectorCard/ArtifactDownloadButton";
import { IdentityAvatar } from "@/modules/identityAvatar";

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
      throw new Error();
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
      feedbackType: FeedBackChatHistoryRequestTypeEnum | undefined;
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
      FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnspecified ||
    normalizedFeedbackType === 0 ||
    normalizedFeedbackType === "0"
  ) {
    return undefined;
  }
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
  const onCiteMessageRef = useRef(onCiteMessage);
  onCiteMessageRef.current = onCiteMessage;
  // Debounced backend persistence for ask-card answers. Created once per component
  // instance with useRef so it is stable across re-renders.
  const persistAskAnswersRef = useRef(
    debounce((sid: string, hid: string, answers: Record<number, any>) => {
      ChatServiceApi().conversationServiceSaveAskAnswers(sid, hid, answers).catch(() => {});
    }, 600),
  );
  const [feedbackState, dispatch] = useReducer(feedbackReducer, {
    showModal: false,
    isSubmitting: false,
    localFeedbackType: normalizeFeedbackType(item?.feed_back),
    localFeedbackHistoryId: item?.history_id,
    targetHistoryId: undefined,
  });

  const loadActiveSession = useWorkflowStore((s) => s.loadActiveSession);
  // Eagerly load the workflow session so the panel appears without waiting for component mount.
  const isLast = index === length - 1;
  useEffect(() => {
    if (isLast && sessionId) {
      loadActiveSession(sessionId);
    }
  }, [isLast, sessionId, loadActiveSession]);

  const workflowSession = useWorkflowStore((s) =>
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

  const hideCiteButton = useCallback(() => {
    citeButtonRef.current?.remove();
    citeButtonRef.current = null;
    citeSelectionTextRef.current = "";
  }, []);

  const handleCiteSelectedText = useCallback(() => {
    const selectedText = citeSelectionTextRef.current.trim();
    if (!selectedText) {
      hideCiteButton();
      return;
    }
    onCiteMessageRef.current?.(selectedText);
    window.getSelection()?.removeAllRanges();
    hideCiteButton();
  }, [hideCiteButton]);

  const handleCiteSelectedTextRef = useRef(handleCiteSelectedText);
  handleCiteSelectedTextRef.current = handleCiteSelectedText;

  const showCiteButton = useCallback(
    (text: string, top: number, left: number) => {
      let button = citeButtonRef.current;
      if (!button) {
        button = document.createElement("button");
        button.type = "button";
        button.className = "chat-cite-selection-btn";
        // Keep selection while clicking the cite button.
        button.addEventListener("mousedown", (event) => {
          event.preventDefault();
          event.stopPropagation();
        });
        button.addEventListener("pointerdown", (event) => {
          event.stopPropagation();
        });
        button.addEventListener("click", (event) => {
          event.stopPropagation();
          handleCiteSelectedTextRef.current();
        });
        document.body.appendChild(button);
        citeButtonRef.current = button;
      }

      citeSelectionTextRef.current = text;
      button.textContent = t("chat.cite");
      button.style.top = `${top}px`;
      button.style.left = `${left}px`;
    },
    [t],
  );

  // Dismiss cite float on any outside click (capture), so it closes even when
  // the click lands outside this message's onMouseUp handler.
  useEffect(() => {
    const dismissOnPointerDown = (event: PointerEvent) => {
      const button = citeButtonRef.current;
      if (!button) {
        return;
      }
      if (event.target instanceof Node && button.contains(event.target)) {
        return;
      }
      window.getSelection()?.removeAllRanges();
      hideCiteButton();
    };

    document.addEventListener("pointerdown", dismissOnPointerDown, true);
    return () => {
      document.removeEventListener("pointerdown", dismissOnPointerDown, true);
      hideCiteButton();
    };
  }, [hideCiteButton]);

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

    // Prefer line boxes from getClientRects(): cross-block / wrapped selections
    // make getBoundingClientRect() as wide as the container, pushing the button right.
    const lineRects = Array.from(range.getClientRects()).filter(
      (lineRect) => lineRect.width > 0 && lineRect.height > 0,
    );
    if (lineRects.length === 0) {
      hideCiteButton();
      return;
    }

    const top = Math.min(...lineRects.map((lineRect) => lineRect.top));
    const left = Math.min(...lineRects.map((lineRect) => lineRect.left));
    const right = Math.max(...lineRects.map((lineRect) => lineRect.right));
    const centerX = (left + right) / 2;
    // Keep the fixed button inside the viewport (button ~ translateX(-50%)).
    const clampedLeft = Math.min(
      Math.max(centerX, 28),
      window.innerWidth - 28,
    );

    showCiteButton(selectedText, Math.max(8, top - 42), clampedLeft);
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

  function getFeedbackRecord(historyId?: string) {
    const resolvedHistoryId = historyId || item?.history_id;
    if (resolvedHistoryId && item?.answers) {
      const answer = item.answers.find(
        (candidate: any) => candidate.history_id === resolvedHistoryId,
      );
      if (answer) {
        return answer;
      }
    }
    return item;
  }

  const createUpdatedItem = (
    feedbackType: FeedBackChatHistoryRequestTypeEnum | undefined,
    targetHistoryId?: string,
    details?: { reason?: string; expectedAnswer?: string },
  ) => {
    const resolvedHistoryId = targetHistoryId || item?.history_id;

    const applyFeedbackFields = (
      record: any,
      nextFeedBack: FeedBackChatHistoryRequestTypeEnum | undefined,
    ) => {
      if (nextFeedBack !== undefined) {
        return {
          ...record,
          feed_back: nextFeedBack,
          reason: details?.reason,
          expected_answer: details?.expectedAnswer,
        };
      }
      return {
        ...record,
        feed_back: undefined,
        reason: undefined,
        expected_answer: undefined,
      };
    };

    if (resolvedHistoryId && item?.answers) {
      const hasTargetAnswer = item.answers.some(
        (ans: any) => ans.history_id === resolvedHistoryId,
      );
      const updatedAnswers = item.answers.map((ans: any) =>
        ans.history_id === resolvedHistoryId
          ? applyFeedbackFields(ans, feedbackType)
          : ans,
      );
      const itemLevelFeedback =
        resolvedHistoryId === item?.history_id || !hasTargetAnswer
          ? feedbackType
          : undefined;
      return applyFeedbackFields(
        { ...item, answers: updatedAnswers },
        itemLevelFeedback,
      );
    }
    return applyFeedbackFields(item, feedbackType);
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
    const isCancel = currentFeedBack === type;
    const requestType = isCancel
      ? FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnspecified
      : type;
    const nextFeedbackType = isCancel ? undefined : type;

    dispatch({ type: "SUBMIT_START" });

    ChatServiceApi()
      .conversationServiceFeedBackChatHistory({
        feedBackChatHistoryRequest: {
          history_id: targetHistoryId,
          type: requestType,
        },
      })
      .then(() => {
        const updatedItem = createUpdatedItem(nextFeedbackType, targetHistoryId);
        updateMessage(updatedItem);

        dispatch({
          type: "SUBMIT_SUCCESS",
          feedbackType: nextFeedbackType,
          historyId: targetHistoryId,
        });
      })
      .catch(() => {
        dispatch({ type: "SUBMIT_FAIL" });
      });
  }

  function handleDislikeClick(historyId?: string) {
    if (feedbackState.isSubmitting) {
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
          { reason: _reasons.join(","), expectedAnswer: _comment },
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
      .catch(() => {});
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
            <ArtifactDownloadButton
              sessionId={sessionId}
              historyId={answerHistoryId}
            />
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
                  onClick={() =>
                    onFeedBack(
                      FeedBackChatHistoryRequestTypeEnum.FeedBackTypeLike,
                      answerHistoryId,
                    )
                  }
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
                  onClick={() => handleDislikeClick(answerHistoryId)}
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
            <ArtifactDownloadButton
              sessionId={sessionId}
              historyId={item.history_id}
            />
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
                onClick={() =>
                  onFeedBack(
                    FeedBackChatHistoryRequestTypeEnum.FeedBackTypeLike,
                  )
                }
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
                onClick={() => handleDislikeClick()}
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
      item.tool_limit_pending &&
      item.tool_limit_pending.decision_id !== item.resolved_tool_limit_decision_id &&
      sessionId &&
      item.finish_reason ===
        ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
    ) {
      return (
        <ToolLimitCard
          key={item.tool_limit_pending.decision_id}
          pending={item.tool_limit_pending}
          onDecision={async (action) => {
            const decisionId = item.tool_limit_pending.decision_id;
            await decideToolLimit(
              sessionId,
              decisionId,
              action,
            );
            updateMessage({
              id: item.id,
              history_id: item.history_id,
              tool_limit_pending: undefined,
              resolved_tool_limit_decision_id: decisionId,
            });
          }}
        />
      );
    }
    // Render ask_pending card if present
    if (item.ask_pending) {
      const askPending = item.ask_pending;
      const isReadOnly = isAskPendingReadOnly(
        item.ask_answered,
        index === length - 1,
      );
      return (
        <AskCard
          key={askPending.ask_id}
          askPending={askPending}
          disabled={isReadOnly}
          savedAnswers={item.ask_saved_answers}
          onAnswerChange={(idx, ans) => {
            const currentAnswers = { ...(item.ask_saved_answers || {}), [idx]: ans };
            // Update in-memory message immediately so answers survive session switches.
            updateMessage({ ...item, ask_saved_answers: currentAnswers });
            // Debounced write to backend so answers survive page reload.
            if (sessionId && item.history_id) {
              persistAskAnswersRef.current(sessionId, item.history_id, currentAnswers);
            }
          }}
          onSubmit={(payload) => {
            persistAskAnswersRef.current.cancel();
            // Mark the card as answered in memory so it shows as disabled immediately.
            updateMessage({ ...item, ask_answered: true, ask_saved_answers: undefined });
            props.sendMessage?.(payload.text, undefined, { ask_answers_structured: payload.structured });
          }}
        />
      );
    }
    // Show stop button while still streaming (no card present).
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
    // Show error + regenerate on unknown/failed finish.
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
  const modalFeedbackRecord = getFeedbackRecord(feedbackState.targetHistoryId);

  if (shouldUseMultiAnswerStyle) {
    return (
      <div
        className="chat-assistant-msg-multi-answer-wrap"
        onMouseUp={handleMouseUp}
      >
        <IdentityAvatar
          className="chat-avatar"
          kind="soul"
          size={32}
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
          {(item.ask_pending || index === length - 1) && renderBottom()}
          {index === length - 1 && workflowSession && sessionId && (
            <WorkflowPanel
              key={sessionId}
              conversationId={sessionId}
              onSendMessage={(text) => props.sendMessage?.(text)}
              onStop={props.stopGeneration}
            />
          )}
        </div>
        <FeedbackModal
          visible={feedbackState.showModal}
          onCancel={() => dispatch({ type: "CLOSE_MODAL" })}
          onSubmit={handleFeedbackSubmit}
          submitLoading={feedbackState.isSubmitting}
          initialReason={modalFeedbackRecord?.reason}
          initialComment={modalFeedbackRecord?.expected_answer}
        />
      </div>
    );
  }

  return (
    <div
      className="chat-assistant-msg-single-answer-wrap"
      onMouseUp={handleMouseUp}
    >
      <IdentityAvatar
        className="chat-avatar"
        kind="soul"
        size={32}
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
        {(item.ask_pending || index === length - 1) && renderBottom()}
        {index === length - 1 && workflowSession && sessionId && (
          <WorkflowPanel
            key={sessionId}
            conversationId={sessionId}
            onSendMessage={(text) => props.sendMessage?.(text)}
            onStop={props.stopGeneration}
          />
        )}
      </div>
      <FeedbackModal
        visible={feedbackState.showModal}
        onCancel={() => dispatch({ type: "CLOSE_MODAL" })}
        onSubmit={handleFeedbackSubmit}
        submitLoading={feedbackState.isSubmitting}
        initialReason={modalFeedbackRecord?.reason}
        initialComment={modalFeedbackRecord?.expected_answer}
      />
    </div>
  );
};

export default AssistantMessage;
