import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import i18n from "@/i18n";
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
import { ChatSourcePanel } from "../AssistantMessage";
import ChatMessageContent from "./components/ChatMessageContent";
import ScrollToBottomButton from "./components/ScrollToBottomButton";
import ConversationTrail from "./components/ConversationTrail";
import StreamRecoveryBanner from "./components/StreamRecoveryBanner";
import { useChatConversation } from "./hooks/useChatConversation";
import { useCiteMessagesInput } from "./hooks/useCiteMessagesInput";
import { useThinkingCollapse } from "./hooks/useThinkingCollapse";
import { useUserMessageEdit } from "./hooks/useUserMessageEdit";
import type { ChatContainerProps, ChatImperativeProps } from "./types";
import { useConversationTrail } from "./hooks/useConversationTrail";
import { mergeConversationTrailIntoMessageList } from "@/modules/chat/utils/message";
import type { ChatSource } from "@/modules/chat/utils/sourceAdapter";

export type { ChatImperativeProps, ChatMessage } from "./types";

const SKILL_DEPOSIT_REMINDER_KEY_PREFIX = "skill-deposit-reminded:";
const SKILL_DEPOSIT_PROMPT_PREFIXES = ["zh-CN", "en-US"].map((language) =>
  String(
    i18n.getResource(language, "translation", "chat.skillDepositPrompt") ?? "",
  ).split("\n")[0],
);

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
      onConversationSettingsChange,
      initialConversationSettings,
      hasWorkflowSession,
      conversationTrailEnabled = true,
      showThinkingDepth = true,
      showSkillDeposit = true,
      showConversationConfig = true,
      fixedThinkingDepth,
    } = props;

    const { clearPendingMessage: clearStorePendingMessage } =
      useChatMessageStore();
    const chatInputRef = useRef<ChatInputImperativeProps>(null);
    const userEditRef = useRef<ReturnType<typeof useUserMessageEdit>>();
    const [sourcePanelSources, setSourcePanelSources] = useState<ChatSource[]>([]);
    const skillDepositWasReadyRef = useRef(false);
    const skillDepositMessageCountRef = useRef(0);

    useEffect(() => {
      setSourcePanelSources([]);
    }, [sessionId]);

    const {
      thinkingCollapseMap,
      toggleThinkingCollapse,
      isThinkingCollapsed,
      collapseAllThinking,
    } = useThinkingCollapse();

    const {
      citeMessages,
      citeHistoryIds,
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
      setIsChatContent,
      clearStorePendingMessage,
      clearCiteMessages,
      chatInputRef,
      thinkingCollapseMap,
      getUserEdit: () => userEditRef.current,
      t,
    });

    const trailRefreshKey = `${conversation.messageList.length}:${conversation.isStreaming ? "streaming" : "idle"}`;
    const conversationTrail = useConversationTrail({
      conversationId: sessionId,
      refreshKey: trailRefreshKey,
      enabled: Boolean(sessionId) && conversationTrailEnabled,
    });
    useEffect(() => {
      if (conversationTrail.items.length === 0) {
        return;
      }
      const merged = mergeConversationTrailIntoMessageList(
        conversation.messageList,
        conversationTrail.items,
      );
      if (merged === conversation.messageList) {
        return;
      }
      conversation.messageListRef.current = merged;
      conversation.setMessageList(merged);
      const currentId = conversation.currentConversationIdRef.current;
      if (currentId) {
        conversation.conversationMessagesCache.current.set(currentId, merged);
      }
    }, [
      conversation.messageList,
      conversationTrail.items,
    ]);

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

    const sendMessage = useCallback(
      (params: Parameters<typeof conversation.sendMessage>[0]) => {
        collapseAllThinking();
        conversation.sendMessage(params);
      },
      [collapseAllThinking, conversation.sendMessage],
    );

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
          (lastAssistantMessage.run_status ||
            lastAssistantMessage.finish_reason !==
              ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified),
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
      sendMessage,
      prepareMessage: ({
        text,
        citeMessage,
        citeMessages: nextCiteMessages,
        appendCitations = false,
      }) => {
        conversation.setContent(text);
        if (!appendCitations) {
          clearCiteMessages();
        }
        const citations = nextCiteMessages ?? (citeMessage ? [citeMessage] : []);
        citations.forEach((citation) => handleAddCiteMessage(citation));
        requestAnimationFrame(() => chatInputRef.current?.focus());
      },
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
          conversationId={conversation.currentConversationIdRef.current || sessionId}
          onCiteMessage={handleAddCiteMessage}
          isThinkingCollapsed={isThinkingCollapsed}
          onToggleThinkingCollapse={toggleThinkingCollapse}
        />
      ),
      [conversation, handleAddCiteMessage, isThinkingCollapsed, sessionId, toggleThinkingCollapse],
    );

    const handleSkillDeposit = useCallback(() => {
      clearCiteMessages();
      sendMessage({
        text: t("chat.skillDepositPrompt"),
        clearInput: true,
        create_time: new Date().toISOString(),
      });
    }, [clearCiteMessages, sendMessage, t]);

    return (
      <div className="chat-chat-container">
        <div className={`chat-box${sourcePanelSources.length ? " has-source-panel" : ""}`}>
          <div className="chat-main-column">
            <MessageList
              messageList={conversation.messageList}
              initialCard={initialCard}
              sendMessage={(text, clearInput, extras) => {
                sendMessage({ text, clearInput, ...(extras ?? {}) });
              }}
              regenerate={conversation.regenerate}
              regenerateDisabled={
                !canChat ||
                conversation.loading ||
                conversation.isStreaming ||
                conversation.runtimeWaiting
              }
              stopGeneration={conversation.stopGeneration}
              renderText={renderText}
              updateAssistantMessage={conversation.updateAssistantMessage}
              onCiteMessage={handleAddCiteMessage}
              onOpenSources={setSourcePanelSources}
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

            <StreamRecoveryBanner
              recovery={conversation.streamRecovery}
              onReconnect={conversation.retryStreamRecovery}
            />

            <ChatInput
              value={conversation.content}
              onChange={conversation.setContent}
              onSend={sendMessage}
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
              disabled={!canChat || conversation.runtimeWaiting}
              disabledReason={canChat ? undefined : disabledReason}
              disabledDescription={canChat ? undefined : disabledDescription}
              disabledAction={canChat ? undefined : disabledAction}
              citeMessages={citeMessages}
              citeHistoryIds={citeHistoryIds}
              onRemoveCiteMessage={handleRemoveCiteMessage}
              onClearCiteMessage={clearCiteMessages}
              skillDepositStats={skillDepositStats}
              skillDepositDisabledReason={
                isLastUserMessageSkillDepositPrompt
                  ? t("chat.skillDepositAlreadyRequestedTooltip")
                  : undefined
              }
              onSkillDeposit={handleSkillDeposit}
              onConversationSettingsChange={onConversationSettingsChange}
              initialConversationSettings={initialConversationSettings}
              hasWorkflowSession={hasWorkflowSession}
              showThinkingDepth={showThinkingDepth}
              showSkillDeposit={showSkillDeposit}
              showConversationConfig={showConversationConfig}
              fixedThinkingDepth={fixedThinkingDepth}
            />
          </div>
          {sourcePanelSources.length > 0 && (
            <ChatSourcePanel
              sources={sourcePanelSources}
              onClose={() => setSourcePanelSources([])}
            />
          )}
        </div>
        <ConversationTrail
          key={sessionId || "new-conversation"}
          items={conversationTrail.items}
          scrollContainerRef={conversation.scroll.chatContentRef}
          messageListLength={conversation.messageList.length}
          loading={conversationTrail.loading}
          error={conversationTrail.error}
          onRetry={conversationTrail.retry}
        />
      </div>
    );
  },
);

ChatContainerComponent.displayName = "ChatContainerComponent";

export default ChatContainerComponent;
