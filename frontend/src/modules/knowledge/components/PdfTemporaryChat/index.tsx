import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import { Alert, Button, message } from "antd";
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
import { axiosInstance, BASE_URL, getLocalizedErrorMessage } from "@/components/request";
import { emitConversationActivity } from "@/modules/chat/utils/conversationActivity";
import { buildChatMessageListFromHistory } from "@/modules/chat/utils/message";
import "./index.scss";
import type { DocumentChatSelection } from "./types";
import type { ChatConfig } from "@/modules/chat/components/ChatConfigs";
import type { DocumentTranslationRequest } from "./types";
import { touchCachedPdfChat } from "./cache";
import { ensureDocumentParsed, retryDocumentParsed, type DocumentParseResult } from "@/modules/knowledge/api/pdfArtifacts";

interface PdfTemporaryChatProps {
  datasetId: string;
  documentId: string;
  fileName: string;
  selection?: DocumentChatSelection;
  conversationToLoad?: string;
  translationRequest?: DocumentTranslationRequest | null;
  onConversationChange?: (conversationId?: string) => void;
  onHistoryChange?: () => void;
  onClose: () => void;
  onClearSelection?: () => void;
  onNavigateSelection?: (selection: DocumentChatSelection) => void;
}

export interface PdfTemporaryChatHandle {
  startNewConversation: () => void;
}

function newPreviewConversationId() {
  const suffix = typeof crypto.randomUUID === "function"
    ? crypto.randomUUID().replace(/-/g, "")
    : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  return `pdf-${suffix}`.slice(0, 36);
}

