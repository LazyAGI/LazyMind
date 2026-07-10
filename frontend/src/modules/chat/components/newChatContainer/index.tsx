import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
} from "react";
import { useTranslation } from "react-i18next";
import { message } from "antd";
import { ChatConversationsResponseFinishReasonEnum } from "@/api/generated/chatbot-client";
import { useChatMessageStore } from "@/modules/chat/store/chatMessage";
import { RoleTypes } from "@/modules/chat/constants/common";
import ChatInput, {
  ChatInputImperativeProps,
  SKILL_DEPOSIT_MIN_TOOL_CALL_TURNS,
  SKILL_DEPOSIT_MIN_USER_TURNS,
  type SkillDepositStats,
} from "../ChatInput";
import "./index.scss";
import MessageList from "./components/MessageList";
import ChatMessageContent from "./components/ChatMessageContent";
import ScrollToBottomButton from "./components/ScrollToBottomButton";
import { useChatConversation } from "./hooks/useChatConversation";
import { useCiteMessagesInput } from "./hooks/useCiteMessagesInput";
import { useThinkingCollapse } from "./hooks/useThinkingCollapse";
import { useUserMessageEdit } from "./hooks/useUserMessageEdit";
import type { ChatContainerProps, ChatImperativeProps } from "./types";

export type { ChatImperativeProps, ChatMessage } from "./types";

const SKILL_DEPOSIT_REMINDER_KEY_PREFIX = "skill-deposit-reminded:";
const SKILL_DEPOSIT_PROMPT_PREFIXES = [
  "请把当前会话沉淀为 skill。",
  "Deposit the current conversation as a skill.",
];

function getSkillDepositStats(messageList: any[]): SkillDepositStats {
  return messageList.reduce<SkillDepositStats>(
    (stats, item) => {
      if (item?.role === RoleTypes.USER && !item?.is_resumed) {
        const hasText = Boolean((item.delta || item.display_delta || "").trim());
        const hasInputs = Array.isArray(item.inputs) && item.inputs.length > 0;
        if (hasText || hasInputs) {
          stats.userTurns += 1;
        }
      }
      if (item?.role === RoleTypes.ASSISTANT) {
        const toolCallTurns = Number(item.tool_call_turns ?? 0);
        if (Number.isFinite(toolCallTurns) && toolCallTurns > 0) {
          stats.toolCallTurns += toolCallTurns;
        }
      }
      return stats;
    },
    { userTurns: 0, toolCallTurns: 0 },
  );
}

function isSkillDepositPromptMessage(item: any, currentPrompt: string) {
  if (!item || item.role !== RoleTypes.USER) {
    return false;
  }

  const text = String(
    item.display_delta ||
      item.delta ||
      item.inputs?.find((input: any) => input?.input_type === "text")?.text ||
      "",
  ).trim();
  const prompt = currentPrompt.trim();
  return (
    Boolean(text) &&
    (text === prompt ||
      SKILL_DEPOSIT_PROMPT_PREFIXES.some((prefix) => text.startsWith(prefix)))
  );
}

