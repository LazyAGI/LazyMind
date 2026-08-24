import { useCallback, useEffect, useRef, useState } from "react";
import { Button, message } from "antd";
import { CloseOutlined, MessageOutlined, PlusOutlined, SaveOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { ChatConversationsRequestActionEnum, type Query } from "@/api/generated/chatbot-client";
import { AgentAppsAuth } from "@/components/auth";
import ChatContainerComponent, {
  type ChatImperativeProps,
} from "@/modules/chat/components/newChatContainer";
import { CHAT_RESUME_CONVERSATION_KEY } from "@/modules/chat/constants/chat";
import { Method, SSE } from "@/modules/chat/utils/sse";
import {
  CHAT_RESUME_STREAM_URL,
  CHAT_STREAM_URL,
  ChatServiceApi,
} from "@/modules/chat/utils/request";
import { axiosInstance, BASE_URL } from "@/components/request";
import { emitConversationActivity } from "@/modules/chat/utils/conversationActivity";
import "./index.scss";
import type { DocumentChatSelection } from "./types";

interface PdfTemporaryChatProps {
  datasetId: string;
  documentId: string;
  fileName: string;
  selection?: DocumentChatSelection;
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
  onClose,
}: PdfTemporaryChatProps) {
  const { t } = useTranslation();
  const chatRef = useRef<ChatImperativeProps>(null);
  const initialConversationIdRef = useRef(newPreviewConversationId());
  const conversationIdRef = useRef(initialConversationIdRef.current);
  const savedRef = useRef(false);
  const preparedSelectionRef = useRef("");
  const [conversationId, setConversationId] = useState(initialConversationIdRef.current);
  const [conversationCreated, setConversationCreated] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [restartKey, setRestartKey] = useState(0);
  const [chatConfig, setChatConfig] = useState({ knowledgeBaseId: [datasetId] });

  const discardConversation = useCallback(async (id: string) => {
    if (!id || id.startsWith("temp_")) return;
    try {
      await ChatServiceApi().conversationServiceDeleteConversation({ conversation: id });
    } catch {
      // A failed cleanup must not block closing the document preview.
    }
  }, []);

  useEffect(() => {
    conversationIdRef.current = conversationId;
  }, [conversationId]);

  useEffect(() => () => {
    sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
    if (!savedRef.current) {
      void discardConversation(conversationIdRef.current);
    }
  }, [discardConversation]);

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
      });
    };
    let timer = window.setTimeout(sendWhenReady, 50);
    return () => window.clearTimeout(timer);
  }, [restartKey, selection]);

  const openSSE = (
    input: Query[],
    action: ChatConversationsRequestActionEnum,
    callbacks: Record<string, (event: CustomEvent) => void>,
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
      mode: "auto",
      basic_chat_only: true,
      create_time: new Date().toISOString(),
      initial_conversation_settings: {
        enable_workflow: false,
        enable_subagent: false,
        ephemeral: true,
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

  const startNewConversation = async () => {
    const previous = conversationIdRef.current;
    const previousWasSaved = savedRef.current;
    chatRef.current?.createNewChat();
    const nextId = newPreviewConversationId();
    setConversationId(nextId);
    conversationIdRef.current = nextId;
    setConversationCreated(false);
    savedRef.current = false;
    setSaved(false);
    preparedSelectionRef.current = "";
    setRestartKey((key) => key + 1);
    if (!previousWasSaved) {
      await discardConversation(previous);
    }
  };

  const saveConversation = async () => {
    const id = conversationIdRef.current;
    if (!id || id.startsWith("temp_")) return;
    setSaving(true);
    try {
      await axiosInstance.post(
        `${BASE_URL}/api/core/conversations/${encodeURIComponent(id)}:promote`,
      );
      savedRef.current = true;
      setSaved(true);
      emitConversationActivity({ conversationId: id });
      message.success(t("knowledge.pdfChatSaved"));
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
            setConversationId(id);
            setConversationCreated(true);
          }}
          parseErrorData={(data) => data}
          setIsChatContent={() => {}}
          showHistoryButton={false}
          chatConfig={chatConfig}
          setChatConfig={setChatConfig}
          setChatConfigFn={setChatConfig}
          initialConversationSettings={{ enable_workflow: false, enable_subagent: false }}
          conversationTrailEnabled={conversationCreated}
        />
      </div>
    </aside>
  );
}
