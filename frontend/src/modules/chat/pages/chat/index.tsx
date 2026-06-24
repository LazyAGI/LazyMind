import { FC, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { AgentAppsAuth } from "@/components/auth";
import {
  ChatConversationsRequestActionEnum,
  Conversation,
  Query,
} from "@/api/generated/chatbot-client";

import ChatContainerComponent, {
  ChatImperativeProps,
} from "@/modules/chat/components/ChatContainer";
import "./index.scss";
import RecordList, {
  RecordListImperativeProps,
} from "@/modules/chat/components/RecordList";
import UIUtils from "@/modules/chat/utils/ui";
import InitialCard from "@/modules/chat/components/InitialCard";
import ChatConfigs, { ChatConfig } from "@/modules/chat/components/ChatConfigs";
import { Method, SSE } from "@/modules/chat/utils/sse";
import { CHAT_STREAM_URL, ChatServiceApi } from "@/modules/chat/utils/request";
import { buildChatMessageListFromHistory } from "@/modules/chat/utils/message";
import { buildEnvironmentContext } from "@/modules/chat/utils/environment";

const ChatPage: FC = () => {
  const { t } = useTranslation();
  const [sessionId, setSessionId] = useState("");
  const [chatConfig, setChatConfig] = useState<ChatConfig>();

  const chatRef = useRef<ChatImperativeProps>(null);
  const recordListRef = useRef<RecordListImperativeProps>(null);
  const previousSessionIdRef = useRef<string>("");

  function onOpenSSE(
    input: Query[],
    action: ChatConversationsRequestActionEnum,
    callbacks: Record<string, (e: CustomEvent) => void>,
  ) {
    const hasUploadedFiles = input?.some(
      (q: Query) => q.input_type === "image" || q.input_type === "file",
    );
    const datasetList = hasUploadedFiles
      ? []
      : chatConfig?.knowledgeBaseId?.map((id) => ({ id })) || [];

    return new SSE(CHAT_STREAM_URL, {
      method: Method.POST,
      headers: {
        "Content-Type": "application/json",
        Accept: "text/event-stream",
        ...AgentAppsAuth.getAuthHeaders(),
      },
      timeout: 1800000,
      payload: JSON.stringify({
        action,
        conversation_id: sessionId,
        conversation: {
          search_config: {
            dataset_list: datasetList,
            database_ids: [chatConfig?.databaseBaseId]?.filter((id) => !!id),
            creators: chatConfig?.creators,
            tags: chatConfig?.tags,
          },
        },
        models: ["LazyMind"],
        stream: true,
        input,
        environment_context: buildEnvironmentContext(),
      }),
      callbacks,
    });
  }

  function setConversationId(id: string) {
    if (id === sessionId) {
      return;
    }
    setSessionId(id);
  }

  useEffect(() => {
    if (
      sessionId === "" &&
      previousSessionIdRef.current !== "" &&
      recordListRef.current
    ) {
      recordListRef.current.refresh();
    }
    previousSessionIdRef.current = sessionId;
  }, [sessionId]);

  function onRecordSelected(data: Conversation) {
    ChatServiceApi()
      .conversationServiceGetConversationDetail({
        conversation: data.conversation_id || "",
      })
      .then((detailRes) =>
        ChatServiceApi()
          .conversationServiceGetConversationHistory({
            name: data.conversation_id || "",
          })
          .then((historyRes) => ({ detailRes, historyRes })),
      )
      .then(({ detailRes, historyRes }) => {
        // Reset configs.
        const conversation = detailRes.data.conversation;
        setChatConfig({
          knowledgeBaseId: conversation?.search_config?.dataset_list
            .map((dataset) => dataset.id)
            .filter((id) => !!id),
          creators: conversation?.search_config?.creators,
          tags: conversation?.search_config?.tags,
          databaseBaseId: conversation?.search_config?.database_ids?.[0],
        });

        // Reset messages.
        const history = historyRes.data.history;
        const list = buildChatMessageListFromHistory(history, {
          fallbackCreateTime: "xxx-xxx-xxx",
          stripCitations: false,
        });
        chatRef.current?.replaceMessageList(
          conversation?.conversation_id || "",
          list,
        );
      });
  }

  function deleteHistory(data: Conversation) {
    if (data.conversation_id === sessionId) {
      chatRef.current?.createNewChat();
    }
  }

  function parseErrorData(data: string) {
    const dataObject = UIUtils.jsonParser(data) || {};
    return dataObject.message;
  }

  function onChatConfigChanged(config: ChatConfig) {
    setChatConfig((prev) => {
      const updated: ChatConfig = { ...prev };

      (Object.keys(config) as Array<keyof ChatConfig>).forEach((key) => {
        updated[key] = config[key] as any;
      });
      return updated;
    });
  }

  function generateNewConversationId(): string {
    if (typeof crypto !== "undefined" && crypto.randomUUID) {
      return crypto.randomUUID();
    }
    return `conv_${Date.now()}_${Math.random().toString(36).substring(2, 15)}`;
  }

  function handleCreateNewChat() {
    const newConversationId = generateNewConversationId();
    chatRef.current?.replaceMessageList(newConversationId, []);
    setSessionId(newConversationId);
  }

  function handleNewConversationCreated(_conversationId: string) {
    if (recordListRef.current) {
      recordListRef.current.refresh();
    }
  }

  return (
    <div className="detail-container">
      <div className="left-box">
        <div className="title">{t("chat.sidebarConfigTitle")}</div>
        <ChatConfigs
          configs={chatConfig || {}}
          onChange={onChatConfigChanged}
        />
        <RecordList
          ref={recordListRef}
          currentSessionId={sessionId}
          onSelected={onRecordSelected}
          onRemove={deleteHistory}
        />
      </div>
      <ChatContainerComponent
        ref={chatRef}
        initialCard={<InitialCard />}
        onOpenSSE={onOpenSSE}
        onConversationIdChange={setConversationId}
        parseErrorData={parseErrorData}
        onCreateNewChat={handleCreateNewChat}
        onNewConversationCreated={handleNewConversationCreated}
      />
    </div>
  );
};

export default ChatPage;
