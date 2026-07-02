import { useEffect, useRef, useState, type RefObject } from "react";
import { message } from "antd";
import {
  ChatConversationsRequestActionEnum,
  ChatConversationsResponseFinishReasonEnum,
} from "@/api/generated/chatbot-client";
import { allowedImageTypes } from "../../ImageUpload";
import type { ChatFileList, ChatInputImperativeProps, SendMessageParams } from "../../ChatInput";
import { RoleTypes } from "@/modules/chat/constants/common";
import {
  CHAT_AUTO_ADVANCE_EVENT,
  CHAT_RESUME_CONVERSATION_KEY,
  type ChatAutoAdvanceDetail,
} from "@/modules/chat/constants/chat";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import { streamManager } from "@/modules/chat/utils/StreamManager";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import UIUtils from "@/modules/chat/utils/ui";
import { emitConversationActivity } from "@/modules/chat/utils/conversationActivity";
import {
  buildChatMessageListFromHistory,
  getRegenerationInputs,
  mergeChatMessageLists,
} from "@/modules/chat/utils/message";
import { splitThinkingContent } from "@/modules/chat/utils/thinking";
import {
  buildCitedMessageText,
  MAX_CITE_MESSAGE_COUNT,
} from "../utils/citeMessage";
import { getFileUrls } from "../utils/fileInputs";
import type { ChatContainerProps } from "../types";
import type { useUserMessageEdit } from "./useUserMessageEdit";
import { useChatScroll } from "./useChatScroll";

type UserEditApi = ReturnType<typeof useUserMessageEdit>;

interface UseChatConversationOptions {
  canChat: boolean;
  disabledReason?: string;
  onOpenSSE: ChatContainerProps["onOpenSSE"];
  onOpenResumeSSE?: ChatContainerProps["onOpenResumeSSE"];
  onConversationIdChange?: ChatContainerProps["onConversationIdChange"];
  parseErrorData: ChatContainerProps["parseErrorData"];
  setIsChatContent: ChatContainerProps["setIsChatContent"];
  clearStorePendingMessage: () => void;
  clearCiteMessages: () => void;
  chatInputRef: RefObject<ChatInputImperativeProps>;
  thinkingCollapseMap: Map<string, boolean>;
  getUserEdit: () => UserEditApi | undefined;
  t: (key: string) => string;
}