const ChatContainerComponent = forwardRef<ChatImperativeProps, ChatContainerProps>(
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
      onPluginSettingsChange,
      initialPluginSettings,
      hasPluginSession,
    } = props;

    const { clearPendingMessage: clearStorePendingMessage } =
      useChatMessageStore();
    const chatInputRef = useRef<ChatInputImperativeProps>(null);
    const userEditRef = useRef<ReturnType<typeof useUserMessageEdit>>();
    const skillDepositWasReadyRef = useRef(false);
    const skillDepositMessageCountRef = useRef(0);

    const {
      thinkingCollapseMap,
      toggleThinkingCollapse,
      isThinkingCollapsed,
    } = useThinkingCollapse();

    const {
      citeMessages,
      handleAddCiteMessage,
      handleRemoveCiteMessage,
      clearCiteMessages,
    } = useCiteMessagesInput(chatInputRef);

    const conversation = useChatConversation({
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
      getUserEdit: () => userEditRef.current,
      t,
    });

    const userEdit = useUserMessageEdit({
      canChat,
      disabledReason,
      loading: conversation.loading,
      activeStreamRef: conversation.activeStreamRef,
      messageList: conversation.messageList,
      messageListRef: conversation.messageListRef,
      setMessageList: conversation.setMessageList,
      currentConversationIdRef: conversation.currentConversationIdRef,
      conversationMessagesCache: conversation.conversationMessagesCache,
      openSSE: conversation.openSSE,
      scrollToEnd: conversation.scroll.scrollToEnd,
    });

    userEditRef.current = userEdit;

    const skillDepositStats = useMemo(
      () => getSkillDepositStats(conversation.messageList),
      [conversation.messageList],
    );
    const isLastUserMessageSkillDepositPrompt = useMemo(() => {
      const lastUserMessage = conversation.messageList.findLast(
        (item) => item?.role === RoleTypes.USER,
      );
      return isSkillDepositPromptMessage(
        lastUserMessage,
        t("chat.skillDepositPrompt"),
      );
    }, [conversation.messageList, t]);
    const canSkillDeposit =
      skillDepositStats.userTurns >= SKILL_DEPOSIT_MIN_USER_TURNS &&
      skillDepositStats.toolCallTurns >= SKILL_DEPOSIT_MIN_TOOL_CALL_TURNS &&
      !isLastUserMessageSkillDepositPrompt;
    const isSkillDepositTurnFinished = useMemo(() => {
      const lastAssistantMessage = conversation.messageList.findLast(
        (item) => item?.role === RoleTypes.ASSISTANT,
      );
      return Boolean(
        lastAssistantMessage &&
          lastAssistantMessage.finish_reason !==
            ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
      );
    }, [conversation.messageList]);
    const shouldRemindSkillDeposit =
      canSkillDeposit &&
      isSkillDepositTurnFinished &&
      !conversation.isStreaming &&
      !conversation.loading;

    useEffect(() => {
      const previousMessageCount = skillDepositMessageCountRef.current;
      skillDepositMessageCountRef.current = conversation.messageList.length;
      if (
        previousMessageCount === 0 &&
        conversation.messageList.length > 0 &&
        canSkillDeposit &&
        !conversation.isStreaming &&
        !conversation.loading
      ) {
        skillDepositWasReadyRef.current = true;
      }
    }, [
      canSkillDeposit,
      conversation.isStreaming,
      conversation.loading,
      conversation.messageList.length,
    ]);

    useEffect(() => {
      if (!canSkillDeposit) {
        skillDepositWasReadyRef.current = false;
        return;
      }
      if (!shouldRemindSkillDeposit || skillDepositWasReadyRef.current) {
        return;
      }

      const conversationId =
        conversation.currentConversationIdRef.current || sessionId;
      if (!conversationId || conversationId.startsWith("temp_")) {
        return;
      }

      skillDepositWasReadyRef.current = true;
      const reminderKey = `${SKILL_DEPOSIT_REMINDER_KEY_PREFIX}${conversationId}`;
      if (sessionStorage.getItem(reminderKey)) {
        return;
      }
      sessionStorage.setItem(reminderKey, "1");
      message.info(t("chat.skillDepositReminder"));
    }, [
      canSkillDeposit,
      conversation.currentConversationIdRef,
      sessionId,
      shouldRemindSkillDeposit,
      t,
    ]);

    useImperativeHandle(ref, () => ({
      replaceMessageList: conversation.replaceMessageList,
      createNewChat: conversation.createNewChat,
      sendMessage: conversation.sendMessage,
      disconnectConversationStream: conversation.disconnectConversationStream,
      uploadFiles: (files: File[]) => {
        chatInputRef.current?.uploadFiles(files);
      },
      openResumeSSE: onOpenResumeSSE
        ? conversation.openResumeSSE
        : undefined,
      appendAutoAdvanceTurn: onOpenResumeSSE
        ? conversation.appendAutoAdvanceTurn
        : undefined,
      ensureAutoAdvanceUserTurn: conversation.ensureAutoAdvanceUserTurn,
    }));

    const renderText = useCallback(
      (item: any, uniqueKey?: string) => (
        <ChatMessageContent
          item={item}
          uniqueKey={uniqueKey}
          isThinkingCollapsed={isThinkingCollapsed}
          onToggleThinkingCollapse={toggleThinkingCollapse}
        />
      ),
      [isThinkingCollapsed, toggleThinkingCollapse],
    );

    const handleSkillDeposit = useCallback(() => {
      clearCiteMessages();
      conversation.sendMessage({
        text: t("chat.skillDepositPrompt"),
        clearInput: true,
        create_time: new Date().toISOString(),
      });
    }, [clearCiteMessages, conversation, t]);

    return (
      <div className="chat-chat-container">
        <div className="chat-box">
          <MessageList
            messageList={conversation.messageList}
            initialCard={initialCard}
            sendMessage={(text, clearInput, extras) => {
              conversation.sendMessage({ text, clearInput, ...(extras ?? {}) });
            }}
            regenerate={conversation.regenerate}
            stopGeneration={conversation.stopGeneration}
            renderText={renderText}
            updateAssistantMessage={conversation.updateAssistantMessage}
            onCiteMessage={handleAddCiteMessage}
            onScroll={conversation.scroll.handleScroll}
            chatContentRef={conversation.scroll.chatContentRef}
            sessionId={sessionId}
            editingUserMessageIndex={userEdit.editingUserMessageIndex}
            editingUserMessageText={userEdit.editingUserMessageText}
            editingUserMessageCites={userEdit.editingUserMessageCites}
            onUserMessageEditTextChange={userEdit.setEditingUserMessageText}
            onRemoveEditingUserMessageCite={
              userEdit.handleRemoveEditingUserMessageCite
            }
            onStartEditUserMessage={userEdit.handleStartEditUserMessage}
            onCancelEditUserMessage={userEdit.handleCancelEditUserMessage}
            onResendEditedUserMessage={userEdit.handleResendEditedUserMessage}
            onCopyUserMessage={userEdit.handleCopyUserMessage}
          />

          {conversation.messageList.length > 0 && (
            <ScrollToBottomButton
              visible={conversation.scroll.showScrollButton}
              inputHeight={conversation.scroll.inputHeight}
              onClick={conversation.scroll.handleToBottom}
            />
          )}

          <ChatInput
            value={conversation.content}
            onChange={conversation.setContent}
            onSend={conversation.sendMessage}
            openHistory={
              setShowHistoryList ? () => setShowHistoryList(true) : undefined
            }
            isChatContent={true}
            showHistoryList={showHistoryList}
            showHistoryButton={showHistoryButton}
            showPromptSuggestions={false}
            openNewChat={conversation.createNewChat}
            ref={chatInputRef}
            onHeightChange={conversation.scroll.handleInputHeightChange}
            chatConfig={chatConfig}
            setChatConfig={setChatConfig}
            setChatConfigFn={setChatConfigFn}
            knowledgeRefreshKey={knowledgeRefreshKey}
            embeddingReady={embeddingReady}
            multimodalEmbeddingReady={multimodalEmbeddingReady}
            rerankReady={rerankReady}
            sessionId={sessionId}
            isStreaming={conversation.isStreaming}
            onStopGeneration={conversation.stopGeneration}
            disabled={!canChat}
            disabledReason={disabledReason}
            disabledDescription={disabledDescription}
            disabledAction={disabledAction}
            citeMessages={citeMessages}
            onRemoveCiteMessage={handleRemoveCiteMessage}
            onClearCiteMessage={clearCiteMessages}
            skillDepositStats={skillDepositStats}
            skillDepositDisabledReason={
              isLastUserMessageSkillDepositPrompt
                ? t("chat.skillDepositAlreadyRequestedTooltip")
                : undefined
            }
            onSkillDeposit={handleSkillDeposit}
            onPluginSettingsChange={onPluginSettingsChange}
            initialPluginSettings={initialPluginSettings}
            hasPluginSession={hasPluginSession}
          />
        </div>
      </div>
    );
  },
);

ChatContainerComponent.displayName = "ChatContainerComponent";

export default ChatContainerComponent;
