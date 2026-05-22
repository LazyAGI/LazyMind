import {
  useRef,
  useState,
  useEffect,
  forwardRef,
  useImperativeHandle,
  ReactElement,
} from "react";
import { Button, Spin, Input, Flex, Badge, message } from "antd";
import {
  PlusSquareOutlined,
  SendOutlined,
  DownOutlined,
  UpOutlined,
  FileImageOutlined,
  FileTextOutlined,
  EditOutlined,
} from "@ant-design/icons";
import {
  ChatConversationsRequestActionEnum,
  ChatConversationsResponseFinishReasonEnum,
  Conversation,
  Query,
  Source,
} from "@/api/generated/chatbot-client";
import { RcFile } from "antd/es/upload";
import RiskTip from "../RiskTip";
import UIUtils from "@/modules/chat/utils/ui";
import { RoleTypes } from "@/modules/chat/constants/common";
import "./index.scss";
import MarkdownViewer from "@/modules/chat/components/MarkdownViewer";
import ImageUpload, { ImageUploadImperativeProps } from "../ImageUpload";
import PromptModal, { PromptImperativeProps } from "../PromptModal";
import AssistantMessage from "../AssistantMessage";
import { fileToBase64 } from "@/modules/chat/utils/upload";
import ChatImages, { ChatImage } from "../ChatImages";
import ChatFiles, { ChatFile } from "../ChatFiles";
import BatchChatComponent, { BatchChatImperativeProps } from "../BatchChat";
import { streamManager } from "@/modules/chat/utils/StreamManager";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";
import { getRegenerationInputs } from "@/modules/chat/utils/message";
import {
  splitThinkingContent,
  formatThinkingForDisplay,
} from "@/modules/chat/utils/thinking";

const ThinkIcon = new URL("../../assets/images/think.png", import.meta.url)
  .href;

export interface ChatImperativeProps {
  replaceMessageList: (id: string, data: any[]) => void;
  createNewChat: () => void;
}

const { TextArea } = Input;

interface Props {
  canChat?: boolean;
  initialCard?: ReactElement | string;
  onOpenSSE: (
    input: any[],
    action: ChatConversationsRequestActionEnum,
    callbacks: Record<string, (e: CustomEvent) => void>,
  ) => any; // Return new SSE.
  onConversationIdChange?: (conversationId: string) => void;
  onCreateNewChat?: () => void;
  onNewConversationCreated?: (conversationId: string) => void;
  parseErrorData: (data: string) => string;
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
  create_time?: string;
  answers?: Array<{
    content: string;
    index: number;
    history_id?: string;
    raw_content?: string;
    reasoning_content?: string;
    sources?: Source[];
    thinking_duration_s?: string;
  }>;
}