const PdfTemporaryChat = forwardRef<PdfTemporaryChatHandle, PdfTemporaryChatProps>(function PdfTemporaryChat({
  datasetId,
  documentId,
  fileName,
  selection,
  conversationToLoad,
  translationRequest,
  onConversationChange,
  onHistoryChange,
  onClose,
  onClearSelection,
  onNavigateSelection,
}: PdfTemporaryChatProps, ref) {
  const { t, i18n } = useTranslation();
  const zh = (i18n?.resolvedLanguage || i18n?.language || "en").startsWith("zh");
  const copy = zh ? {
    assistant:"论文助手", hint:"细读论文，随时提问。", ready:"论文已就绪", scope:"回答基于当前文档", quote:"原文摘录", page:"第", remove:"移除原文摘录", companion:"阅读助手", welcome:"读懂这篇论文", intro:"探索核心观点、核对证据，或选中原文提出具体问题。",
    prompts:[
      ["核心贡献", "理解创新点及其意义", "请解释这篇论文的核心贡献，以及它与已有工作的区别。请引用原文作为依据。"],
      ["解释方法", "逐步理解研究方法", "请逐步解释这篇论文的方法、关键假设，并引用相关章节。"],
      ["关键结果", "核对研究证据", "请总结这篇论文的关键结果，包括评估设置和支持结论的证据。"],
      ["局限性", "了解尚未解决的问题", "这篇论文有哪些局限和开放问题？请区分论文明确陈述的局限与推测。"],
    ],
  } : {
    assistant:"Paper assistant", hint:"Read closely. Ask freely.", ready:"Paper ready", scope:"Answers grounded in this document", quote:"Source quote", page:"Page", remove:"Remove source quote", companion:"Your reading companion", welcome:"Make sense of this paper", intro:"Explore its ideas, follow the evidence, or select a passage to ask a focused question.",
    prompts:[
      ["Core contribution", "What is new, and why it matters", "Explain the core contribution of this paper and how it differs from prior work. Cite supporting passages."],
      ["Explain the method", "Walk through the approach", "Walk me through the method in this paper step by step. Explain the key assumptions and cite the relevant sections."],
      ["Key results", "Understand the evidence", "Summarize the key results of this paper, including the evaluation setup and evidence supporting its claims."],
      ["Limitations", "Look beyond the headline", "What are the limitations and open questions in this paper? Distinguish stated limitations from your own inferences."],
    ],
  };
  const isPaper = /\.pdf$/i.test(fileName);
  const [dismissedSelection, setDismissedSelection] = useState<DocumentChatSelection>();
  const activeSelection = selection !== dismissedSelection ? selection : undefined;
  const chatRef = useRef<ChatImperativeProps>(null);
  const initialConversationIdRef = useRef(newPreviewConversationId());
  const conversationIdRef = useRef(initialConversationIdRef.current);
  const preparedSelectionRef = useRef<DocumentChatSelection>();
  const handledTranslationRequestRef = useRef(0);
  const pendingTranslationRef = useRef<(DocumentTranslationRequest & { conversationId: string }) | null>(null);
  const [conversationId, setConversationId] = useState(initialConversationIdRef.current);
  const [conversationCreated, setConversationCreated] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [restartKey, setRestartKey] = useState(0);
  const [chatConfig, setChatConfig] = useState<ChatConfig>({ knowledgeBaseId: [datasetId] });
  const [parseState, setParseState] = useState<DocumentParseResult["status"] | "loading">("loading");
  const [parseError, setParseError] = useState("");
  const [parseAttempt, setParseAttempt] = useState(0);
  const [retrying, setRetrying] = useState(false);

  useEffect(() => {
    // Opening document chat is the first concrete signal that the stored file
    // needs readable text. Parsing is idempotent, so eagerly start it while the
    // user is composing the first question instead of waiting for retrieval to
    // discover that no chunks exist.
    let cancelled = false;
    setParseState("loading");
    setParseError("");
    let timer: number | undefined;
    const poll = async () => {
      try {
        const result = await ensureDocumentParsed(datasetId, documentId);
        if (cancelled) return;
        setParseState(result.status);
        setParseError(result.error?.message || (result.status === "failed" ? t("knowledge.pdfChatParseReasonUnknown") : ""));
        if (result.status === "parsing") timer = window.setTimeout(poll, 1000);
      } catch (error) {
        if (cancelled) return;
        setParseState("unknown");
        setParseError(getLocalizedErrorMessage(error));
      }
    };
    void poll();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [datasetId, documentId, parseAttempt]);

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
      touchCachedPdfChat(documentId, conversationToLoad);
      setSaved(false);
      preparedSelectionRef.current = undefined;
      chatRef.current?.replaceMessageList(conversationToLoad, list);
      if (statusResponse.data?.is_generating) {
        chatRef.current?.openResumeSSE?.(conversationToLoad);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [conversationToLoad, documentId]);

  useEffect(() => {
    if (!activeSelection) return;
    if (preparedSelectionRef.current === activeSelection) return;
    let attempts = 0;
    const sendWhenReady = () => {
      if (preparedSelectionRef.current === activeSelection) return;
      if (!chatRef.current && attempts < 10) {
        attempts += 1;
        timer = window.setTimeout(sendWhenReady, 50);
        return;
      }
      if (!chatRef.current) return;
      preparedSelectionRef.current = activeSelection;
      chatRef.current.prepareMessage({
        ...(activeSelection.question?.trim() ? {text: activeSelection.question.trim()} : {}),
        citeMessage: activeSelection.text,
        appendCitations: true,
      });
    };
    let timer = window.setTimeout(sendWhenReady, 50);
    return () => window.clearTimeout(timer);
  }, [restartKey, selection, activeSelection]);

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
      ...extras,
      action,
      conversation_id: conversationId,
      conversation: {
        display_name: t("knowledge.pdfChatTitle", { fileName }),
        search_config: { dataset_list: [{ id: datasetId }] },
      },
      models: [t("chat.lazyMindModel")],
      surface: "knowledge_document_preview",
      stream: true,
      input,
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
        page: activeSelection?.page,
        bbox: activeSelection?.bbox,
        segment_id: activeSelection?.segmentId,
        segment_number: activeSelection?.segmentNumber,
        segment_group: activeSelection?.group,
        selected_text: activeSelection?.text,
        paragraph_text: activeSelection?.context,
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
    preparedSelectionRef.current = undefined;
    setRestartKey((key) => key + 1);
    onConversationChange?.(undefined);
    return nextId;
  };

  useImperativeHandle(ref, () => ({ startNewConversation }));

  useEffect(() => {
    if (!translationRequest || handledTranslationRequestRef.current === translationRequest.id) return;
    handledTranslationRequestRef.current = translationRequest.id;
    const nextId = startNewConversation();
    pendingTranslationRef.current = { ...translationRequest, conversationId: nextId };
  // startNewConversation intentionally creates a fresh chat for each explicit request.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [translationRequest]);

  useEffect(() => {
    const request = pendingTranslationRef.current;
    if (!request || request.conversationId !== conversationId) return;
    pendingTranslationRef.current = null;
    const timer = window.setTimeout(async () => {
      const sent = await chatRef.current?.sendMessage({
        text: "请结合引用内容所在句子完成翻译。若引用是单词或短语，先给出其在当前语境下最合适的中文释义，再用一句话解释所在句子的含义；若引用是完整句子或段落，直接给出准确、自然、简洁的中文译文。不要搜索文档，不要复述任务，不要添加无关背景。",
        citeMessage: request.selection.text,
        clearInput: true,
      });
      if (sent && conversationIdRef.current === request.conversationId) {
        // Mark the selection consumed so a pending preparation cannot restore it.
        preparedSelectionRef.current = request.selection;
        chatRef.current?.prepareMessage({ text: "", citeMessages: [] });
      }
    }, 0);
    return () => window.clearTimeout(timer);
  }, [conversationId, restartKey]);

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
    <aside
      className={`pdf-temporary-chat${isPaper ? " pdf-temporary-chat--paper" : ""}`}
      aria-label={t("knowledge.pdfChatPanelLabel")}
      onPointerDown={() => touchCachedPdfChat(documentId, conversationIdRef.current)}
      onKeyDown={() => touchCachedPdfChat(documentId, conversationIdRef.current)}
      onWheel={() => touchCachedPdfChat(documentId, conversationIdRef.current)}
      onFocus={() => touchCachedPdfChat(documentId, conversationIdRef.current)}
    >
      <header className="pdf-temporary-chat__header">
        <div>
          <MessageOutlined />
          <strong>{isPaper ? copy.assistant : t("knowledge.pdfChatPanelLabel")}</strong>
          <span>{isPaper ? copy.hint : t("knowledge.pdfChatTemporaryHint")}</span>
        </div>
        {isPaper && <Button type="text" icon={<PlusOutlined />} aria-label={t("knowledge.pdfChatNew")} title={t("knowledge.pdfChatNew")} onClick={() => void startNewConversation()} />}
        <Button type="text" icon={<CloseOutlined />} aria-label={t("common.close")} onClick={onClose} />
      </header>
      {isPaper && parseState === "parsed" && <div className="paper-assistant-ready" role="status"><span />{copy.ready}<span className="paper-assistant-ready__scope">{copy.scope}</span></div>}
      {!isPaper && <div className="pdf-temporary-chat__actions">
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
      </div>}
      {isPaper && activeSelection && <div className="paper-assistant-quote">
        <div className="paper-assistant-quote__heading">
          <button type="button" disabled={!onNavigateSelection} onClick={() => onNavigateSelection?.(activeSelection)}>
            {copy.quote}{activeSelection.page ? (zh ? ` · 第 ${activeSelection.page} 页` : ` · Page ${activeSelection.page}`) : ""}
          </button>
          <Button type="text" size="small" icon={<CloseOutlined />} aria-label={copy.remove} onClick={() => {
            setDismissedSelection(activeSelection);
            preparedSelectionRef.current = activeSelection;
            chatRef.current?.prepareMessage({ citeMessages: [] });
            onClearSelection?.();
          }} />
        </div>
        <blockquote>{activeSelection.text}</blockquote>
      </div>}
      <div className="pdf-temporary-chat__body">
        {parseState === "parsing" ? (
          <Alert className="pdf-temporary-chat__parse-status" type="info" showIcon message={t("knowledge.pdfChatParsing")} description={t("knowledge.pdfChatParsingHint")} />
        ) : null}
        {parseState === "parsed" && !isPaper ? (
          <Alert className="pdf-temporary-chat__parse-status" type="success" showIcon message={t("knowledge.pdfChatParsed")} />
        ) : null}
        {parseState === "failed" ? (
          <Alert className="pdf-temporary-chat__parse-status" type="error" showIcon message={t("knowledge.pdfChatParseFailed")} description={parseError} action={<Button size="small" loading={retrying} onClick={async () => {
            setRetrying(true);
            try { await retryDocumentParsed(datasetId, documentId); setParseAttempt(value => value + 1); }
            catch (error) { setParseError(getLocalizedErrorMessage(error)); }
            finally { setRetrying(false); }
          }}>{t("knowledge.pdfChatRetryParse")}</Button>} />
        ) : null}
        {["loading", "unknown", "not_requested"].includes(parseState) && <Alert
          className="pdf-temporary-chat__parse-status"
          type={parseState === "loading" ? "info" : "warning"}
          showIcon
          message={t(parseState === "loading" ? "knowledge.pdfChatChecking" : parseState === "not_requested" ? "knowledge.pdfChatNotParsed" : "knowledge.pdfChatUnknown")}
          description={parseError}
          action={parseState !== "loading" ? <Button size="small" onClick={() => setParseAttempt(value => value + 1)}>{t("knowledge.pdfChatRefreshStatus")}</Button> : undefined}
        />}
        <ChatContainerComponent
          ref={chatRef}
          initialCard={isPaper ? <section className="paper-assistant-welcome">
            <h2>{copy.welcome}</h2>
            <p>{copy.intro}</p>
            <div className="paper-assistant-prompts">
              {copy.prompts.map(([title, description, prompt]) => <button type="button" key={title} onClick={() => chatRef.current?.prepareMessage({ text: prompt, appendCitations: true })}>
                <strong>{title}<span aria-hidden="true">↗</span></strong><span>{description}</span>
              </button>)}
            </div>
          </section> : undefined}
          canChat={parseState === "parsed"}
          disabledReason={parseState === "parsed" ? undefined : t(parseState === "failed" ? "knowledge.pdfChatParseFailed" : parseState === "parsing" ? "knowledge.pdfChatParsing" : parseState === "loading" ? "knowledge.pdfChatChecking" : "knowledge.pdfChatUnknown")}
          sessionId={conversationId}
          onOpenSSE={openSSE}
          onOpenResumeSSE={openResumeSSE}
          onConversationIdChange={(id) => {
            if (!id) return;
            setConversationId(id);
            setConversationCreated(true);
            touchCachedPdfChat(documentId, id);
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
          showModelSelector
          composerVariant="paper"
          modelSelectionDraft={!conversationCreated}
          allowLocalWorkspace={false}
          fixedThinkingDepth="low"
        />
      </div>
    </aside>
  );
});

export default PdfTemporaryChat;
