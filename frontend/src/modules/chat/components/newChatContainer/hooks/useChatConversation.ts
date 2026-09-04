import { useEffect, useRef, useState, type RefObject } from "react";
import { message, Modal } from "antd";
import { useNavigate } from "react-router-dom";
import {
  ChatConversationsRequestActionEnum,
  ChatConversationsResponseFinishReasonEnum,
} from "@/api/generated/chatbot-client";
import { allowedImageTypes } from "../../ImageUpload";
import type {
  ChatFileList,
  ChatInputImperativeProps,
  SendMessageParams,
} from "../../ChatInput/types";
import { RoleTypes } from "@/modules/chat/constants/common";
import {
  CHAT_AUTO_ADVANCE_EVENT,
  CHAT_FFMPEG_DEPENDENCY_MISSING_EVENT,
  type ChatAutoAdvanceDetail,
} from "@/modules/chat/constants/chat";
import { streamManager } from "@/modules/chat/utils/StreamManager";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import UIUtils from "@/modules/chat/utils/ui";
import { emitConversationActivity } from "@/modules/chat/utils/conversationActivity";
import {
  buildChatMessageListFromHistory,
  getRegenerationInputs,
  mergeChatMessageLists,
  stripAskUserReceipt,
} from "@/modules/chat/utils/message";
import { mergeChatStreamDelta } from "@/modules/chat/utils/streamDelta";
import { splitThinkingContent } from "@/modules/chat/utils/thinking";
import {
  buildCitedMessageText,
  MAX_CITE_MESSAGE_COUNT,
} from "../utils/citeMessage";
import { getFileUrls } from "../utils/fileInputs";
import type { ChatContainerProps } from "../types";
import type { useUserMessageEdit } from "./useUserMessageEdit";
import { useChatScroll } from "./useChatScroll";
import { waitForRuntimeCapability } from "@/runtime/readiness";
import {
  idleStreamRecoveryState,
  isTemporaryStreamFailure,
  preserveProviderRetryAfterReconciliation,
  recoveryActionAfterFailure,
  recoveryDelayForAttempt,
  removeTrailingEmptyAssistantPlaceholder,
  StreamRecoveryRegistry,
  STREAM_RECOVERY_MAX_ATTEMPTS,
  STREAM_RECOVERY_SUCCESS_DURATION_MS,
  type StreamRecoveryEntry,
  type StreamRecoveryViewState,
} from "@/modules/chat/utils/streamRecovery";
import {
  applyChatStreamFailure,
  parseCoreChatStreamError,
} from "@/modules/chat/utils/chatStreamError";

type UserEditApi = ReturnType<typeof useUserMessageEdit>;
type RuntimeWaitingOperation = "chat" | "workflow";

interface UseChatConversationOptions {
  canChat: boolean;
  disabledReason?: string;
  onOpenSSE: ChatContainerProps["onOpenSSE"];
  onOpenResumeSSE?: ChatContainerProps["onOpenResumeSSE"];
  onConversationIdChange?: ChatContainerProps["onConversationIdChange"];
  setIsChatContent: ChatContainerProps["setIsChatContent"];
  clearStorePendingMessage: () => void;
  clearCiteMessages: () => void;
  chatInputRef: RefObject<ChatInputImperativeProps>;
  thinkingCollapseMap: Map<string, boolean>;
  getUserEdit: () => UserEditApi | undefined;
  isModelSelectionSaving?: () => boolean;
  concurrentStream?: boolean;
  onRequestPendingChange?: (pending: boolean) => void;
  t: (key: string) => string;
}

