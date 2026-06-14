import {
  useRef,
  useState,
  useEffect,
  forwardRef,
  useImperativeHandle,
  useCallback,
  ReactNode,
} from "react";
import { Spin, Flex, Tooltip, message } from "antd";
import {
  CommentOutlined,
  DoubleRightOutlined,
  DownOutlined,
  UpOutlined,
} from "@ant-design/icons";
import {
  ChatConversationsRequestActionEnum,
  ChatConversationsResponseFinishReasonEnum,
  Query,
  Source,
} from "@/api/generated/chatbot-client";
import { RcFile } from "antd/es/upload";

import UIUtils from "@/modules/chat/utils/ui";
import { RoleTypes } from "@/modules/chat/constants/common";
import "./index.scss";
import MarkdownViewer from "@/modules/chat/components/MarkdownViewer";
import ChatImages, { ChatImage } from "../ChatImages";
import ChatFiles from "../ChatFiles";
import MessageList from "./components/MessageList";
import ChatInput, {
  ChatFileList,
  SendMessageParams,
  ChatInputImperativeProps,
} from "../ChatInput";
import { ChatConfig } from "../ChatConfigs";
import { allowedImageTypes } from "../ImageUpload";
import { streamManager } from "@/modules/chat/utils/StreamManager";
import { useModelSelectionStore } from "@/modules/chat/store/modelSelection";
import type { PreferenceType } from "../MultiAnswerDisplay";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import { useChatMessageStore } from "@/modules/chat/store/chatMessage";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import { CHAT_RESUME_CONVERSATION_KEY } from "@/modules/chat/constants/chat";
import { useTranslation } from "react-i18next";
import { getRegenerationInputs } from "@/modules/chat/utils/message";
import {
  splitThinkingContent,
  formatThinkingForDisplay,
} from "@/modules/chat/utils/thinking";

const ThinkIcon = new URL("../../assets/images/think.png", import.meta.url)
  .href;
const MAX_CITE_MESSAGE_COUNT = 3;

function buildCitedMessageText(text: string, citeMessages?: string[]) {
  const normalizedText = text.trim();
  const normalizedCiteMessages =
    citeMessages?.map((item) => item.trim()).filter(Boolean) ?? [];
  if (normalizedCiteMessages.length < 1) {
    return normalizedText;
  }
  const citedText = normalizedCiteMessages
    .map((citeMessage) => `<cite_message>${citeMessage}</cite_message>`)
    .join("\n");
  return `${citedText}\n${normalizedText}`;
}

