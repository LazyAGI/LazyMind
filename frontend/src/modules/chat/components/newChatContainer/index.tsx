import {
  forwardRef,
  useCallback,
  useImperativeHandle,
  useRef,
} from "react";
import { useTranslation } from "react-i18next";
import { useChatMessageStore } from "@/modules/chat/store/chatMessage";
import ChatInput, { ChatInputImperativeProps } from "../ChatInput";
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

    useImperativeHandle(ref, () => ({
      replaceMessageList: conversation.replaceMessageList,
      createNewChat: conversation.createNewChat,
      sendMessage: conversation.sendMessage,
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