export function useChatConversation({
  canChat,
  disabledReason,
  onOpenSSE,
  onOpenResumeSSE,
  onConversationIdChange,
  parseErrorData,
  setIsChatContent,
  clearStorePendingMessage,
  clearCiteMessages,
  chatInputRef,
  thinkingCollapseMap,
  getUserEdit,
  t,
}: UseChatConversationOptions) {
  const sseRef = useRef<any>(null);
  const activeStreamRef = useRef(false);
  const fileRef = useRef<any>(null);
  const currentConversationIdRef = useRef<string>("");
  const messageListRef = useRef<any[]>([]);
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const conversationMessagesCache = useRef<Map<string, any[]>>(new Map());
  const tempIdToRealIdRef = useRef<Map<string, string>>(new Map());

  const [messageList, setMessageList] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [content, setContent] = useState("");
  const [fileList, setFileList] = useState<ChatFileList[]>([]);
  const [isStreaming, setIsStreaming] = useState(false);

  const scroll = useChatScroll({
    chatInputRef,
    messageListLength: messageList.length,
    thinkingCollapseMap,
  });

  useEffect(() => {
    return () => {
      if (saveTimerRef.current) {
        clearTimeout(saveTimerRef.current);
        const currentId = currentConversationIdRef.current;
        if (currentId && streamManager.hasActiveStream(currentId)) {
          streamManager.saveMessageList(currentId, messageListRef.current);
        }
      }

      streamManager.cleanupFinishedStreams();
      conversationMessagesCache.current.clear();

      if (currentConversationIdRef.current) {
        streamManager.setActiveConversation(null);
      }
    };
  }, []);

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

  function updateAssistantMessage(data: any, id?: string, index?: number) {
    setMessageList((list) => {
      const newList = [...list];
      const targetIndex =
        index !== undefined
          ? index
          : id
            ? newList.findIndex(
                (msg) => msg.id === id || msg.history_id === id,
              )
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

    const errMessage = parseErrorData(e.data || "");

    if (errorConversationId === currentConversationIdRef.current) {
      updateAssistantMessage({
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonUnknown,
        errMessage,
      });
      setIsStreaming(false);
      closeSSE();
    }

    if (errorConversationId) {
      streamManager.closeAndCleanup(errorConversationId);
      conversationMessagesCache.current.delete(errorConversationId);
    }
    sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
  }

  function onTimeout(e: any) {
    if (e.type !== "timeout") {
      return;
    }
    onError({ type: "error", data: e.data });
  }

  function onMessage(e: any) {
    const result = UIUtils.jsonParser(e.data)?.result;
    if (!result) {
      return;
    }

    if (result.task_created && result.task_created.task_id) {
      const convId =
        result.conversation_id || currentConversationIdRef.current || "";
      const tc = result.task_created;
      const taskStore = useTaskCenterStore.getState();
      taskStore.upsertTask(convId, {
        task_id: tc.task_id,
        title: tc.title,
        agent_type: tc.agent_type,
        mode: tc.mode,
        status: tc.status || "pending",
      });
      taskStore.subscribeTask(convId, tc.task_id);
      if (tc.agent_type === "plugin_step" && tc.plugin_session_id) {
        import("@/modules/chat/store/pluginPanel").then(({ usePluginStore }) => {
          usePluginStore.getState().loadActiveSession(convId);
        });
      }
    }

    const messageConversationId = result.conversation_id || "";
    const currentConversationIdAtStart = currentConversationIdRef.current;
    const isUsingTempId = currentConversationIdAtStart.startsWith("temp_");

    let isActiveConversation = false;
    if (messageConversationId) {
      if (isUsingTempId) {
        const stream = streamManager.getStream(messageConversationId);
        isActiveConversation = !stream;
      } else {
        isActiveConversation =
          messageConversationId === currentConversationIdAtStart;
      }
    } else {
      isActiveConversation = currentConversationIdAtStart === "";
    }

    if (messageConversationId && !isActiveConversation) {
      for (const [tempKey] of tempIdToRealIdRef.current) {
        if (
          tempKey.startsWith("temp_") &&
          conversationMessagesCache.current.has(tempKey) &&
          !conversationMessagesCache.current.has(messageConversationId)
        ) {
          const cachedList = conversationMessagesCache.current.get(tempKey)!;
          conversationMessagesCache.current.set(messageConversationId, cachedList);
          conversationMessagesCache.current.delete(tempKey);
          streamManager.saveMessageList(messageConversationId, cachedList);
          tempIdToRealIdRef.current.delete(tempKey);
          break;
        }
      }
    }

    const isFirstTimeReceivingId =
      result.conversation_id &&
      result.conversation_id !== currentConversationIdRef.current &&
      isActiveConversation;

    if (isFirstTimeReceivingId) {
      onConversationIdChange?.(result.conversation_id);
      sessionStorage.setItem(
        CHAT_RESUME_CONVERSATION_KEY,
        result.conversation_id,
      );

      const previousConversationId = currentConversationIdRef.current;
      const isPreviousTempId = previousConversationId.startsWith("temp_");

      if (isPreviousTempId) {
        const currentList = messageListRef.current;
        conversationMessagesCache.current.set(
          previousConversationId,
          currentList,
        );

        currentConversationIdRef.current = result.conversation_id;
        streamManager.setActiveConversation(result.conversation_id);

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

    if (
      isActiveConversation &&
      result.finish_reason ===
        ChatConversationsResponseFinishReasonEnum.FinishReasonStop
    ) {
      scroll.isMouseScrollingRef.current = true;
    }

    if (
      result.finish_reason !==
      ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
    ) {
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
      sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
    }

    const updateMessageListInternal = (list: any[]) => {
      const newList = [...list];
      let assistantMessage =
        newList.length > 0 ? newList[newList.length - 1] : null;

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
      }

      const previousRawDelta =
        assistantMessage.raw_delta || assistantMessage.delta || "";
      const mergedRawDelta = previousRawDelta + (result.delta || "");
      const splitResult = splitThinkingContent(
        mergedRawDelta,
        assistantMessage.reasoning_content || "",
      );

      assistantMessage = {
        ...assistantMessage,
        ...result,
        id: result.messageId,
        raw_delta: mergedRawDelta,
        delta: splitResult.content,
        reasoning_content: splitResult.reasoning_content,
        sources:
          result.sources && result.sources.length > 0
            ? result.sources
            : assistantMessage.sources,
      };

      newList[newList.length - 1] = assistantMessage;
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
    } else if (messageConversationId) {
      if (streamManager.hasActiveStream(messageConversationId)) {
        let savedList = conversationMessagesCache.current.get(
          messageConversationId,
        );
        if (!savedList) {
          const streamState = streamManager.getStreamState(messageConversationId);
          savedList = streamState?.messageList || [];
        }

        const newList = updateMessageListInternal(savedList);
        conversationMessagesCache.current.set(messageConversationId, newList);
        streamManager.saveMessageList(messageConversationId, newList);
      }
    }
  }

  const openSSE = async (
    input: any[],
    action: ChatConversationsRequestActionEnum,
    extras?: Record<string, unknown>,
  ) => {
    activeStreamRef.current = true;
    setLoading(true);
    setIsStreaming(true);

    let conversationId = currentConversationIdRef.current;
    if (!conversationId) {
      conversationId = `temp_${Date.now()}_${Math.random().toString(36).substring(2, 15)}`;
      currentConversationIdRef.current = conversationId;
      tempIdToRealIdRef.current.set(conversationId, conversationId);
    } else {
      sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, conversationId);
    }

    const callbacks: Record<string, (e: CustomEvent) => void> = {
      message: (e) => onMessage(e),
      error: (e) => onError(e),
      timeout: (e) => onTimeout(e),
    };

    const sseOrPromise = onOpenSSE(input, action, {}, extras);
    const sse = sseOrPromise instanceof Promise ? await sseOrPromise : sseOrPromise;
    sseRef.current = sse;

    streamManager.registerStream(conversationId, sse, callbacks);
    streamManager.setActiveConversation(conversationId);

    const currentList = messageListRef.current;
    conversationMessagesCache.current.set(conversationId, currentList);
    streamManager.saveMessageList(conversationId, currentList);

    if (conversationId.startsWith("temp_")) {
      const tempId = conversationId;
      setTimeout(() => {
        ChatServiceApi()
          .conversationServiceListConversations({
            pageToken: "",
            pageSize: 5,
          })
          .then((res) => {
            const conversations = res?.data?.conversations ?? [];
            const latest = conversations[0];
            const realId = latest?.conversation_id;
            if (!realId) return;
            if (currentConversationIdRef.current !== tempId) return;
            sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, realId);
            onConversationIdChange?.(realId);
          })
          .catch(() => {});
      }, 400);
    }
  };

  async function syncGeneratingHistory(conversationId: string) {
    try {
      const statusRes = await ChatServiceApi().conversationServiceGetChatStatus({
        conversationId,
      });
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
      const cached = conversationMessagesCache.current.get(conversationId) ?? [];
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

  function openResumeSSE(conversationId: string) {
    if (!onOpenResumeSSE) {
      return;
    }
    if (streamManager.hasActiveStream(conversationId)) {
      if (currentConversationIdRef.current === conversationId) {
        activeStreamRef.current = true;
        setLoading(true);
        setIsStreaming(true);
        const callbacks: Record<string, (e: CustomEvent) => void> = {
          message: (e) => onMessage(e),
          error: (e) => onError(e),
          timeout: (e) => onTimeout(e),
        };
        streamManager.restoreStreamCallbacks(conversationId, callbacks);
        sseRef.current = streamManager.getStream(conversationId) ?? sseRef.current;
      }
      return;
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
    const sse = onOpenResumeSSE(conversationId, {});
    sseRef.current = sse;

    streamManager.registerStream(conversationId, sse, callbacks);
    streamManager.setActiveConversation(conversationId);
    const currentList = messageListRef.current;
    conversationMessagesCache.current.set(conversationId, currentList);
    streamManager.saveMessageList(conversationId, currentList);
    sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, conversationId);
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
      finish_reason:
        ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
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

  function appendAutoAdvanceTurn(conversationId: string, driverMessage: string) {
    ensureAutoAdvanceUserTurn(conversationId, driverMessage);
    openResumeSSE(conversationId);
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
        void syncGeneratingHistory(detail.conversationId).finally(() => {
          openResumeSSE(detail.conversationId);
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
    if (activeStreamRef.current || loading || !normalizedText) {
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

    if (params?.fileList) {
      setFileList(params.fileList);
    }
    if (params?.fileListRef) {
      fileRef.current = params.fileListRef.current;
    }

    const tempGroup =
      Object.groupBy(params?.fileList ?? [], (item) => {
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
      role: RoleTypes.USER,
      images: tempGroup?.image,
      files: tempGroup?.file,
      fileList,
      inputs,
      finish_reason:
        ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      create_time,
      model_mode: "value_engineering",
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
    const newMessageList = [...messageList, userMessage, assistantMessage];
    messageListRef.current = newMessageList;
    setMessageList(newMessageList);

    scroll.isMouseScrollingRef.current = true;
    scroll.scrollToEnd();
    openSSE(inputs, ChatConversationsRequestActionEnum.ChatActionNext, {
      ...(params.run_in_background ? { run_in_background: true } : {}),
    });

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
      }

      streamManager.setActiveConversation(null);
    }

    currentConversationIdRef.current = id;

    if (id) {
      import("@/modules/chat/store/pluginPanel").then(({ usePluginStore }) => {
        usePluginStore.getState().loadActiveSession(id);
      });
    }

    streamManager.setActiveConversation(id || null);

    if (id && streamManager.hasActiveStream(id)) {
      activeStreamRef.current = true;
      const callbacks: Record<string, (event: CustomEvent) => void> = {
        message: (event) => onMessage(event),
        error: (event) => onError(event),
        timeout: (event) => onTimeout(event),
      };
      streamManager.restoreStreamCallbacks(id, callbacks);

      const streamState = streamManager.getStreamState(id);
      if (streamState) {
        const cachedList = conversationMessagesCache.current.get(id);
        const baseList = mergeChatMessageLists(list, cachedList);

        if (baseList.length > 0) {
          const savedList = [...baseList];
          const lastIndex = savedList.length - 1;
          if (savedList[lastIndex]?.role === RoleTypes.ASSISTANT) {
            savedList[lastIndex] = {
              ...savedList[lastIndex],
              sources: streamState.sources || savedList[lastIndex].sources,
              finish_reason: streamState.finish_reason,
              id: streamState.messageId || savedList[lastIndex].id,
              history_id:
                streamState.history_id || savedList[lastIndex].history_id,
            };
          }
          messageListRef.current = savedList;
          setMessageList(savedList);
          conversationMessagesCache.current.set(id, savedList);
          streamManager.saveMessageList(id, savedList);
          setLoading(true);
          if (
            streamState.finish_reason ===
            ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
          ) {
            setIsStreaming(true);
          }
        } else if (
          streamState.messageList &&
          streamState.messageList.length > 0
        ) {
          const savedList = [...streamState.messageList];
          const lastIndex = savedList.length - 1;
          if (savedList[lastIndex]?.role === RoleTypes.ASSISTANT) {
            savedList[lastIndex] = {
              ...savedList[lastIndex],
              sources: streamState.sources || savedList[lastIndex].sources,
              finish_reason: streamState.finish_reason,
              id: streamState.messageId || savedList[lastIndex].id,
              history_id:
                streamState.history_id || savedList[lastIndex].history_id,
            };
          }
          messageListRef.current = savedList;
          setMessageList(savedList);
          setLoading(true);
          if (
            streamState.finish_reason ===
            ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
          ) {
            setIsStreaming(true);
          }
        } else {
          messageListRef.current = list;
          setMessageList(list);
          if (
            streamState.finish_reason ===
            ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
          ) {
            setLoading(true);
            setIsStreaming(true);
          }
        }
      } else {
        messageListRef.current = list;
        setMessageList(list);
      }
    } else {
      if (id) {
        const cachedList = conversationMessagesCache.current.get(id);
        if (cachedList && cachedList.length > 0) {
          messageListRef.current = cachedList;
          setMessageList(cachedList);
        } else {
          messageListRef.current = list;
          setMessageList(list);
        }
      } else {
        messageListRef.current = list;
        setMessageList(list);
      }
      closeSSE();
    }

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

        if (sseRef.current) {
          try {
            sseRef.current.close();
          } catch (error) {
            console.error("Error closing SSE when creating new chat:", error);
          }
        }

        streamManager.closeAndCleanup(previousConversationId);
      }

      streamManager.setActiveConversation(null);
    }

    currentConversationIdRef.current = "";
    setMessageList([]);
    messageListRef.current = [];
    getUserEdit()?.resetEditState();
    setLoading(false);
    setIsStreaming(false);
    closeSSE();
    sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
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

    if (sseRef.current) {
      try {
        sseRef.current.close();
      } catch (error) {
        console.error("Error closing SSE:", error);
      }
    }

    updateAssistantMessage({
      finish_reason:
        ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
    });

    setIsStreaming(false);
    closeSSE();

    if (conversationId) {
      streamManager.closeAndCleanup(conversationId);
      conversationMessagesCache.current.delete(conversationId);
    }
    sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
  }

  function regenerate() {
    if (!canChat) {
      if (disabledReason) {
        message.warning(disabledReason);
      }
      return;
    }
    if (loading) {
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

    const currentId = currentConversationIdRef.current;
    if (currentId) {
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
    newList[newList.length - 1] = assistantMessage;
    messageListRef.current = newList;
    setMessageList(newList);

    if (currentId) {
      conversationMessagesCache.current.set(currentId, newList);
      streamManager.saveMessageList(currentId, newList);
    }

    scroll.isMouseScrollingRef.current = true;
    openSSE(
      regenerationInputs,
      ChatConversationsRequestActionEnum.ChatActionRegeneration,
    );
  }

  return {
    messageList,
    setMessageList,
    loading,
    isStreaming,
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
    updateAssistantMessage,
    openSSE,
    openResumeSSE,
    appendAutoAdvanceTurn,
    ensureAutoAdvanceUserTurn,
    scroll,
  };
}