export function useChatConversation({
  canChat,
  disabledReason,
  onOpenSSE,
  onOpenResumeSSE,
  onConversationIdChange,
  setIsChatContent,
  clearStorePendingMessage,
  clearCiteMessages,
  chatInputRef,
  thinkingCollapseMap,
  getUserEdit,
  isModelSelectionSaving,
  concurrentStream = false,
  onRequestPendingChange,
  t,
}: UseChatConversationOptions) {
  const navigate = useNavigate();
  const sseRef = useRef<any>(null);
  const activeStreamRef = useRef(false);
  const fileRef = useRef<any>(null);
  const currentConversationIdRef = useRef<string>("");
  const messageListRef = useRef<any[]>([]);
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const conversationMessagesCache = useRef<Map<string, any[]>>(new Map());
  const ffmpegErrorBufferRef = useRef("");
  const ffmpegPromptOpenRef = useRef(false);
  const runtimeWaitAbortRef = useRef<AbortController | null>(null);
  const runtimeWaitInProgressRef = useRef(false);
  const regenerateInProgressRef = useRef(false);
  const pendingClientConversationIdRef = useRef("");
  const streamRecoveryRegistryRef = useRef(new StreamRecoveryRegistry());
  const streamRecoverySuccessTimerRef = useRef<
    ReturnType<typeof setTimeout> | null
  >(null);

  const [messageList, setMessageList] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [content, setContent] = useState("");
  const [fileList, setFileList] = useState<ChatFileList[]>([]);
  const [isStreaming, setIsStreaming] = useState(false);
  const [runtimeWaiting, setRuntimeWaiting] = useState(false);
  const [runtimeWaitingOperation, setRuntimeWaitingOperation] =
    useState<RuntimeWaitingOperation>("chat");
  const [streamRecovery, setStreamRecovery] =
    useState<StreamRecoveryViewState>(idleStreamRecoveryState());

  const scroll = useChatScroll({
    chatInputRef,
    messageListLength: messageList.length,
    thinkingCollapseMap,
  });

  function showFFmpegDependencyPrompt() {
    if (ffmpegPromptOpenRef.current) {
      return;
    }
    ffmpegPromptOpenRef.current = true;
    ffmpegErrorBufferRef.current = "";
    Modal.confirm({
      title: t("chat.ffmpegGifRequiredTitle"),
      content: t("chat.ffmpegGifRequiredDesc"),
      okText: t("chat.configureFfmpeg"),
      cancelText: t("common.close"),
      onOk: () => navigate("/settings?section=system_tools#ffmpeg-dependency"),
      afterClose: () => {
        ffmpegPromptOpenRef.current = false;
      },
    });
  }

  useEffect(() => {
    window.addEventListener(
      CHAT_FFMPEG_DEPENDENCY_MISSING_EVENT,
      showFFmpegDependencyPrompt,
    );
    return () => {
      runtimeWaitAbortRef.current?.abort();
      window.removeEventListener(
        CHAT_FFMPEG_DEPENDENCY_MISSING_EVENT,
        showFFmpegDependencyPrompt,
      );
      if (saveTimerRef.current) {
        clearTimeout(saveTimerRef.current);
        const currentId = currentConversationIdRef.current;
        if (currentId && streamManager.hasActiveStream(currentId)) {
          streamManager.saveMessageList(currentId, messageListRef.current);
        }
      }
      streamRecoveryRegistryRef.current.clearAll();
      if (streamRecoverySuccessTimerRef.current) {
        clearTimeout(streamRecoverySuccessTimerRef.current);
        streamRecoverySuccessTimerRef.current = null;
      }

      streamManager.cleanupFinishedStreams();
      conversationMessagesCache.current.clear();

      if (currentConversationIdRef.current) {
        if (streamManager.hasActiveStream(currentConversationIdRef.current)) {
          disconnectConversationStream(currentConversationIdRef.current);
        }
        if (!concurrentStream) {
          streamManager.setActiveConversation(null);
        }
      }
    };
  }, []);

  async function waitForChatRuntime(
    operation: RuntimeWaitingOperation = "chat",
  ) {
    if (runtimeWaitInProgressRef.current) {
      return false;
    }

    runtimeWaitInProgressRef.current = true;
    const controller = new AbortController();
    runtimeWaitAbortRef.current = controller;

    try {
      await waitForRuntimeCapability("chat", {
        signal: controller.signal,
        onWaiting: () => {
          setRuntimeWaitingOperation(operation);
          setRuntimeWaiting(true);
        },
      });
      return true;
    } catch (error) {
      if ((error as Error)?.name !== "AbortError") {
        message.error(t("runtime.initializationFailed"));
      }
      return false;
    } finally {
      if (runtimeWaitAbortRef.current === controller) {
        runtimeWaitAbortRef.current = null;
      }
      runtimeWaitInProgressRef.current = false;
      setRuntimeWaiting(false);
    }
  }

  function clearMultiData() {
    setFileList([]);
    fileRef.current?.clear();
  }

  function closeSSE() {
    sseRef.current = null;
    activeStreamRef.current = false;
    setLoading(false);
    setIsStreaming(false);
  }

  function rollbackFailedStreamOpen(conversationId: string, stream?: any) {
    if (streamManager.getStream(conversationId)) {
      streamManager.closeAndCleanup(conversationId);
    } else {
      try {
        stream?.close?.();
      } catch (error) {
        console.error("Failed to close rejected chat stream:", error);
      }
    }
    if (currentConversationIdRef.current === conversationId) {
      if (conversationId.startsWith("temp_")) {
        currentConversationIdRef.current = "";
      }
      closeSSE();
    }
  }

  function disconnectConversationStream(conversationId: string) {
    if (!conversationId) {
      return;
    }

    if (currentConversationIdRef.current === conversationId && sseRef.current) {
      try {
        sseRef.current.close();
      } catch (error) {
        console.error("Error closing active SSE:", error);
      }
    }

    streamManager.closeAndCleanup(conversationId);
    activeStreamRef.current = false;
    setLoading(false);
    setIsStreaming(false);
  }

  function updateAssistantMessage(data: any, id?: string, index?: number) {
    setMessageList((list) => {
      const newList = [...list];
      const targetIndex =
        index !== undefined
          ? index
          : id
            ? newList.findIndex((msg) => msg.id === id || msg.history_id === id)
            : newList.length - 1;
      if (targetIndex >= 0) {
        newList[targetIndex] = { ...newList[targetIndex], ...data };
      }
      messageListRef.current = newList;
      const currentId = currentConversationIdRef.current;
      if (currentId) {
        conversationMessagesCache.current.set(currentId, newList);
      }
      return newList;
    });
    if (!id && scroll.isMouseScrollingRef.current) {
      scroll.scrollToEnd();
    }
  }

  function updateVisibleRecovery(
    conversationId: string,
    entry: StreamRecoveryEntry,
    displayedAttempt = entry.attempt,
  ) {
    if (currentConversationIdRef.current !== conversationId) {
      return;
    }
    setStreamRecovery({
      conversationId,
      status: entry.status,
      attempt: displayedAttempt,
      maxAttempts: STREAM_RECOVERY_MAX_ATTEMPTS,
    });
  }

  function clearStreamRecovery(conversationId: string) {
    streamRecoveryRegistryRef.current.clear(conversationId);
    if (currentConversationIdRef.current === conversationId) {
      if (streamRecoverySuccessTimerRef.current) {
        clearTimeout(streamRecoverySuccessTimerRef.current);
        streamRecoverySuccessTimerRef.current = null;
      }
      setStreamRecovery(idleStreamRecoveryState(conversationId));
    }
  }

  function settleStreamRecoveryFromMessage(
    sourceConversationIds: string[],
    visibleConversationId: string,
    continuesGenerating: boolean,
  ) {
    let recoveredAttempt: number | undefined;
    for (const conversationId of new Set(sourceConversationIds.filter(Boolean))) {
      const entry = streamRecoveryRegistryRef.current.get(conversationId);
      if (entry?.status === "resuming") {
        recoveredAttempt = Math.max(recoveredAttempt ?? 0, entry.attempt);
      }
      streamRecoveryRegistryRef.current.clear(conversationId);
    }
    if (
      recoveredAttempt === undefined ||
      currentConversationIdRef.current !== visibleConversationId
    ) {
      return;
    }

    if (streamRecoverySuccessTimerRef.current) {
      clearTimeout(streamRecoverySuccessTimerRef.current);
      streamRecoverySuccessTimerRef.current = null;
    }
    if (!continuesGenerating) {
      setStreamRecovery(idleStreamRecoveryState(visibleConversationId));
      return;
    }

    setStreamRecovery({
      conversationId: visibleConversationId,
      status: "recovered",
      attempt: recoveredAttempt,
      maxAttempts: STREAM_RECOVERY_MAX_ATTEMPTS,
    });
    streamRecoverySuccessTimerRef.current = setTimeout(() => {
      streamRecoverySuccessTimerRef.current = null;
      if (currentConversationIdRef.current !== visibleConversationId) {
        return;
      }
      setStreamRecovery((current) =>
        current.conversationId === visibleConversationId &&
        current.status === "recovered"
          ? idleStreamRecoveryState(visibleConversationId)
          : current,
      );
    }, STREAM_RECOVERY_SUCCESS_DURATION_MS);
  }

  function conversationHasAuthoritativeTerminal(conversationId: string) {
    const list =
      currentConversationIdRef.current === conversationId
        ? messageListRef.current
        : (conversationMessagesCache.current.get(conversationId) ?? []);
    const latestAssistant = list.findLast(
      (item) => item?.role === RoleTypes.ASSISTANT,
    );
    return Boolean(latestAssistant?.run_terminal || latestAssistant?.run_status);
  }

  function applyReconciledHistory(conversationId: string, apiList: any[]) {
    if (apiList.length === 0) {
      return;
    }
    const cached = conversationMessagesCache.current.get(conversationId) ?? [];
    const baseList =
      currentConversationIdRef.current === conversationId
        ? messageListRef.current
        : cached;
    const merged = preserveProviderRetryAfterReconciliation(
      mergeChatMessageLists(apiList, baseList),
      baseList,
      RoleTypes.ASSISTANT,
    );
    conversationMessagesCache.current.set(conversationId, merged);
    streamManager.saveMessageList(conversationId, merged);
    if (currentConversationIdRef.current === conversationId) {
      messageListRef.current = merged;
      setMessageList(merged);
      scroll.isMouseScrollingRef.current = true;
      scroll.scrollToEnd();
    }
  }

  function stopStreamAfterReconciliation(conversationId: string) {
    streamManager.closeAndCleanup(conversationId);
    if (currentConversationIdRef.current === conversationId) {
      try {
        sseRef.current?.close();
      } catch (error) {
        console.error("Error closing reconciled SSE:", error);
      }
      closeSSE();
    }
  }

  async function reconcileStreamRecovery(
    conversationId: string,
  ): Promise<"terminal" | "generating" | "stopped" | "unavailable"> {
    const [statusResult, historyResult] = await Promise.allSettled([
      ChatServiceApi().conversationServiceGetChatStatus({ conversationId }),
      ChatServiceApi().conversationServiceGetConversationHistory({
        name: conversationId,
      }),
    ]);

    const isGenerating =
      statusResult.status === "fulfilled"
        ? Boolean(statusResult.value.data?.is_generating)
        : undefined;
    if (historyResult.status === "fulfilled") {
      const history = historyResult.value.data.history ?? [];
      const cachedList =
        currentConversationIdRef.current === conversationId
          ? messageListRef.current
          : (conversationMessagesCache.current.get(conversationId) ?? []);
      const activeHistoryId = cachedList.findLast(
        (item) => item?.role === RoleTypes.ASSISTANT,
      )?.history_id;
      const latestHistory =
        history.find((record) => record.id === activeHistoryId) ??
        history.reduce<(typeof history)[number] | undefined>(
          (latest, record) =>
            !latest || Number(record.seq || 0) > Number(latest.seq || 0)
              ? record
              : latest,
          undefined,
        );
      const historyHasTerminal = Boolean(
        latestHistory?.run_terminal || latestHistory?.run_status,
      );
      const apiList = buildChatMessageListFromHistory(
        history as Parameters<typeof buildChatMessageListFromHistory>[0],
        {
          isGenerating: isGenerating === true && !historyHasTerminal,
        },
      );
      applyReconciledHistory(conversationId, apiList);
      if (historyHasTerminal) {
        clearStreamRecovery(conversationId);
        stopStreamAfterReconciliation(conversationId);
        return "terminal";
      }
    }

    if (isGenerating === true) {
      return "generating";
    }
    if (isGenerating === false) {
      return "stopped";
    }
    return "unavailable";
  }

  function markStreamRecoveryFailed(conversationId: string) {
    const entry = streamRecoveryRegistryRef.current.ensure(conversationId);
    if (entry.timer) {
      clearTimeout(entry.timer);
      entry.timer = null;
    }
    entry.status = "failed";
    entry.attempt = STREAM_RECOVERY_MAX_ATTEMPTS;
    const sourceList =
      currentConversationIdRef.current === conversationId
        ? messageListRef.current
        : (conversationMessagesCache.current.get(conversationId) ?? []);
    const preservedList = removeTrailingEmptyAssistantPlaceholder(
      sourceList,
      RoleTypes.ASSISTANT,
    );
    if (preservedList !== sourceList) {
      conversationMessagesCache.current.set(conversationId, preservedList);
      streamManager.saveMessageList(conversationId, preservedList);
      if (currentConversationIdRef.current === conversationId) {
        messageListRef.current = preservedList;
        setMessageList(preservedList);
      }
    }
    updateVisibleRecovery(conversationId, entry);
    stopStreamAfterReconciliation(conversationId);
  }

  function markStructuredChatFailure(
    conversationId: string,
    semanticCode: string,
  ) {
    clearStreamRecovery(conversationId);
    const sourceList =
      currentConversationIdRef.current === conversationId
        ? messageListRef.current
        : (conversationMessagesCache.current.get(conversationId) ?? []);
    const failedList = applyChatStreamFailure(
      sourceList,
      RoleTypes.ASSISTANT,
      semanticCode,
    );
    if (conversationId) {
      conversationMessagesCache.current.set(conversationId, failedList);
      streamManager.saveMessageList(conversationId, failedList);
    }
    if (currentConversationIdRef.current === conversationId) {
      messageListRef.current = failedList;
      setMessageList(failedList);
    }
    if (conversationId) {
      stopStreamAfterReconciliation(conversationId);
    } else {
      closeSSE();
    }
  }

  function confirmPendingClientConversation(conversationId: string) {
    if (
      !conversationId ||
      pendingClientConversationIdRef.current !== conversationId
    ) {
      return false;
    }
    pendingClientConversationIdRef.current = "";
    onConversationIdChange?.(conversationId);
    return true;
  }

  function scheduleStreamRecovery(conversationId: string) {
    if (!onOpenResumeSSE) {
      markStreamRecoveryFailed(conversationId);
      return;
    }
    const entry = streamRecoveryRegistryRef.current.ensure(conversationId);
    if (entry.timer || entry.status === "failed") {
      return;
    }
    const nextAttempt = entry.attempt + 1;
    if (nextAttempt > STREAM_RECOVERY_MAX_ATTEMPTS) {
      void handleStreamRecoveryFailure(conversationId, 0);
      return;
    }
    entry.status = "resuming";
    updateVisibleRecovery(conversationId, entry, nextAttempt);
    entry.timer = setTimeout(() => {
      const currentEntry = streamRecoveryRegistryRef.current.get(conversationId);
      if (!currentEntry) {
        return;
      }
      currentEntry.timer = null;
      currentEntry.attempt = nextAttempt;
      void openResumeSSE(conversationId, true).then((opened) => {
        if (!opened) {
          void handleStreamRecoveryFailure(conversationId, 0);
        }
      });
    }, recoveryDelayForAttempt(nextAttempt));
  }

  async function handleStreamRecoveryFailure(
    conversationId: string,
    status: number,
  ) {
    if (conversationHasAuthoritativeTerminal(conversationId)) {
      clearStreamRecovery(conversationId);
      stopStreamAfterReconciliation(conversationId);
      return;
    }

    const entry = streamRecoveryRegistryRef.current.ensure(conversationId);
    if (!isTemporaryStreamFailure(status)) {
      const result = await reconcileStreamRecovery(conversationId);
      if (result !== "terminal") {
        markStreamRecoveryFailed(conversationId);
      }
      return;
    }

    const action = recoveryActionAfterFailure(entry.attempt);
    if (action === "retry") {
      scheduleStreamRecovery(conversationId);
      return;
    }

    const result = await reconcileStreamRecovery(conversationId);
    if (result === "terminal") {
      return;
    }
    if (action === "reconcile" && result === "generating") {
      scheduleStreamRecovery(conversationId);
      return;
    }
    markStreamRecoveryFailed(conversationId);
  }

  function onError(e: any) {
    if (e.type !== "error") {
      return;
    }

    let errorConversationId = currentConversationIdRef.current;
    try {
      const data = (e as any).data;
      if (typeof data === "string") {
        const parsed = JSON.parse(data);
        if (parsed?.result?.conversation_id) {
          errorConversationId = parsed.result.conversation_id;
        }
      }
    } catch {
      // ignore malformed error payload
    }

    if (errorConversationId) {
      const mappedError = parseCoreChatStreamError(
        (e as any).data,
        (e as any).status,
      );
      if (mappedError) {
        confirmPendingClientConversation(errorConversationId);
        if (!conversationHasAuthoritativeTerminal(errorConversationId)) {
          markStructuredChatFailure(
            errorConversationId,
            mappedError.semanticCode,
          );
          return;
        }
      }
      streamManager.removeStreamEntry(errorConversationId);
      void handleStreamRecoveryFailure(
        errorConversationId,
        Number((e as any).status || 0),
      );
    }
  }

  function onTimeout(e: any) {
    if (e.type !== "timeout") {
      return;
    }
    onError({ type: "error", data: e.data, status: 0 });
  }

  function onMessage(e: any) {
    const result = UIUtils.jsonParser(e.data)?.result;
    if (!result) {
      return;
    }
    ffmpegErrorBufferRef.current = (
      ffmpegErrorBufferRef.current + JSON.stringify(result)
    ).slice(-8192);
    if (
      !ffmpegPromptOpenRef.current &&
      ffmpegErrorBufferRef.current.includes("FFMPEG_DEPENDENCY_MISSING")
    ) {
      showFFmpegDependencyPrompt();
    }

    const messageConversationId = result.conversation_id || "";
    const currentConversationIdAtStart = currentConversationIdRef.current;
    const pendingClientConversationId = pendingClientConversationIdRef.current;
    if (
      pendingClientConversationId &&
      messageConversationId &&
      messageConversationId !== pendingClientConversationId
    ) {
      markStructuredChatFailure(
        pendingClientConversationId,
        "protocol_error",
      );
      return;
    }
    const isUsingTempId = currentConversationIdAtStart.startsWith("temp_");
    const isActiveConversation =
      !messageConversationId ||
      messageConversationId === currentConversationIdAtStart ||
      (isUsingTempId && !!messageConversationId);

    const isFirstTimeReceivingId =
      result.conversation_id &&
      isActiveConversation &&
      (result.conversation_id !== currentConversationIdRef.current ||
        result.conversation_id === pendingClientConversationId);

    if (isFirstTimeReceivingId) {
      if (!confirmPendingClientConversation(result.conversation_id)) {
        onConversationIdChange?.(result.conversation_id);
      }

      const previousConversationId = currentConversationIdRef.current;
      const isPreviousTempId = previousConversationId.startsWith("temp_");

      if (isPreviousTempId) {
        const currentList = messageListRef.current;
        conversationMessagesCache.current.set(
          previousConversationId,
          currentList,
        );

        currentConversationIdRef.current = result.conversation_id;
        if (!concurrentStream) {
          streamManager.setActiveConversation(result.conversation_id);
        }

        if (sseRef.current) {
          const tempStream = streamManager.getStream(previousConversationId);
          if (tempStream) {
            const tempCallbacks = streamManager.getCallbacks(
              previousConversationId,
            );
            if (tempCallbacks) {
              if (tempCallbacks.message) {
                tempStream.removeEventListener(
                  "message",
                  tempCallbacks.message,
                );
              }
              if (tempCallbacks.error) {
                tempStream.removeEventListener("error", tempCallbacks.error);
              }
              if (tempCallbacks.timeout) {
                tempStream.removeEventListener(
                  "timeout",
                  tempCallbacks.timeout,
                );
              }
            }
          }
          streamManager.clearStreamState(previousConversationId);
          streamManager.removeStreamEntry(previousConversationId);

          const streamCallbacks: Record<string, (event: CustomEvent) => void> =
            {
              message: (event) => onMessage(event),
              error: (event) => onError(event),
              timeout: (event) => onTimeout(event),
            };
          streamManager.registerStream(
            result.conversation_id,
            sseRef.current,
            streamCallbacks,
            event,
            { allowConcurrent: concurrentStream },
          );

          const cachedList = conversationMessagesCache.current.get(
            previousConversationId,
          );
          if (cachedList) {
            conversationMessagesCache.current.set(
              result.conversation_id,
              cachedList,
            );
            conversationMessagesCache.current.delete(previousConversationId);
          }

          streamManager.saveMessageList(result.conversation_id, currentList);
        }
      }

      const firstUserMessage = messageListRef.current.find(
        (item) => item.role === RoleTypes.USER,
      );
      const initialDisplayName = (
        firstUserMessage?.display_delta ||
        firstUserMessage?.delta ||
        ""
      ).trim();
      emitConversationActivity({
        conversationId: result.conversation_id,
        displayName: initialDisplayName || undefined,
      });
    }

    const runTerminal =
      result.runtime_event?.type === "run_finished"
        ? result.runtime_event.data
        : undefined;
    const legacyTerminal = Boolean(
      result.finish_reason &&
        result.finish_reason !==
          ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
    );
    const allRunsFinished = Boolean(
      (runTerminal || legacyTerminal) &&
        (messageConversationId || currentConversationIdAtStart) &&
        streamManager.isStreamFinished(
          messageConversationId || currentConversationIdAtStart,
        ),
    );
    const finalRunTerminal = allRunsFinished
      ? streamManager.getAggregatedRunTerminal(
          messageConversationId || currentConversationIdAtStart,
        )
      : undefined;
    const recoveredIntoTerminalFailure = Boolean(
      runTerminal &&
        ["failed", "interrupted", "cancelled"].includes(runTerminal.status),
    );
    settleStreamRecoveryFromMessage(
      [currentConversationIdAtStart, messageConversationId],
      messageConversationId || currentConversationIdAtStart,
      !allRunsFinished && !recoveredIntoTerminalFailure,
    );

    if (
      isActiveConversation &&
      (finalRunTerminal?.status === "completed" ||
        result.finish_reason ===
          ChatConversationsResponseFinishReasonEnum.FinishReasonStop)
    ) {
      scroll.isMouseScrollingRef.current = true;
    }

    if (allRunsFinished) {
      if (isActiveConversation) {
        setIsStreaming(false);
        closeSSE();
      }

      const cleanupConversationId =
        messageConversationId || currentConversationIdAtStart;
      if (cleanupConversationId) {
        streamManager.closeAndCleanup(cleanupConversationId);
        if (isActiveConversation) {
          conversationMessagesCache.current.delete(cleanupConversationId);
        }
      }
    }

    const updateMessageListInternal = (list: any[]) => {
      const newList = [...list];
      const runtimeEventType = result.runtime_event?.type;
      const runtimeEventData = result.runtime_event?.data;
      const scheduledRetry =
        runtimeEventType === "model_retry_scheduled" &&
        runtimeEventData &&
        typeof runtimeEventData === "object"
          ? {
              retry_index: Number(runtimeEventData.retry_index || 0),
              max_attempts: Number(runtimeEventData.max_attempts || 1),
            }
          : undefined;
      const clearsRetry =
        runtimeEventType === "model_call_finished" ||
        runtimeEventType === "run_finished";
      if (result.history_id) {
        const lastUserIndex = newList.findLastIndex(
          (item) => item?.role === RoleTypes.USER,
        );
        const lastUser = newList[lastUserIndex];
        if (lastUserIndex >= 0 && lastUser && !lastUser.history_id) {
          newList[lastUserIndex] = {
            ...lastUser,
            history_id: result.history_id,
            seq: result.seq,
          };
        }
      }
      let assistantMessage =
        newList.length > 0 ? newList[newList.length - 1] : null;
      let assistantMessageIndex = newList.length - 1;
      if (result.history_id) {
        const existingAssistantIndex = newList.findIndex(
          (item) =>
            item?.role === RoleTypes.ASSISTANT &&
            item?.history_id === result.history_id,
        );
        if (existingAssistantIndex >= 0) {
          assistantMessageIndex = existingAssistantIndex;
          assistantMessage = newList[existingAssistantIndex];
        }
      }

      const incomingExternalSequence = Number(
        result.external_event_sequence || 0,
      );
      const currentExternalSequence = Number(
        assistantMessage?.external_event_sequence || 0,
      );
      const executionChanged =
        !!result.execution &&
        JSON.stringify(result.execution) !==
          JSON.stringify(assistantMessage?.execution);
      if (
        incomingExternalSequence > 0 &&
        currentExternalSequence >= incomingExternalSequence &&
        (!result.history_id ||
          result.history_id === assistantMessage?.history_id) &&
        !executionChanged
      ) {
        return newList;
      }

      const isLastAssistantCompleted =
        assistantMessage?.role === RoleTypes.ASSISTANT &&
        assistantMessage?.finish_reason ===
          ChatConversationsResponseFinishReasonEnum.FinishReasonStop;

      if (
        !assistantMessage ||
        assistantMessage.role !== RoleTypes.ASSISTANT ||
        isLastAssistantCompleted
      ) {
        assistantMessage = {
          role: RoleTypes.ASSISTANT,
          delta: "",
          reasoning_content: "",
          finish_reason:
            ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
          answers: [],
        };
        newList.push(assistantMessage);
        assistantMessageIndex = newList.length - 1;
      }

      const previousRawDelta =
        assistantMessage.raw_delta || assistantMessage.delta || "";
      const mergedRawDelta = mergeChatStreamDelta(
        previousRawDelta,
        result.delta || "",
        result.delta_mode,
      );
      const splitResult = splitThinkingContent(
        mergedRawDelta,
        assistantMessage.reasoning_content || "",
      );

      assistantMessage = {
        ...assistantMessage,
        ...result,
        seq: result.seq ?? assistantMessage.seq,
        // Raw upstream/provider diagnostics must remain server-side only.
        errMessage: undefined,
        error_message: undefined,
        provider_raw_error: undefined,
        model_retry: scheduledRetry ??
          (clearsRetry ? undefined : assistantMessage.model_retry),
        run_terminal:
          (runtimeEventType === "run_finished" && runtimeEventData
            ? runtimeEventData
            : undefined) ||
          finalRunTerminal ||
          assistantMessage.run_terminal,
        run_status: finalRunTerminal?.status || assistantMessage.run_status,
        id: result.messageId,
        raw_delta: mergedRawDelta,
        delta: stripAskUserReceipt(
          splitResult.content,
          !!(result.ask_pending || assistantMessage.ask_pending),
        ),
        reasoning_content: splitResult.reasoning_content,
        sources:
          result.sources && result.sources.length > 0
            ? result.sources
            : assistantMessage.sources,
      };

      newList[assistantMessageIndex] = assistantMessage;
      return newList;
    };

    if (isActiveConversation) {
      setMessageList((list) => {
        const newList = updateMessageListInternal(list);
        messageListRef.current = newList;

        const currentId = currentConversationIdRef.current;
        if (currentId) {
          conversationMessagesCache.current.set(currentId, newList);
        }

        if (currentId && streamManager.hasActiveStream(currentId)) {
          if (saveTimerRef.current) {
            clearTimeout(saveTimerRef.current);
          }
          saveTimerRef.current = setTimeout(() => {
            streamManager.saveMessageList(currentId, messageListRef.current);
            saveTimerRef.current = null;
          }, 100);
        }

        return newList;
      });

      if (scroll.isMouseScrollingRef.current) {
        scroll.scrollToEnd();
      }
    }
  }

  const openSSE = async (
    input: any[],
    action: ChatConversationsRequestActionEnum,
    extras?: Record<string, unknown>,
  ) => {
    let conversationId = currentConversationIdRef.current;
    if (!conversationId) {
      conversationId = `temp_${Date.now()}_${Math.random().toString(36).substring(2, 15)}`;
      currentConversationIdRef.current = conversationId;
    }
    clearStreamRecovery(conversationId);
    onRequestPendingChange?.(true);

    const operation = extras?.run_in_background === true ? "workflow" : "chat";
    if (!(await waitForChatRuntime(operation))) {
      if (action === ChatConversationsRequestActionEnum.ChatActionNext) {
        markStructuredChatFailure(
          conversationId,
          "service_unavailable",
        );
      }
      onRequestPendingChange?.(false);
      return false;
    }

    activeStreamRef.current = true;
    setLoading(true);
    setIsStreaming(true);

    const callbacks: Record<string, (e: CustomEvent) => void> = {
      message: (e) => onMessage(e),
      error: (e) => onError(e),
      timeout: (e) => onTimeout(e),
    };

    let sse: any;
    try {
      const requestExtras = {
        ...extras,
        __prepareClientConversationId: (preparedId: unknown) => {
          if (typeof preparedId !== "string" || !preparedId.trim()) {
            return;
          }
          const normalizedId = preparedId.trim();
          conversationId = normalizedId;
          currentConversationIdRef.current = normalizedId;
          pendingClientConversationIdRef.current = normalizedId;
        },
      };
      const sseOrPromise = onOpenSSE(input, action, {}, requestExtras);
      sse =
        sseOrPromise instanceof Promise ? await sseOrPromise : sseOrPromise;
      sseRef.current = sse;

      streamManager.registerStream(conversationId, sse, callbacks, undefined, {
        allowConcurrent: concurrentStream,
      });
      if (!concurrentStream) {
        streamManager.setActiveConversation(conversationId);
      }

      const currentList = messageListRef.current;
      conversationMessagesCache.current.set(conversationId, currentList);
      streamManager.saveMessageList(conversationId, currentList);
    } catch (error) {
      console.error("Failed to open chat SSE:", error);
      if (action === ChatConversationsRequestActionEnum.ChatActionNext) {
        markStructuredChatFailure(conversationId, "transport_error");
      } else {
        rollbackFailedStreamOpen(conversationId, sse);
      }
      onRequestPendingChange?.(false);
      return false;
    }
    return true;
  };

  async function syncGeneratingHistory(conversationId: string) {
    try {
      const statusRes = await ChatServiceApi().conversationServiceGetChatStatus(
        {
          conversationId,
        },
      );
      if (!statusRes.data?.is_generating) {
        return;
      }
      const historyRes =
        await ChatServiceApi().conversationServiceGetConversationHistory({
          name: conversationId,
        });
      const apiList = buildChatMessageListFromHistory(historyRes.data.history, {
        isGenerating: true,
      });
      if (apiList.length === 0) {
        return;
      }
      const cached =
        conversationMessagesCache.current.get(conversationId) ?? [];
      const baseList =
        currentConversationIdRef.current === conversationId
          ? messageListRef.current
          : cached;
      const merged = mergeChatMessageLists(apiList, baseList);
      conversationMessagesCache.current.set(conversationId, merged);
      streamManager.saveMessageList(conversationId, merged);
      if (currentConversationIdRef.current === conversationId) {
        messageListRef.current = merged;
        setMessageList(merged);
        scroll.isMouseScrollingRef.current = true;
        scroll.scrollToEnd();
      }
    } catch {
      // ignore sync failures; resume SSE still proceeds
    }
  }

  async function openResumeSSE(
    conversationId: string,
    isRecoveryCycle = false,
  ): Promise<boolean> {
    if (!onOpenResumeSSE) {
      return false;
    }
    onRequestPendingChange?.(true);
    if (!(await waitForChatRuntime())) {
      if (!isRecoveryCycle) {
        void handleStreamRecoveryFailure(conversationId, 0);
      }
      onRequestPendingChange?.(false);
      return false;
    }
    if (streamManager.hasActiveStream(conversationId)) {
      streamManager.closeAndCleanup(conversationId);
    }
    activeStreamRef.current = true;
    setLoading(true);
    setIsStreaming(true);
    currentConversationIdRef.current = conversationId;

    const callbacks: Record<string, (e: CustomEvent) => void> = {
      message: (e) => onMessage(e),
      error: (e) => onError(e),
      timeout: (e) => onTimeout(e),
    };
    const latestAssistant = messageListRef.current.findLast(
      (item) => item?.role === RoleTypes.ASSISTANT,
    );
    try {
      const sseOrPromise = onOpenResumeSSE(
        conversationId,
        {},
        {
          historyId: latestAssistant?.history_id,
          afterSequence: Number(latestAssistant?.external_event_sequence || 0),
        },
      );
      const sse =
        sseOrPromise instanceof Promise ? await sseOrPromise : sseOrPromise;
      sseRef.current = sse;

      streamManager.registerStream(conversationId, sse, callbacks, undefined, {
        allowConcurrent: concurrentStream,
      });
      if (!concurrentStream) {
        streamManager.setActiveConversation(conversationId);
      }
      const currentList = messageListRef.current;
      conversationMessagesCache.current.set(conversationId, currentList);
      streamManager.saveMessageList(conversationId, currentList);
      return true;
    } catch (error) {
      console.error("Failed to open resume SSE:", error);
      rollbackFailedStreamOpen(conversationId, sseRef.current);
      if (!isRecoveryCycle) {
        void handleStreamRecoveryFailure(conversationId, 0);
      }
      onRequestPendingChange?.(false);
      return false;
    }
  }

  function ensureAutoAdvanceUserTurn(
    conversationId: string,
    driverMessage: string,
  ) {
    const text = (driverMessage || "").trim();
    if (!text) return;

    const cached = conversationMessagesCache.current.get(conversationId) ?? [];
    const sourceList =
      currentConversationIdRef.current === conversationId
        ? messageListRef.current
        : cached;
    const lastUser = sourceList.findLast((msg) => msg?.role === RoleTypes.USER);
    const alreadyHasUserTurn =
      lastUser?.delta === text || lastUser?.display_delta === text;

    if (alreadyHasUserTurn) {
      conversationMessagesCache.current.set(conversationId, sourceList);
      streamManager.saveMessageList(conversationId, sourceList);
      return;
    }

    const create_time = new Date().toISOString();
    const userMessage = {
      delta: text,
      display_delta: text,
      role: RoleTypes.USER,
      inputs: [{ input_type: "text", text }],
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      create_time,
      model_mode: "value_engineering",
      auto_advance: true,
    };
    const assistantMessage = {
      role: RoleTypes.ASSISTANT,
      delta: "",
      reasoning_content: "",
      finish_reason:
        ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
      answers: [],
      sources: [],
      model_mode: "value_engineering",
    };
    const nextList = [...sourceList, userMessage, assistantMessage];
    conversationMessagesCache.current.set(conversationId, nextList);
    streamManager.saveMessageList(conversationId, nextList);

    if (currentConversationIdRef.current === conversationId) {
      messageListRef.current = nextList;
      setMessageList(nextList);
      scroll.isMouseScrollingRef.current = true;
      scroll.scrollToEnd();
    }
  }

  function appendAutoAdvanceTurn(
    conversationId: string,
    driverMessage: string,
  ) {
    ensureAutoAdvanceUserTurn(conversationId, driverMessage);
    void openResumeSSE(conversationId);
  }

  useEffect(() => {
    const handleAutoAdvance = (event: Event) => {
      const detail = (event as CustomEvent<ChatAutoAdvanceDetail>).detail;
      if (!detail?.conversationId) return;
      if (detail.phase === "append") {
        ensureAutoAdvanceUserTurn(
          detail.conversationId,
          detail.driverMessage || "",
        );
        return;
      }
      if (detail.phase === "resume") {
        if (detail.conversationId !== currentConversationIdRef.current) {
          return;
        }
        // Conversation-level events (notably ask_pending) are emitted alongside
        // the active chat stream. Reopening that same stream here disconnects it
        // and replays the in-flight assistant turn, which renders the response
        // twice. A resume is only needed when no chat stream is currently open
        // (for example, a background auto-chat reaching a user boundary).
        if (streamManager.hasActiveStream(detail.conversationId)) {
          return;
        }
        void syncGeneratingHistory(detail.conversationId).finally(() => {
          void openResumeSSE(detail.conversationId);
        });
      }
    };
    window.addEventListener(CHAT_AUTO_ADVANCE_EVENT, handleAutoAdvance);
    return () => {
      window.removeEventListener(CHAT_AUTO_ADVANCE_EVENT, handleAutoAdvance);
    };
  }, []);

  async function sendMessage(params: SendMessageParams) {
    const {
      text,
      citeMessage: paramsCiteMessage,
      citeMessages: paramsCiteMessages,
      citeHistoryIds: paramsCiteHistoryIds,
      clearInput = true,
      create_time,
    } = params;
    const normalizedText = text.trim();
    if (!canChat) {
      if (disabledReason) {
        message.warning(disabledReason);
      }
      return;
    }
    if (
      activeStreamRef.current ||
      runtimeWaitInProgressRef.current ||
      loading ||
      isModelSelectionSaving?.() ||
      !normalizedText
    ) {
      return;
    }
    const normalizedCiteMessages =
      paramsCiteMessages
        ?.map((item) => item.trim())
        .filter(Boolean)
        .slice(0, MAX_CITE_MESSAGE_COUNT) ??
      (paramsCiteMessage?.trim() ? [paramsCiteMessage.trim()] : []);
    const textWithCitation = buildCitedMessageText(
      normalizedText,
      normalizedCiteMessages,
    );

    const submittedFileList = params.fileList ?? fileList;
    if (params.fileList) {
      setFileList(submittedFileList);
    }
    if (params?.fileListRef) {
      fileRef.current = params.fileListRef.current;
    }

    const tempGroup =
      Object.groupBy(submittedFileList, (item) => {
        const name = item.name ?? "";
        const suffix = name.substring(name.lastIndexOf(".")).toLowerCase();
        return allowedImageTypes.includes(suffix) ? "image" : "file";
      }) ?? {};
    const tempFileGroup =
      Object.groupBy(params?.files ?? [], (item) => {
        const name = item.name ?? "";
        const suffix = name.substring(name.lastIndexOf(".")).toLowerCase();
        return allowedImageTypes.includes(suffix) ? "image" : "file";
      }) ?? {};

    const inputs = [
      { input_type: "text", text: textWithCitation },
      ...getFileUrls(tempFileGroup?.image, tempGroup?.image).map((image) => ({
        input_type: "image",
        uri: image.uri || "",
        input_base64: image.base64 || "",
      })),
      ...getFileUrls(tempFileGroup?.file, tempGroup?.file).map((file) => ({
        input_type: "file",
        uri: file.uri || "",
      })),
    ];

    if (clearInput) {
      setContent("");
      clearMultiData();
    }

    const userMessage = {
      delta: normalizedText,
      display_delta: normalizedText,
      cite_message: normalizedCiteMessages.join("\n\n"),
      cite_messages: normalizedCiteMessages,
      cite_history_ids: paramsCiteHistoryIds?.filter(
        (historyId): historyId is string => Boolean(historyId?.trim()),
      ),
      role: RoleTypes.USER,
      images: tempGroup?.image,
      files: tempGroup?.file,
      fileList: submittedFileList,
      inputs,
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      create_time,
      model_mode: "value_engineering",
      mentions: params.mentions || [],
    };
    const assistantMessage = {
      role: RoleTypes.ASSISTANT,
      delta: "",
      reasoning_content: "",
      finish_reason:
        ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
      answers: [],
      sources: [],
      model_mode: "value_engineering",
    };
    const newMessageList = [
      ...messageListRef.current,
      userMessage,
      assistantMessage,
    ];
    messageListRef.current = newMessageList;
    setMessageList(newMessageList);

    scroll.isMouseScrollingRef.current = true;
    scroll.scrollToEnd();
    const opened = await openSSE(
      inputs,
      ChatConversationsRequestActionEnum.ChatActionNext,
      {
        ...(params.run_in_background ? { run_in_background: true } : {}),
        ...(params.thinking_depth
          ? { thinking_depth: params.thinking_depth }
          : {}),
        ...(params.mentions?.length ? { mentions: params.mentions } : {}),
        ...(paramsCiteHistoryIds?.length
          ? {
              cite_history_ids: paramsCiteHistoryIds.filter(
                (historyId): historyId is string => Boolean(historyId?.trim()),
              ),
            }
          : {}),
        ...(params.ask_answers_structured
          ? { ask_answers_structured: params.ask_answers_structured }
          : {}),
        ...(params.mail_draft_confirm_id
          ? { mail_draft_confirm_id: params.mail_draft_confirm_id }
          : {}),
        ...(typeof params.mail_draft_confirm_revision === "number" &&
        Number.isFinite(params.mail_draft_confirm_revision) &&
        params.mail_draft_confirm_revision > 0
          ? { mail_draft_confirm_revision: params.mail_draft_confirm_revision }
          : {}),
      },
    );
    if (!opened) {
      return;
    }

    const currentId = currentConversationIdRef.current;
    if (currentId) {
      conversationMessagesCache.current.set(currentId, newMessageList);
      streamManager.saveMessageList(currentId, newMessageList);
      if (!currentId.startsWith("temp_")) {
        emitConversationActivity({ conversationId: currentId });
      }
    }
  }

  function replaceMessageList(id: string, list: any[]) {
    const userEdit = getUserEdit();
    const previousConversationId = currentConversationIdRef.current;
    if (previousConversationId && previousConversationId !== id) {
      userEdit?.persistCurrentUserMessageEditDraft(previousConversationId);
      userEdit?.resetEditState();
    }

    if (previousConversationId && previousConversationId !== id) {
      const previousRecovery =
        streamRecoveryRegistryRef.current.get(previousConversationId);
      if (previousRecovery?.status === "resuming") {
        clearStreamRecovery(previousConversationId);
      }
      if (saveTimerRef.current) {
        clearTimeout(saveTimerRef.current);
        saveTimerRef.current = null;
      }

      if (streamManager.hasActiveStream(previousConversationId)) {
        conversationMessagesCache.current.set(
          previousConversationId,
          messageListRef.current,
        );
        streamManager.saveMessageList(
          previousConversationId,
          messageListRef.current,
        );
        disconnectConversationStream(previousConversationId);
      }

      if (!concurrentStream) {
        streamManager.setActiveConversation(null);
      }
    }

    currentConversationIdRef.current = id;
    pendingClientConversationIdRef.current = "";
    const selectedRecovery = streamRecoveryRegistryRef.current.get(id);
    setStreamRecovery(
      selectedRecovery
        ? {
            conversationId: id,
            status: selectedRecovery.status,
            attempt: selectedRecovery.attempt,
            maxAttempts: STREAM_RECOVERY_MAX_ATTEMPTS,
          }
        : idleStreamRecoveryState(id),
    );

    if (!concurrentStream) {
      streamManager.setActiveConversation(id || null);
    }
    if (id) {
      // `list` was just loaded from the server and is authoritative. Reusing a
      // cached list here makes A -> B -> A navigation display the previous pane.
      conversationMessagesCache.current.set(id, list);
      streamManager.saveMessageList(id, list);
      messageListRef.current = list;
      setMessageList(list);
    } else {
      messageListRef.current = list;
      setMessageList(list);
    }
    closeSSE();

    onConversationIdChange?.(id);

    if (id) {
      userEdit?.restoreUserMessageEditDraft(id, messageListRef.current);
    }

    scroll.scrollToEndImmediately();
  }

  function createNewChat() {
    chatInputRef.current?.clearFiles();
    setFileList([]);
    clearCiteMessages();
    clearStorePendingMessage();

    const previousConversationId = currentConversationIdRef.current;
    if (previousConversationId) {
      if (saveTimerRef.current) {
        clearTimeout(saveTimerRef.current);
        saveTimerRef.current = null;
      }

      if (streamManager.hasActiveStream(previousConversationId)) {
        conversationMessagesCache.current.set(
          previousConversationId,
          messageListRef.current,
        );
        streamManager.saveMessageList(
          previousConversationId,
          messageListRef.current,
        );

        disconnectConversationStream(previousConversationId);
      }

      if (!concurrentStream) {
        streamManager.setActiveConversation(null);
      }
    }

    currentConversationIdRef.current = "";
    pendingClientConversationIdRef.current = "";
    streamRecoveryRegistryRef.current.clearAll();
    setStreamRecovery(idleStreamRecoveryState());
    setMessageList([]);
    messageListRef.current = [];
    getUserEdit()?.resetEditState();
    setLoading(false);
    setIsStreaming(false);
    closeSSE();
    onConversationIdChange?.("");
    setIsChatContent(false);
  }

  function stopGeneration() {
    const conversationId = currentConversationIdRef.current;

    if (conversationId) {
      ChatServiceApi()
        .conversationServiceStopChatGeneration({
          stopChatGenerationRequest: { conversation_id: conversationId },
        })
        .catch((err) =>
          console.error("Error calling stopChatGeneration:", err),
        );
    }

    // The stop request is only a control signal. Keep the business stream open
    // until Core emits the authoritative cancelled run_finished event.
  }

  async function regenerate() {
    if (!canChat) {
      if (disabledReason) {
        message.warning(disabledReason);
      }
      return;
    }
    if (
      activeStreamRef.current ||
      loading ||
      runtimeWaitInProgressRef.current ||
      regenerateInProgressRef.current ||
      isModelSelectionSaving?.()
    ) {
      return;
    }
    const userMessage = messageListRef.current.findLast(
      (item: any) => item.role === RoleTypes.USER,
    );
    const regenerationInputs = getRegenerationInputs(userMessage);
    if (regenerationInputs.length < 1) {
      message.error(t("chat.regenerateInputMissing"));
      return;
    }

    regenerateInProgressRef.current = true;

    const currentId = currentConversationIdRef.current;
    const previousMessageList = messageListRef.current;
    if (currentId) {
      clearStreamRecovery(currentId);
      streamManager.closeAndCleanup(currentId);
      conversationMessagesCache.current.delete(currentId);
    }

    const assistantMessage = {
      role: RoleTypes.ASSISTANT,
      delta: "",
      reasoning_content: "",
      finish_reason:
        ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
      answers: [],
      sources: [],
      history_id: undefined,
      id: undefined,
      feed_back: undefined,
      selected_answer_index: undefined,
      answer_preference: undefined,
    };
    const newList = [...messageListRef.current];
    const previousAssistant = newList[newList.length - 1];
    const preservesFailedAttempt =
      previousAssistant?.role === RoleTypes.ASSISTANT &&
      ["failed", "interrupted"].includes(previousAssistant.run_status);
    if (preservesFailedAttempt) {
      const originalHistoryId = previousAssistant.history_id;
      const archivedAttemptId = `${originalHistoryId || "pending"}:failed:${
        previousAssistant.run_id || Date.now()
      }`;
      newList[newList.length - 1] = {
        ...previousAssistant,
        id: archivedAttemptId,
        history_id: archivedAttemptId,
        original_history_id: originalHistoryId,
        archived_failure: true,
      };
      newList.push({
        ...assistantMessage,
        history_id: originalHistoryId,
      });
    } else {
      newList[newList.length - 1] = assistantMessage;
    }
    messageListRef.current = newList;
    setMessageList(newList);

    if (currentId) {
      conversationMessagesCache.current.set(currentId, newList);
      streamManager.saveMessageList(currentId, newList);
    }

    scroll.isMouseScrollingRef.current = true;
    try {
      const opened = await openSSE(
        regenerationInputs,
        ChatConversationsRequestActionEnum.ChatActionRegeneration,
      );
      if (!opened) {
        messageListRef.current = previousMessageList;
        setMessageList(previousMessageList);
        if (currentId) {
          conversationMessagesCache.current.set(
            currentId,
            previousMessageList,
          );
          streamManager.saveMessageList(currentId, previousMessageList);
        }
      }
    } finally {
      regenerateInProgressRef.current = false;
    }
  }

  async function retryStreamRecovery() {
    const conversationId = currentConversationIdRef.current;
    if (!conversationId || conversationId.startsWith("temp_")) {
      return;
    }

    streamRecoveryRegistryRef.current.clear(conversationId);
    const entry = streamRecoveryRegistryRef.current.ensure(conversationId);
    entry.status = "resuming";
    updateVisibleRecovery(conversationId, entry, 1);

    const result = await reconcileStreamRecovery(conversationId);
    if (result === "terminal") {
      return;
    }
    if (result === "stopped") {
      markStreamRecoveryFailed(conversationId);
      return;
    }
    scheduleStreamRecovery(conversationId);
  }

  return {
    messageList,
    setMessageList,
    loading,
    isStreaming,
    runtimeWaiting,
    runtimeWaitingOperation,
    streamRecovery,
    content,
    setContent,
    activeStreamRef,
    messageListRef,
    currentConversationIdRef,
    conversationMessagesCache,
    sendMessage,
    replaceMessageList,
    createNewChat,
    stopGeneration,
    regenerate,
    retryStreamRecovery,
    updateAssistantMessage,
    openSSE,
    openResumeSSE,
    appendAutoAdvanceTurn,
    ensureAutoAdvanceUserTurn,
    disconnectConversationStream,
    scroll,
  };
}