function splitCiteMessages(citeMessage?: string) {
  return (citeMessage || "")
    .split(/\n{2,}/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function getCiteMessages(message?: { cite_message?: string; cite_messages?: string[] }) {
  if (Array.isArray(message?.cite_messages)) {
    return message.cite_messages.map((item) => item.trim()).filter(Boolean);
  }
  const textInput = (message as any)?.inputs?.find((input: any) => {
    const inputType = input?.input_type || "text";
    return inputType === "text" && typeof input?.text === "string";
  });
  const inputCites = Array.from(
    `${textInput?.text || ""}`.matchAll(/<cite_message>([\s\S]*?)<\/cite_message>/gi),
  )
    .map((match) => match[1]?.trim())
    .filter(Boolean);
  if (inputCites.length > 0) {
    return inputCites;
  }
  return splitCiteMessages(message?.cite_message);
}

export interface ChatImperativeProps {
  replaceMessageList: (id: string, data: any[]) => void;
  createNewChat: () => void;
  sendMessage: (params: SendMessageParams) => void;
  uploadFiles?: (files: File[]) => void;
  openResumeSSE?: (conversationId: string) => void;
}

interface Props {
  canChat?: boolean;
  initialCard?: ReactNode;
  sessionId?: string;
  onOpenSSE: (
    input: any[],
    action: ChatConversationsRequestActionEnum,
    callbacks: Record<string, (e: CustomEvent) => void>,
  ) => any; // Return new SSE.
  onOpenResumeSSE?: (
    conversationId: string,
    callbacks: Record<string, (e: CustomEvent) => void>,
  ) => any;
  onConversationIdChange?: (conversationId: string) => void;
  parseErrorData: (data: string) => string;
  setShowHistoryList?: (show: boolean) => void;
  showHistoryList?: boolean;
  showHistoryButton?: boolean;
  setIsChatContent: (isChatContent: boolean) => void;
  chatConfig?: ChatConfig;
  setChatConfig?: (chatConfig: ChatConfig) => void;
  setChatConfigFn: (chatConfig: ChatConfig) => void;
  knowledgeRefreshKey?: number | string;
  embeddingReady?: boolean | null;
  multimodalEmbeddingReady?: boolean | null;
  rerankReady?: boolean | null;
  disabledReason?: string;
  disabledDescription?: string;
  disabledAction?: ReactNode;
}

export interface ChatMessage {
  role?: string;
  delta?: string;
  raw_delta?: string;
  images?: {
    base64?: string;
    uid?: string;
  }[];
  files?: {
    name?: string;
    uid?: string;
  }[];
  finish_reason?: string;
  inputs?: Query[];
  reasoning_content?: string;
  history_id?: string;
  sources?: Source[];
  feed_back?: string;
  answers?: Array<{
    content: string;
    index: number;
    history_id?: string;
    raw_content?: string;
    reasoning_content?: string;
    sources?: Source[];
    thinking_duration_s?: string;
  }>;
  answer_index?: number;
  create_time?: string;
  is_resumed?: boolean;
  display_delta?: string;
  cite_message?: string;
  cite_messages?: string[];
}

const ChatContainerComponent = forwardRef<ChatImperativeProps, Props>(
  (props, ref) => {
    const { t } = useTranslation();
    const {
      canChat = true,
      initialCard,
      sessionId = "",
      onOpenSSE,
      onOpenResumeSSE,
      onConversationIdChange,
      parseErrorData,
      setShowHistoryList,
      showHistoryList,
      showHistoryButton = true,
      setIsChatContent,
      chatConfig,
      setChatConfig,
      setChatConfigFn,
      knowledgeRefreshKey,
      embeddingReady,
      multimodalEmbeddingReady,
      rerankReady,
      disabledReason,
      disabledDescription,
      disabledAction,
    } = props;
    const { getModelSelection, setModelSelection, resetForNewChat } =
      useModelSelectionStore();

    const handlePreferenceSelect = useCallback(
      (preference: PreferenceType, sessId?: string) => {
        const sid =
          sessId ?? sessionId ?? currentConversationIdRef.current ?? "";
        if (preference === "prefer_first") {
          setModelSelection(sid, "value_engineering");
          message.success("后续回答将为 LazyMind 大模型");
        } else if (preference === "prefer_second") {
          setModelSelection(sid, "deepseek");
          message.success("后续回答将为DeepSeek");
        } else if (preference === "similar") {
          message.success("感谢您的反馈，后续回答将仍为双模型");
        } else if (preference === "neither") {
          message.success("抱歉您的体验不佳，反馈已收到，后续回答将仍为双模型");
        }
      },
      [sessionId, setModelSelection],
    );
    const { clearPendingMessage: clearStorePendingMessage } =
      useChatMessageStore();
    const isMouseScrollingRef = useRef(false);
    const sseRef = useRef<any>(null);
    const activeStreamRef = useRef(false);
    const fileRef = useRef<any>(null);
    const chatContentRef = useRef<HTMLDivElement>(null);
    const currentConversationIdRef = useRef<string>("");
    const messageListRef = useRef<any[]>([]);
    const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const conversationMessagesCache = useRef<Map<string, any[]>>(new Map());

    const [messageList, setMessageList] = useState<any[]>([]);
    const [loading, setLoading] = useState(false);
    const [content, setContent] = useState("");
    const [editingUserMessageIndex, setEditingUserMessageIndex] = useState<number | null>(null);
    const [editingUserMessageText, setEditingUserMessageText] = useState("");
    const [editingUserMessageCites, setEditingUserMessageCites] = useState<string[]>([]);
    const [thinkingCollapseMap, setThinkingCollapseMap] = useState<
      Map<string, boolean>
    >(new Map());
    const [fileList, setFileList] = useState<ChatFileList[]>([]);
    const [citeMessages, setCiteMessages] = useState<string[]>([]);
    const [showScrollButton, setShowScrollButton] = useState(false);
    const chatInputRef = useRef<ChatInputImperativeProps>(null);
    const [inputHeight, setInputHeight] = useState(120);
    const [IS_STREAMING, setIS_STREAMING] = useState(false);

    useImperativeHandle(ref, () => ({
      replaceMessageList,
      createNewChat,
      sendMessage,
      uploadFiles: (files: File[]) => {
        chatInputRef.current?.uploadFiles(files);
      },
      openResumeSSE: onOpenResumeSSE ? openResumeSSE : undefined,
    }));

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

    useEffect(() => {
      if (editingUserMessageIndex === null) {
        return;
      }
      if (
        editingUserMessageIndex < 0 ||
        editingUserMessageIndex >= messageList.length ||
        messageList[editingUserMessageIndex]?.role !== RoleTypes.USER
      ) {
        setEditingUserMessageIndex(null);
        setEditingUserMessageText("");
        setEditingUserMessageCites([]);
      }
    }, [editingUserMessageIndex, messageList]);

    function getFileUrls(
      files: (RcFile & { uri: string })[] | undefined,
      images?: ChatImage[],
    ) {
      if (!files) {
        return [];
      }

      return files?.map((file) => {
        return {
          uri: file.uri,
          base64: images
            ? images.find((image) => image.uid === file.uid)?.base64
            : "",
        };
      });
    }

    function clearMultiData() {
      setFileList([]);
      fileRef.current?.clear();
    }

    function sendMessage(params: SendMessageParams) {
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
          const suffix = item.name
            .substring(item.name.lastIndexOf("."))
            .toLowerCase();
          return allowedImageTypes.includes(suffix) ? "image" : "file";
        }) ?? {};
      const tempFileGroup =
        Object.groupBy(params?.files ?? [], (item) => {
          const suffix = item.name
            .substring(item.name.lastIndexOf("."))
            .toLowerCase();
          return allowedImageTypes.includes(suffix) ? "image" : "file";
        }) ?? {};

      const inputs = [
        { input_type: "text", text: textWithCitation },
        ...getFileUrls(tempFileGroup?.image, tempGroup?.image).map((image) => {
          return {
            input_type: "image",
            uri: image.uri || "",
            input_base64: image.base64 || "",
          };
        }),
        ...getFileUrls(tempFileGroup?.file, tempGroup?.file).map((file) => {
          return { input_type: "file", uri: file.uri || "" };
        }),
      ];

      if (clearInput) {
        setContent("");
        clearMultiData();
      }
      const currentModelSelection = getModelSelection(
        currentConversationIdRef.current || sessionId,
      );

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
        model_mode: currentModelSelection,
      };
      const assistantMessage = {
        role: RoleTypes.ASSISTANT,
        delta: "",
        reasoning_content: "",
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
        answers: [],
        sources: [],
        model_mode: currentModelSelection,
      };
      const newMessageList = [...messageList, userMessage, assistantMessage];
      messageListRef.current = newMessageList;
      setMessageList(newMessageList);

      isMouseScrollingRef.current = true;
      scrollToEnd();
      openSSE(inputs, ChatConversationsRequestActionEnum.ChatActionNext);

      const currentId = currentConversationIdRef.current;
      if (currentId) {
        conversationMessagesCache.current.set(currentId, newMessageList);
        streamManager.saveMessageList(currentId, newMessageList);
      }
    }

    const openSSE = (
      input: any[],
      action: ChatConversationsRequestActionEnum,
    ) => {
      activeStreamRef.current = true;
      setLoading(true);
      setIS_STREAMING(true);

      let conversationId = currentConversationIdRef.current;
      if (!conversationId) {
        conversationId = `temp_${Date.now()}_${Math.random().toString(36).substring(2, 15)}`;
        currentConversationIdRef.current = conversationId;
      } else {
        sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, conversationId);
      }
      const callbacks: Record<string, (e: CustomEvent) => void> = {
        message: (e) => onMessage(e),
        error: (e) => onError(e),
        timeout: (e) => onTimeout(e),
      };

      const sse = onOpenSSE(input, action, {});
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

    function openResumeSSE(conversationId: string) {
      if (!onOpenResumeSSE) {
        return;
      }
      activeStreamRef.current = true;
      setLoading(true);
      setIS_STREAMING(true);
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

    function closeSSE() {
      sseRef.current = null;
      activeStreamRef.current = false;
      setLoading(false);
      setIS_STREAMING(false);
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

      const isFirstTimeReceivingId =
        result.conversation_id &&
        result.conversation_id !== currentConversationIdRef.current &&
        isActiveConversation;

      if (isFirstTimeReceivingId) {
        if (onConversationIdChange) {
          onConversationIdChange(result.conversation_id);
        }

        sessionStorage.setItem(
          CHAT_RESUME_CONVERSATION_KEY,
          result.conversation_id,
        );

        const previousConversationId = currentConversationIdRef.current;
        const isPreviousTempId = previousConversationId.startsWith("temp_");

        const newChatModelSelection = getModelSelection("");
        setModelSelection(result.conversation_id, newChatModelSelection);

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

            const streamCallbacks: Record<
              string,
              (event: CustomEvent) => void
            > = {
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
      }

      if (
        isActiveConversation &&
        result.finish_reason ===
          ChatConversationsResponseFinishReasonEnum.FinishReasonStop
      ) {
        isMouseScrollingRef.current = true;
      }

      if (
        result.finish_reason !==
        ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
      ) {
        if (isActiveConversation) {
          setIS_STREAMING(false);
          closeSSE();
        }

        const cleanupConversationId =
          messageConversationId || currentConversationIdAtStart;
        if (cleanupConversationId) {
          streamManager.closeAndCleanup(cleanupConversationId);
          conversationMessagesCache.current.delete(cleanupConversationId);
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
          if (isLastAssistantCompleted) {
            newList.push({
              role: RoleTypes.USER,
              delta: "",
              finish_reason:
                ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
              inputs: [],
              is_resumed: true,
            });
          }

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

        const convModelSelection =
          assistantMessage?.model_mode ||
          getModelSelection(
            messageConversationId || currentConversationIdAtStart,
          );
        const isMultiAnswerMode =
          convModelSelection === "both" && result.history_id;

        if (isMultiAnswerMode) {
          if (!assistantMessage.answers) {
            assistantMessage.answers = [];
          }

          let targetAnswer = assistantMessage.answers.find(
            (ans: any) => ans.history_id === result.history_id,
          );

          if (!targetAnswer) {
            const answerIndex = assistantMessage.answers.length;
            targetAnswer = {
              content: "",
              index: answerIndex,
              history_id: result.history_id,
              raw_content: "",
              reasoning_content: "",
              sources: [],
            };
            assistantMessage.answers.push(targetAnswer);
          }

          targetAnswer.raw_content =
            (targetAnswer.raw_content || targetAnswer.content || "") +
            (result.delta || "");
          const answerSplitResult = splitThinkingContent(
            targetAnswer.raw_content,
            targetAnswer.reasoning_content || "",
          );
          targetAnswer.content = answerSplitResult.content;
          targetAnswer.reasoning_content = answerSplitResult.reasoning_content;

          if (result.sources && result.sources.length > 0) {
            targetAnswer.sources = result.sources;
          }

          if (result.thinking_duration_s) {
            targetAnswer.thinking_duration_s = result.thinking_duration_s;
          }

          assistantMessage = {
            ...assistantMessage,
            finish_reason:
              result.finish_reason || assistantMessage.finish_reason,
            conversation_id:
              result.conversation_id || assistantMessage.conversation_id,
            id: result.messageId || assistantMessage.id,
          };
        } else {
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
        }

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

        if (isMouseScrollingRef.current) {
          scrollToEnd();
        }
      } else {
        if (messageConversationId) {
          if (streamManager.hasActiveStream(messageConversationId)) {
            let savedList = conversationMessagesCache.current.get(
              messageConversationId,
            );
            if (!savedList) {
              const streamState = streamManager.getStreamState(
                messageConversationId,
              );
              savedList = streamState?.messageList || [];
            }

            const newList = updateMessageListInternal(savedList);

            conversationMessagesCache.current.set(
              messageConversationId,
              newList,
            );
            streamManager.saveMessageList(messageConversationId, newList);
          }
        }
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
      }

      const errMessage = parseErrorData(e.data || "");

      if (errorConversationId === currentConversationIdRef.current) {
        updateAssistantMessage({
          finish_reason:
            ChatConversationsResponseFinishReasonEnum.FinishReasonUnknown,
          errMessage,
        });
        setIS_STREAMING(false);
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
        return newList;
      });
      if (!id) {
        if (isMouseScrollingRef.current) {
          scrollToEnd();
        }
      }
    }

    function scrollToEnd() {
      if (!isMouseScrollingRef.current) {
        return;
      }
      scrollToEndImmediately();
    }

    function scrollToEndImmediately() {
      isMouseScrollingRef.current = true;
      setShowScrollButton(false);
      const scroll = () => {
        const container = chatContentRef.current;
        if (container) {
          container.scrollTop = container.scrollHeight;
        }
      };
      requestAnimationFrame(() => {
        scroll();
        requestAnimationFrame(scroll);
        window.setTimeout(scroll, 80);
      });
    }

    function replaceMessageList(id: string, list: any[]) {
      const previousConversationId = currentConversationIdRef.current;
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

          if (cachedList && cachedList.length > 0) {
            const savedList = [...cachedList];
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
              setIS_STREAMING(true);
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
              setIS_STREAMING(true);
            }
          } else {
            messageListRef.current = list;
            setMessageList(list);
            if (
              streamState.finish_reason ===
              ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
            ) {
              setLoading(true);
              setIS_STREAMING(true);
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

      if (onConversationIdChange) {
        onConversationIdChange(id);
      }

      scrollToEndImmediately();
    }

    function createNewChat() {
      chatInputRef.current?.clearFiles();
      setFileList([]);
      setCiteMessages([]);
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

      resetForNewChat();

      setMessageList([]);
      messageListRef.current = [];
      setEditingUserMessageIndex(null);
      setEditingUserMessageText("");
      setEditingUserMessageCites([]);
      setLoading(false);
      setIS_STREAMING(false);

      closeSSE();
      sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);

      if (onConversationIdChange) {
        onConversationIdChange("");
      }

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

      setIS_STREAMING(false);
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

      isMouseScrollingRef.current = true;
      openSSE(
        regenerationInputs,
        ChatConversationsRequestActionEnum.ChatActionRegeneration,
      );
    }

    async function handleCopyUserMessage(item: any) {
      const text = (item?.delta || "").trim();
      if (!text) {
        return;
      }
      try {
        if (navigator.clipboard?.writeText) {
          await navigator.clipboard.writeText(text);
          message.success(t("chat.copySuccess"));
          return;
        }
      } catch {
      }

      const textarea = document.createElement("textarea");
      textarea.value = text;
      textarea.setAttribute("readonly", "");
      textarea.style.position = "fixed";
      textarea.style.top = "0";
      textarea.style.left = "0";
      textarea.style.opacity = "0";
      document.body.appendChild(textarea);
      textarea.focus();
      textarea.select();
      try {
        const copied = document.execCommand("copy");
        if (copied) {
          message.success(t("chat.copySuccess"));
        } else {
          message.error(t("chat.copyFailedManual"));
        }
      } finally {
        document.body.removeChild(textarea);
      }
    }

    function handleStartEditUserMessage(item: any, index: number) {
      if (!canChat) {
        if (disabledReason) {
          message.warning(disabledReason);
        }
        return;
      }
      if (loading || activeStreamRef.current) {
        return;
      }
      setEditingUserMessageIndex(index);
      setEditingUserMessageText(item?.delta || "");
      setEditingUserMessageCites(getCiteMessages(item));
    }

    function handleCancelEditUserMessage() {
      setEditingUserMessageIndex(null);
      setEditingUserMessageText("");
      setEditingUserMessageCites([]);
    }

    function handleRemoveEditingUserMessageCite(index: number) {
      setEditingUserMessageCites((prev) =>
        prev.filter((_, itemIndex) => itemIndex !== index),
      );
    }

    function handleResendEditedUserMessage(index: number, value: string) {
      if (!canChat) {
        if (disabledReason) {
          message.warning(disabledReason);
        }
        return;
      }
      if (loading || activeStreamRef.current) {
        return;
      }
      const normalizedText = value.trim();
      if (!normalizedText) {
        return;
      }

      const oldUserMessage = messageListRef.current[index];
      if (!oldUserMessage || oldUserMessage.role !== RoleTypes.USER) {
        return;
      }

      const oldInputs = Array.isArray(oldUserMessage.inputs) ? oldUserMessage.inputs : [];
      const textWithCitation = buildCitedMessageText(normalizedText, editingUserMessageCites);
      const rebuiltInputs = oldInputs
        .filter((input: any) => (input?.input_type || "text") !== "text")
        .map((input: any) => ({ ...input }));
      rebuiltInputs.unshift({ input_type: "text", text: textWithCitation });

      const newUserMessage = {
        ...oldUserMessage,
        delta: normalizedText,
        display_delta: normalizedText,
        cite_message: editingUserMessageCites.join("\n\n"),
        cite_messages: editingUserMessageCites,
        inputs: rebuiltInputs,
      };

      const currentModelSelection = getModelSelection(
        currentConversationIdRef.current || sessionId,
      );
      const assistantMessage = {
        role: RoleTypes.ASSISTANT,
        delta: "",
        reasoning_content: "",
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
        answers: [],
        sources: [],
        model_mode: currentModelSelection,
      };

      const truncated = messageListRef.current.slice(0, index);
      const newList = [...truncated, newUserMessage, assistantMessage];
      messageListRef.current = newList;
      setMessageList(newList);
      setEditingUserMessageIndex(null);
      setEditingUserMessageText("");
      setEditingUserMessageCites([]);

      const currentId = currentConversationIdRef.current;
      if (currentId) {
        conversationMessagesCache.current.set(currentId, newList);
        streamManager.saveMessageList(currentId, newList);
      }

      isMouseScrollingRef.current = true;
      scrollToEnd();
      openSSE(rebuiltInputs, ChatConversationsRequestActionEnum.ChatActionRegeneration);
    }

    function renderText(item: any, uniqueKey?: string) {
      const thinkingKey = uniqueKey || item.history_id || item.id || "default";
      const isCollapsed = thinkingCollapseMap.get(thinkingKey) || false;
      const citeMessageList =
        item.role === RoleTypes.USER ? getCiteMessages(item) : [];

      const toggleCollapse = () => {
        setThinkingCollapseMap((prev) => {
          const newMap = new Map(prev);
          newMap.set(thinkingKey, !isCollapsed);
          return newMap;
        });
      };
      return (
        <Flex vertical>
          {item.images && <ChatImages images={item.images} />}
          {item.files && <ChatFiles files={item.files} />}
          {citeMessageList.length > 0 ? (
            <Tooltip
              placement="topRight"
              overlayClassName="chat-user-citation-tooltip"
              title={
                <div className="chat-user-citation-tooltip-content">
                  {citeMessageList.map((citeMessage, index) => (
                    <div className="chat-user-citation-tooltip-item" key={`${index}-${citeMessage}`}>
                      {citeMessage}
                    </div>
                  ))}
                </div>
              }
            >
              <span className="chat-user-citation-icon" aria-label={t("chat.cite")}>
                <CommentOutlined />
              </span>
            </Tooltip>
          ) : null}
          {item.reasoning_content && (
            <>
              <div className="chat-think-status" onClick={toggleCollapse}>
                <img src={ThinkIcon} className="chat-think-icon" />
                <span className="chat-think-title">
                  {item.delta ? "已深度思考" : "思考中"}
                  {(item.thinking_duration_s || item.thinking_time_s) &&
                    item.thinking_duration_s !== "0" &&
                    item.thinking_time_s !== "0" &&
                    ` (${item.thinking_duration_s || item.thinking_time_s}s)`}
                </span>
                {isCollapsed ? (
                  <UpOutlined className="chat-arrow-icon" />
                ) : (
                  <DownOutlined className="chat-arrow-icon" />
                )}
              </div>
              <div className={isCollapsed ? "chat-collapse" : "chat-expand"}>
                <div className="chat-think-text">
                  <MarkdownViewer
                    sources={item.sources}
                    IS_STREAMING={
                      item.finish_reason !==
                      ChatConversationsResponseFinishReasonEnum.FinishReasonStop
                    }
                  >
                    {formatThinkingForDisplay(item.reasoning_content)}
                  </MarkdownViewer>
                </div>
                {!item.delta &&
                  item.finish_reason !==
                    ChatConversationsResponseFinishReasonEnum.FinishReasonStop && (
                    <Spin />
                  )}
              </div>
            </>
          )}
          <div className="chat-text">
            <MarkdownViewer
              sources={item.sources}
              IS_STREAMING={
                item.finish_reason !==
                ChatConversationsResponseFinishReasonEnum.FinishReasonStop
              }
            >
              {item.display_delta || item.delta}
            </MarkdownViewer>
          </div>
        </Flex>
      );
    }

    const getScrollMetrics = useCallback(() => {
      const el = chatContentRef.current;
      if (!el) {
        return null;
      }

      const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
      return {
        distance,
        hasScrollbar: el.scrollHeight > el.clientHeight + 2,
      };
    }, []);

    const updateScrollButtonVisibility = useCallback(() => {
      const metrics = getScrollMetrics();
      if (!metrics) {
        return;
      }

      setShowScrollButton(metrics.hasScrollbar && metrics.distance > 10);
    }, [getScrollMetrics]);

    const handleScroll = () => {
      const metrics = getScrollMetrics();
      if (!metrics) {
        return;
      }

      setShowScrollButton(metrics.hasScrollbar && metrics.distance > 10);
      if (metrics.distance <= 10) {
        isMouseScrollingRef.current = true;
      } else {
        isMouseScrollingRef.current = false;
      }
    };

    const handleToBottom = () => {
      const el = chatContentRef.current;
      if (!el) {
        return;
      }
      isMouseScrollingRef.current = true;
      el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
      setShowScrollButton(false);
    };

    useEffect(() => {
      const rafId = requestAnimationFrame(() => {
        updateScrollButtonVisibility();
      });

      return () => cancelAnimationFrame(rafId);
    }, [messageList, thinkingCollapseMap, inputHeight, updateScrollButtonVisibility]);

    useEffect(() => {
      const updateInputHeight = () => {
        const inputElement = chatInputRef.current?.element;
        if (inputElement) {
          const height = inputElement.offsetHeight;
          setInputHeight(height + 20);
          document.documentElement.style.setProperty(
            "--chat-input-height",
            `${height + 20}px`,
          );
        }
      };

      updateInputHeight();

      window.addEventListener("resize", updateInputHeight);

      const observer = new MutationObserver(() => {
        updateInputHeight();
      });

      if (chatInputRef.current?.element) {
        observer.observe(chatInputRef.current.element, {
          attributes: true,
          childList: true,
          subtree: true,
          attributeFilter: ["style", "class"],
        });
      }

      return () => {
        window.removeEventListener("resize", updateInputHeight);
        observer.disconnect();
      };
    }, []);

    const handleInputHeightChange = () => {
      const inputElement = chatInputRef.current?.element;
      if (inputElement) {
        const height = inputElement.offsetHeight;
        setInputHeight(height + 20);
        document.documentElement.style.setProperty(
          "--chat-input-height",
          `${height + 20}px`,
        );
      }
    };

    const handleAddCiteMessage = useCallback(
      (text: string) => {
        const normalizedText = text.trim();
        if (!normalizedText) {
          return;
        }

        setCiteMessages((prev) => {
          if (prev.length >= MAX_CITE_MESSAGE_COUNT) {
            message.warning(
              t("chat.maxCitationsWarning", {
                count: MAX_CITE_MESSAGE_COUNT,
              }),
            );
            return prev;
          }

          return [...prev, normalizedText];
        });
        requestAnimationFrame(() => {
          chatInputRef.current?.focus();
        });
      },
      [t],
    );

    const handleRemoveCiteMessage = useCallback((index: number) => {
      setCiteMessages((prev) =>
        prev.filter((_, itemIndex) => itemIndex !== index),
      );
    }, []);

    return (
      <div className="chat-chat-container">
        <div className="chat-box">
          <MessageList
            messageList={messageList}
            initialCard={initialCard}
            sendMessage={(text, clearInput) => {
              sendMessage({ text, clearInput });
            }}
            regenerate={regenerate}
            stopGeneration={stopGeneration}
            renderText={renderText}
            updateAssistantMessage={updateAssistantMessage}
            onCiteMessage={handleAddCiteMessage}
            onScroll={handleScroll}
            chatContentRef={chatContentRef}
            sessionId={sessionId}
            onPreferenceSelect={handlePreferenceSelect}
            editingUserMessageIndex={editingUserMessageIndex}
            editingUserMessageText={editingUserMessageText}
            editingUserMessageCites={editingUserMessageCites}
            onUserMessageEditTextChange={setEditingUserMessageText}
            onRemoveEditingUserMessageCite={handleRemoveEditingUserMessageCite}
            onStartEditUserMessage={handleStartEditUserMessage}
            onCancelEditUserMessage={handleCancelEditUserMessage}
            onResendEditedUserMessage={handleResendEditedUserMessage}
            onCopyUserMessage={handleCopyUserMessage}
          />

          {messageList.length > 0 && (
            <div
              style={{ bottom: inputHeight }}
              className={`toBottomContainer ${!showScrollButton ? "hidden" : ""}`}
            >
              <span className="toBottom" onClick={handleToBottom}>
                <DoubleRightOutlined
                  style={{
                    fontSize: 18,
                    cursor: "pointer",
                    color: "#8d9ab2",
                    transform: "rotate(90deg)",
                  }}
                />
              </span>
            </div>
          )}

          <ChatInput
            value={content}
            onChange={setContent}
            onSend={sendMessage}
            openHistory={
              setShowHistoryList ? () => setShowHistoryList(true) : undefined
            }
            isChatContent={true}
            showHistoryList={showHistoryList}
            showHistoryButton={showHistoryButton}
            showPromptSuggestions={false}
            openNewChat={createNewChat}
            ref={chatInputRef}
            onHeightChange={handleInputHeightChange}
            chatConfig={chatConfig}
            setChatConfig={setChatConfig}
            setChatConfigFn={setChatConfigFn}
            knowledgeRefreshKey={knowledgeRefreshKey}
            embeddingReady={embeddingReady}
            multimodalEmbeddingReady={multimodalEmbeddingReady}
            rerankReady={rerankReady}
            sessionId={sessionId}
            isStreaming={IS_STREAMING}
            disabled={!canChat}
            disabledReason={disabledReason}
            disabledDescription={disabledDescription}
            disabledAction={disabledAction}
            citeMessages={citeMessages}
            onRemoveCiteMessage={handleRemoveCiteMessage}
            onClearCiteMessage={() => setCiteMessages([])}
          />
        </div>
      </div>
    );
  },
);

ChatContainerComponent.displayName = "ChatContainerComponent";

export default ChatContainerComponent;
