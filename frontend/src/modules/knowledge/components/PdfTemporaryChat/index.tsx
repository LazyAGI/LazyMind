import { useCallback, useEffect, useRef, useState } from "react";
import { Button, message } from "antd";
import { CloseOutlined, MessageOutlined, PlusOutlined, SaveOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { ChatConversationsRequestActionEnum, type Query } from "@/api/generated/chatbot-client";
import { AgentAppsAuth } from "@/components/auth";
import ChatContainerComponent, {
  type ChatImperativeProps,
} from "@/modules/chat/components/newChatContainer";
import { Method, SSE } from "@/modules/chat/utils/sse";
import {
  CHAT_RESUME_STREAM_URL,
  CHAT_STREAM_URL,
  ChatServiceApi,
} from "@/modules/chat/utils/request";
import { axiosInstance, BASE_URL } from "@/components/request";
import { emitConversationActivity } from "@/modules/chat/utils/conversationActivity";
import { buildChatMessageListFromHistory } from "@/modules/chat/utils/message";
import "./index.scss";
import type { DocumentChatSelection } from "./types";

interface PdfTemporaryChatProps {
  datasetId: string;
  documentId: string;
  fileName: string;
  selection?: DocumentChatSelection;
  conversationToLoad?: string;
  onConversationChange?: (conversationId?: string) => void;
  onHistoryChange?: () => void;
  onClose: () => void;
}

function newPreviewConversationId() {
  const suffix = typeof crypto.randomUUID === "function"
    ? crypto.randomUUID().replaceAll("-", "")
    : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  return `pdf-${suffix}`.slice(0, 36);
}

export default function PdfTemporaryChat({
  datasetId,
  documentId,
  fileName,
  selection,
  conversationToLoad,
  onConversationChange,
  onHistoryChange,
  onClose,
}: PdfTemporaryChatProps) {
  const { t } = useTranslation();
  const chatRef = useRef<ChatImperativeProps>(null);
  const initialConversationIdRef = useRef(newPreviewConversationId());
  const conversationIdRef = useRef(initialConversationIdRef.current);
  const preparedSelectionRef = useRef("");
  const [conversationId, setConversationId] = useState(initialConversationIdRef.current);
  const [conversationCreated, setConversationCreated] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [restartKey, setRestartKey] = useState(0);
  const [chatConfig, setChatConfig] = useState({ knowledgeBaseId: [datasetId] });

  useEffect(() => {
    conversationIdRef.current = conversationId;
  }, [conversationId]);

  useEffect(() => {
    if (!conversationToLoad || !chatRef.current) return;
    let cancelled = false;
    Promise.all([
      ChatServiceApi().conversationServiceGetConversationHistory({ name: conversationToLoad }),
      ChatServiceApi().conversationServiceGetChatStatus({ conversationId: conversationToLoad }),
    ]).then(([historyResponse, statusResponse]) => {
      if (cancelled) return;
      const list = buildChatMessageListFromHistory(historyResponse.data.history || [], {
        isGenerating: Boolean(statusResponse.data?.is_generating),
      });
      setConversationId(conversationToLoad);
      conversationIdRef.current = conversationToLoad;
      setConversationCreated(true);
      setSaved(false);
      preparedSelectionRef.current = "";
      chatRef.current?.replaceMessageList(conversationToLoad, list);
      if (statusResponse.data?.is_generating) {
        chatRef.current?.openResumeSSE?.(conversationToLoad);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [conversationToLoad]);

  useEffect(() => {
    if (!selection) return;
    const selectionKey = `${selection.source}:${selection.page}:${selection.segmentId}:${selection.text}:${selection.bbox?.join(",") || ""}`;
    if (preparedSelectionRef.current === selectionKey) return;
    let attempts = 0;
    const sendWhenReady = () => {
      if (!chatRef.current && attempts < 10) {
        attempts += 1;
        timer = window.setTimeout(sendWhenReady, 50);
        return;
      }
      if (!chatRef.current) return;
      preparedSelectionRef.current = selectionKey;
      chatRef.current.prepareMessage({
        text: "",
        citeMessage: selection.text,
        appendCitations: true,
      });
    };
    let timer = window.setTimeout(sendWhenReady, 50);
    return () => window.clearTimeout(timer);
  }, [restartKey, selection]);

  const openSSE = (
    input: Query[],
    action: ChatConversationsRequestActionEnum,
    callbacks: Record<string, (event: CustomEvent) => void>,
    extras?: Record<string, unknown>,
  ) => new SSE(CHAT_STREAM_URL, {
    method: Method.POST,
    headers: {
      "Content-Type": "application/json",
      Accept: "text/event-stream",
      ...AgentAppsAuth.getAuthHeaders(),
    },
    timeout: 1800000,
    payload: JSON.stringify({
      action,
      conversation_id: conversationId,
      conversation: {
        display_name: t("knowledge.pdfChatTitle", { fileName }),
        search_config: { dataset_list: [{ id: datasetId }] },
      },
      models: [t("chat.lazyMindModel")],
      stream: true,
      input,
      ...extras,
      thinking_depth: "low",
      mode: "auto",
      basic_chat_only: true,
      create_time: new Date().toISOString(),
      initial_conversation_settings: {
        enable_workflow: false,
        enable_subagent: false,
        ephemeral: true,
        persistent_ephemeral: true,
        source_type: "pdf_preview",
        source_dataset_id: datasetId,
        source_document_id: documentId,
        source_display_name: fileName,
      },
      document_context: {
        dataset_id: datasetId,
        document_id: documentId,
        file_name: fileName,
        page: selection?.page,
        bbox: selection?.bbox,
        segment_id: selection?.segmentId,
        segment_number: selection?.segmentNumber,
        segment_group: selection?.group,
      },
    }),
    callbacks,
  });

  const openResumeSSE = (
    id: string,
    callbacks: Record<string, (event: CustomEvent) => void>,
    cursor?: { historyId?: string; afterSequence?: number },
  ) => new SSE(CHAT_RESUME_STREAM_URL, {
    method: Method.POST,
    headers: {
      "Content-Type": "application/json",
      Accept: "text/event-stream",
      ...AgentAppsAuth.getAuthHeaders(),
    },
    timeout: 1800000,
    payload: JSON.stringify({
      conversation_id: id,
      history_id: cursor?.historyId,
      after_sequence: cursor?.afterSequence,
    }),
    callbacks,
  });

  const startNewConversation = () => {
    chatRef.current?.createNewChat();
    const nextId = newPreviewConversationId();
    setConversationId(nextId);
    conversationIdRef.current = nextId;
    setConversationCreated(false);
    setSaved(false);
    preparedSelectionRef.current = "";
    setRestartKey((key) => key + 1);
    onConversationChange?.(undefined);
  };

  const saveConversation = async () => {
    const id = conversationIdRef.current;
    if (!id || id.startsWith("temp_")) return;
    setSaving(true);
    try {
      await axiosInstance.post(
        `${BASE_URL}/api/core/conversations/${encodeURIComponent(id)}:promote`,
      );
      setSaved(true);
      emitConversationActivity({ conversationId: id });
      message.success(t("knowledge.pdfChatSaved"));
      onHistoryChange?.();
    } finally {
      setSaving(false);
    }
  };

  return (
    <aside className="pdf-temporary-chat" aria-label={t("knowledge.pdfChatPanelLabel")}>
      <header className="pdf-temporary-chat__header">
        <div>
          <MessageOutlined />
          <strong>{t("knowledge.pdfChatPanelLabel")}</strong>
          <span>{t("knowledge.pdfChatTemporaryHint")}</span>
        </div>
        <Button type="text" icon={<CloseOutlined />} aria-label={t("common.close")} onClick={onClose} />
      </header>
      <div className="pdf-temporary-chat__actions">
        <Button size="small" icon={<PlusOutlined />} onClick={() => void startNewConversation()}>
          {t("knowledge.pdfChatNew")}
        </Button>
        <Button
          size="small"
          type="primary"
          icon={<SaveOutlined />}
          loading={saving}
          disabled={!conversationCreated || saved}
          onClick={() => void saveConversation()}
        >
          {t("knowledge.pdfChatSave")}
        </Button>
      </div>
      <div className="pdf-temporary-chat__body">
        <ChatContainerComponent
          ref={chatRef}
          sessionId={conversationId}
          onOpenSSE={openSSE}
          onOpenResumeSSE={openResumeSSE}
          onConversationIdChange={(id) => {
            if (!id) return;
            setConversationId(id);
            setConversationCreated(true);
            onHistoryChange?.();
          }}
          parseErrorData={(data) => data}
          setIsChatContent={() => {}}
          showHistoryButton={false}
          chatConfig={chatConfig}
          setChatConfig={setChatConfig}
          setChatConfigFn={setChatConfig}
          initialConversationSettings={{ enable_workflow: false, enable_subagent: false }}
          conversationTrailEnabled={conversationCreated}
          showThinkingDepth={false}
          showSkillDeposit={false}
          showConversationConfig={false}
          fixedThinkingDepth="low"
        />
      </div>
    </aside>
  );
}