const ChatContainerComponent = forwardRef<ChatImperativeProps, Props>(
  (
    {
      canChat = true,
      initialCard,
      onOpenSSE,
      onConversationIdChange,
      onCreateNewChat,
      onNewConversationCreated,
      parseErrorData,
    },
    ref,
  ) => {
    const { t } = useTranslation();
    const batchChatTask = localStorage.getItem("batchChatTask");
    const isMouseScrollingRef = useRef(false);
    const sseRef = useRef<any>(null);
    const activeStreamRef = useRef(false);
    const imageRef = useRef<ImageUploadImperativeProps | null>(null);
    const fileRef = useRef<ImageUploadImperativeProps | null>(null);
    const promptRef = useRef<PromptImperativeProps | null>(null);
    const batchChatRef = useRef<BatchChatImperativeProps | null>(null);
    const currentConversationIdRef = useRef<string>("");
    const messageListRef = useRef<any[]>([]);
    const saveTimerRef = useRef<number | null>(null);
    const conversationMessagesCache = useRef<Map<string, any[]>>(new Map());
    const newConversationIdsRef = useRef<Set<string>>(new Set());
    const chatBoxRef = useRef<HTMLDivElement>(null);

    const [showDot, setShowDot] = useState(batchChatTask);

    const [messageList, setMessageList] = useState<any[]>([]);
    const [loading, setLoading] = useState(false);
    const [content, setContent] = useState("");
    const [isThinkingCollapse, setIsThinkingCollapse] = useState(false);
    const [imageList, setImageList] = useState<ChatImage[]>([]);
    const [fileList, setFileList] = useState<ChatFile[]>([]);

    const IMAGE_MAX_COUNT = 2;
    const IMAGE_MAX_TIPS = `最多上传 ${IMAGE_MAX_COUNT} 张图片`;
    const FILE_MAX_COUNT = 6;
    const FILE_MAX_TIPS = `最多上传 ${FILE_MAX_COUNT} 个文件`;
    const allowedImageTypes = [".png", ".jpg", ".jpeg"];
    const allowedFileTypes = [".pdf", ".docx", ".doc", ".pptx"];

    useImperativeHandle(ref, () => ({
      replaceMessageList,
      createNewChat,
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
      setImageList([]);
      imageRef.current?.clear();
      setFileList([]);
      fileRef.current?.clear();
    }

    function autoSelectPreviousAnswerInList(currentMessages: any[]) {
      let lastAssistantMessageIndex = -1;
      for (let i = currentMessages.length - 1; i >= 0; i--) {
        if (currentMessages[i].role === RoleTypes.ASSISTANT) {
          lastAssistantMessageIndex = i;
          break;
        }
      }

      if (lastAssistantMessageIndex === -1) {
        return currentMessages;
      }

      const lastAssistantMessage = currentMessages[lastAssistantMessageIndex];

      const hasMultipleAnswers =
        lastAssistantMessage.answers &&
        Array.isArray(lastAssistantMessage.answers) &&
        lastAssistantMessage.answers.length >= 2;

      if (!hasMultipleAnswers) {
        return currentMessages;
      }

      if (
        lastAssistantMessage.selected_answer_index !== undefined &&
        lastAssistantMessage.selected_answer_index !== null
      ) {
        return currentMessages;
      }

      if (
        lastAssistantMessage.finish_reason !==
        ChatConversationsResponseFinishReasonEnum.FinishReasonStop
      ) {
        return currentMessages;
      }

      const selectedIndex = 0;
      const allAnswers = lastAssistantMessage.answers;
      const selectedAnswer = allAnswers[selectedIndex];
      const selectedHistoryId = selectedAnswer.history_id;

      const deletedHistoryIds = allAnswers
        .filter((_: any, index: number) => index !== selectedIndex)
        .map((answer: any) => answer.history_id);

      const promises = deletedHistoryIds.map((deletedHistoryId: string) => {
        return ChatServiceApi().conversationServiceSetChatHistory({
          setChatHistoryRequest: {
            deleted_history_id: deletedHistoryId,
            set_history_id: selectedHistoryId,
          } as any,
        });
      });

      Promise.all(promises).catch((error) => {
        console.error(t("chat.autoSelectAnswerFailed"), error);
      });

      const newMessageList = [...currentMessages];
      newMessageList[lastAssistantMessageIndex] = {
        ...lastAssistantMessage,
        selected_answer_index: selectedIndex,
        answer_preference: "prefer_first",
        delta: selectedAnswer.content || "",
        raw_delta: selectedAnswer.raw_content || selectedAnswer.content || "",
        reasoning_content: selectedAnswer.reasoning_content || "",
        sources: selectedAnswer.sources || lastAssistantMessage.sources,
        history_id:
          selectedAnswer.history_id || lastAssistantMessage.history_id,
        thinking_duration_s: selectedAnswer.thinking_duration_s,
      };
      return newMessageList;
    }

    function sendMessage(text: string, clearInput = true) {
      const normalizedText = text.trim();
      if (activeStreamRef.current || loading || !canChat || !normalizedText) {
        return;
      }

      const inputs = [
        { input_type: "text", text: normalizedText },
        ...getFileUrls(imageRef.current?.getFiles(), imageList).map((image) => {
          return {
            input_type: "image",
            uri: image.uri || "",
            input_base64: image.base64 || "",
          };
        }),
        ...getFileUrls(fileRef.current?.getFiles()).map((file) => {
          return { input_type: "file", uri: file.uri || "" };
        }),
      ];
      if (clearInput) {
        setContent("");
        clearMultiData();
      }

      const messagesWithAutoSelection = autoSelectPreviousAnswerInList(
        messageListRef.current,
      );

      const userMessage = {
        delta: normalizedText,
        role: RoleTypes.USER,
        images: imageList,
        files: fileList,
        inputs,
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      };
      const assistantMessage = {
        role: RoleTypes.ASSISTANT,
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
      };
      const newMessageList = [
        ...messagesWithAutoSelection,
        userMessage,
        assistantMessage,
      ];
      messageListRef.current = newMessageList;
      setMessageList(newMessageList);

      isMouseScrollingRef.current = true;
      scrollToEnd();
      openSSE(inputs, ChatConversationsRequestActionEnum.ChatActionNext);

      if (currentConversationIdRef.current) {
        streamManager.saveMessageList(
          currentConversationIdRef.current,
          newMessageList,
        );
      }
    }

    const openSSE = (
      input: any[],
      action: ChatConversationsRequestActionEnum,
    ) => {
      activeStreamRef.current = true;
      setLoading(true);
      const callbacks: Record<string, (e: CustomEvent) => void> = {
        message: (e) => onMessage(e),
        error: (e) => onError(e),
        timeout: (e) => onTimeout(e),
      };
      const sse = onOpenSSE(input, action, {});
      sseRef.current = sse;

      let conversationId = currentConversationIdRef.current;
      if (!conversationId) {
        conversationId = `temp_${Date.now()}_${Math.random().toString(36).substring(2, 15)}`;
        currentConversationIdRef.current = conversationId;
      } else if (!conversationId.startsWith("temp_")) {
        newConversationIdsRef.current.add(conversationId);
      }

      streamManager.registerStream(conversationId, sse, callbacks);
      streamManager.setActiveConversation(conversationId);
      streamManager.saveMessageList(conversationId, messageListRef.current);
    };

    function closeSSE() {
      sseRef.current = null;
      activeStreamRef.current = false;
      setLoading(false);
    }

    function onMessage(e: any) {
      const result = UIUtils.jsonParser(e.data)?.result;

      if (!result) {
        return;
      }

      const messageConversationId = result.conversation_id || "";
      const currentConversationIdAtStart = currentConversationIdRef.current;

      const isUsingTempId = currentConversationIdAtStart.startsWith("temp_");
      const isActiveConversation = messageConversationId
        ? messageConversationId === currentConversationIdAtStart ||
          (isUsingTempId && messageConversationId)
        : currentConversationIdAtStart === "";

      const isFirstTimeReceivingId =
        result.conversation_id &&
        result.conversation_id !== currentConversationIdRef.current &&
        isActiveConversation;

      const isNewConversationFromFrontend =
        result.conversation_id &&
        newConversationIdsRef.current.has(result.conversation_id) &&
        result.conversation_id === currentConversationIdRef.current &&
        isActiveConversation;

      if (isFirstTimeReceivingId || isNewConversationFromFrontend) {
        if (onConversationIdChange) {
          onConversationIdChange(result.conversation_id);
        }

        const previousConversationId = currentConversationIdRef.current;
        const isPreviousTempId = previousConversationId.startsWith("temp_");
        const isNewConversation =
          isPreviousTempId || previousConversationId !== result.conversation_id;

        if (isFirstTimeReceivingId) {
          currentConversationIdRef.current = result.conversation_id;
          streamManager.setActiveConversation(result.conversation_id);
        }

        if (isPreviousTempId && sseRef.current && isFirstTimeReceivingId) {
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

          const currentList = messageListRef.current;
          conversationMessagesCache.current.set(
            result.conversation_id,
            currentList,
          );
          streamManager.saveMessageList(result.conversation_id, currentList);
        }

        if (
          (isNewConversation || isNewConversationFromFrontend) &&
          onNewConversationCreated
        ) {
          onNewConversationCreated(result.conversation_id);
          newConversationIdsRef.current.delete(result.conversation_id);
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
          closeSSE();
        }

        const cleanupConversationId =
          messageConversationId || currentConversationIdAtStart;
        if (cleanupConversationId) {
          setTimeout(() => {
            streamManager.closeAndCleanup(cleanupConversationId);
          }, 1000);
        }
      }

      const updateMessageListInternal = (list: any[]) => {
        const newList = [...list];
        let assistantMessage =
          newList.length > 0 ? newList[newList.length - 1] : null;

        if (
          !assistantMessage ||
          assistantMessage.role !== RoleTypes.ASSISTANT
        ) {
          assistantMessage = {
            role: RoleTypes.ASSISTANT,
            delta: "",
            reasoning_content: "",
            finish_reason:
              ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
          };
          newList.push(assistantMessage);
        }

        const previousDelta = assistantMessage.delta || "";
        const previousRawDelta =
          assistantMessage.raw_delta || previousDelta || "";

        const previousSecondDelta = assistantMessage.second_result || "";
        const previousSecondRawDelta =
          assistantMessage.second_raw_result || previousSecondDelta || "";

        const mergedRawDelta = previousRawDelta + (result.delta || "");
        const splitResult = splitThinkingContent(
          mergedRawDelta,
          assistantMessage.reasoning_content || "",
        );
        const mergedSecondRawDelta =
          previousSecondRawDelta + (result.second_result || "");
        const secondSplitResult = splitThinkingContent(
          mergedSecondRawDelta,
          assistantMessage.second_reasoning_content || "",
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
          second_raw_result: mergedSecondRawDelta,
          second_result: secondSplitResult.content,
          second_reasoning_content: secondSplitResult.reasoning_content,
          second_id: result.second_id || assistantMessage.second_id,
        };

        if (
          assistantMessage.second_result &&
          assistantMessage.second_reasoning_content &&
          assistantMessage.second_id
        ) {
          assistantMessage.answers = [
            {
              content: assistantMessage.delta || "",
              index: 0,
              history_id: assistantMessage.id || result.messageId,
              raw_content:
                assistantMessage.raw_delta || assistantMessage.delta || "",
              reasoning_content: assistantMessage.reasoning_content || "",
              sources: assistantMessage.sources,
              thinking_duration_s: assistantMessage.thinking_duration_s,
            },
            {
              content: assistantMessage.second_result,
              index: 1,
              history_id: assistantMessage.second_id,
              raw_content:
                assistantMessage.second_raw_result ||
                assistantMessage.second_result,
              reasoning_content: assistantMessage.second_reasoning_content,
              sources: assistantMessage.sources,
              thinking_duration_s: assistantMessage.second_thinking_duration_s,
            },
          ];
          assistantMessage.reasoning_content = "";
          assistantMessage.delta = "";
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
        if (
          messageConversationId &&
          streamManager.hasActiveStream(messageConversationId)
        ) {
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

          conversationMessagesCache.current.set(messageConversationId, newList);
          streamManager.saveMessageList(messageConversationId, newList);
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
        closeSSE();
      }

      if (errorConversationId) {
        streamManager.closeAndCleanup(errorConversationId);
        conversationMessagesCache.current.delete(errorConversationId);
      }
    }

    function onTimeout(e: any) {
      if (e.type !== "timeout") {
        return;
      }
      onError({ type: "error" });
    }

    // Update answer: If you don't pass the ID, it will be the last one.
    function updateAssistantMessage(data: any, id?: string) {
      setMessageList((list) => {
        const newList = [...list];
        const index = id
          ? newList.findIndex((msg) => msg.id === id || msg.history_id === id)
          : newList.length - 1;
        if (index >= 0) {
          newList[index] = { ...newList[index], ...data };
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
      requestAnimationFrame(() => {
        const container = chatBoxRef.current;
        if (container) {
          container.scrollTop = container.scrollHeight;
        }
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
          } else {
            messageListRef.current = list;
            setMessageList(list);
            if (
              streamState.finish_reason ===
              ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
            ) {
              setLoading(true);
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
      scrollToEnd();
    }

    function createNewChat() {
      clearMultiData();

      if (onCreateNewChat) {
        onCreateNewChat();
        return;
      }

      setMessageList([]);
      messageListRef.current = [];
      setLoading(false);

      if (sseRef.current) {
        sseRef.current = null;
      }

      replaceMessageList("", []);
    }

    function onBatchChat() {
      batchChatRef.current?.onOpen();
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

      closeSSE();

      if (conversationId) {
        streamManager.closeAndCleanup(conversationId);
        conversationMessagesCache.current.delete(conversationId);
      }
    }

    function regenerate() {
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
      const assistantMessage = {
        role: RoleTypes.ASSISTANT,
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
      };
      const newList = [...messageListRef.current];
      newList[newList.length - 1] = assistantMessage;
      messageListRef.current = newList;
      setMessageList(newList);
      isMouseScrollingRef.current = true;
      openSSE(
        regenerationInputs,
        ChatConversationsRequestActionEnum.ChatActionRegeneration,
      );
    }

    function renderText(item: any) {
      const isStreaming =
        item.finish_reason !==
        ChatConversationsResponseFinishReasonEnum.FinishReasonStop;

      return (
        <Flex vertical>
          {item.images && <ChatImages images={item.images} />}
          {item.files && <ChatFiles files={item.files} />}
          {item.reasoning_content && (
            <>
              <div
                className="chat-think-status"
                onClick={() => {
                  setIsThinkingCollapse(!isThinkingCollapse);
                }}
              >
                <img src={ThinkIcon} className="chat-think-icon" />
                <span className="chat-think-title">
                  {item.delta ? t("chat.thinkingDone") : t("chat.thinking")}{" "}
                  {item.thinking_duration_s &&
                    item.thinking_duration_s !== "0" &&
                    ` (${item.thinking_duration_s}s)`}
                </span>
                {isThinkingCollapse ? (
                  <UpOutlined className="chat-arrow-icon" />
                ) : (
                  <DownOutlined className="chat-arrow-icon" />
                )}
              </div>
              <div
                className={isThinkingCollapse ? "chat-collapse" : "chat-expand"}
              >
                <div className="chat-think-text">
                  <MarkdownViewer
                    sources={item.sources}
                    IS_STREAMING={isStreaming}
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
            <MarkdownViewer sources={item.sources} IS_STREAMING={isStreaming}>
              {item.delta}
            </MarkdownViewer>
          </div>
        </Flex>
      );
    }

    function renderUser(item: any) {
      return (
        <div className="user-message-row">
          {item.create_time && (
            <div className="chat-time">
              {dayjs(item.create_time).format("MM/DD HH:mm")}
            </div>
          )}
          <div className="user-wrap">
            <div className="chat-user">{renderText(item)}</div>
          </div>
        </div>
      );
    }

    const removeImage = (uid: string) => {
      imageRef.current?.removeFile(uid);
      const list = [...imageList].filter((item) => item.uid !== uid);
      setImageList(list);
    };

    const removeFile = (uid: string) => {
      fileRef.current?.removeFile(uid);
      const list = [...fileList].filter((item) => item.uid !== uid);
      setFileList(list);
    };

    const updateImageList = async (list: RcFile[]) => {
      const data: ChatImage[] = [];
      for (let i = 0; i < list.length; i++) {
        const res = await fileToBase64(list[i]);
        data.push({
          uid: list[i].uid,
          base64: res as string,
        });
      }
      setImageList(data);
    };

    const updateFileList = (list: RcFile[]) => {
      const data: ChatFile[] = [];
      for (let i = 0; i < list.length; i++) {
        data.push({
          name: list[i].name,
          uid: list[i].uid,
        });
      }
      setFileList(data);
    };

    const handleScroll = () => {
      const el = chatBoxRef.current;
      if (!el) return;

      const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
      if (distance <= 50) {
        isMouseScrollingRef.current = true;
      } else {
        isMouseScrollingRef.current = false;
      }
    };

    return (
      <div className="chat-chat-container">
        <div className="chat-box" ref={chatBoxRef} onScroll={handleScroll}>
          <div
            className="message-container chat-content"
            // onWheel={(e) => onChatListWheel(e.deltaY)}
          >
            {messageList.length > 0 &&
              messageList.map((item, index) => {
                return (
                  <div className="chat-item" key={`chat-${index}`}>
                    {item.role === RoleTypes.USER && renderUser(item)}
                    {item.role === RoleTypes.ASSISTANT && (
                      <AssistantMessage
                        item={item}
                        index={index}
                        length={messageList.length}
                        sendMessage={sendMessage}
                        regenerate={regenerate}
                        stopGeneration={stopGeneration}
                        renderText={renderText}
                        updateMessage={(
                          msg: Conversation & {
                            id?: string;
                            history_id?: string;
                          },
                        ) =>
                          updateAssistantMessage(msg, msg.id || msg.history_id)
                        }
                      />
                    )}
                  </div>
                );
              })}
            {messageList.length === 0 && initialCard}
          </div>
          <div className="action-container">
            <div className="bottom-bar">
              <Flex gap="8px">
                <Button
                  size="small"
                  className="add-btn"
                  icon={<PlusSquareOutlined />}
                  onClick={createNewChat}
                >
                  新增对话
                </Button>
                {}
                <Badge dot={showDot === "true"}>
                  <Button
                    size="small"
                    className="add-btn"
                    icon={<PlusSquareOutlined />}
                    onClick={onBatchChat}
                  >
                    批量对话
                  </Button>
                </Badge>
                <Button
                  size="small"
                  className="add-btn"
                  icon={<EditOutlined />}
                  onClick={() => promptRef.current?.onOpen()}
                >
                  常用话术
                </Button>
                <ImageUpload
                  updateFiles={updateImageList}
                  listNum={imageList.length}
                  ref={imageRef}
                  types={allowedImageTypes}
                  max={IMAGE_MAX_COUNT}
                  maxTips={IMAGE_MAX_TIPS}
                  maxSize={5} // 5 MB
                  icon={
                    <FileImageOutlined
                      style={{
                        cursor: imageList?.length >= 2 ? "no-drop" : "pointer",
                      }}
                    />
                  }
                />
                <ImageUpload
                  updateFiles={updateFileList}
                  listNum={fileList.length}
                  ref={fileRef}
                  types={allowedFileTypes}
                  max={FILE_MAX_COUNT}
                  maxTips={FILE_MAX_TIPS}
                  maxSize={100} // 100 MB
                  icon={
                    <FileTextOutlined
                      style={{
                        cursor: fileList?.length >= 6 ? "no-drop" : "pointer",
                      }}
                    />
                  }
                />
                <RiskTip />
              </Flex>
              <span className="chat-tip">AI生成内容不代表开发者立场</span>
            </div>
            <ChatImages images={imageList} onRemove={removeImage} />
            <ChatFiles files={fileList} onRemove={removeFile} />
            <div
              className={`input-box ${loading || !canChat ? "disabled" : ""}`}
            >
              <TextArea
                className="input"
                value={content}
                placeholder="请输入问题，进行智能问答、图文理解等多种任务，使用 Shift+Enter 换行"
                onPressEnter={(e) => {
                  if (e.shiftKey) {
                    return;
                  }
                  e.preventDefault();
                  sendMessage(content);
                }}
                autoSize={{ minRows: 1, maxRows: 3 }}
                disabled={loading || !canChat}
                onChange={(e) => setContent(e.target.value)}
              />
              <Button
                type="primary"
                shape="round"
                className="submit-btn"
                onClick={() => sendMessage(content)}
                disabled={loading || !content.trim() || !canChat}
              >
                <SendOutlined />
              </Button>
            </div>
          </div>
        </div>

        {}
        <PromptModal
          ref={promptRef}
          onSelectPrompt={(prompt) => setContent(prompt)}
        />

        {}
        <BatchChatComponent
          ref={batchChatRef}
          cancelFn={(bool) => setShowDot(bool)}
        />
      </div>
    );
  },
);

ChatContainerComponent.displayName = "ChatContainerComponent";

export default ChatContainerComponent;
