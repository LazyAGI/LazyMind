import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import { Alert, Button, Space, message } from "antd";
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
import { getDocumentParseStatus, ensureDocumentParsed, retryDocumentParsed, type DocumentParseResult } from "@/modules/knowledge/api/pdfArtifacts";

interface PdfTemporaryChatProps {
  datasetId: string;
  documentId: string;
  fileName: string;
  paperMode?: boolean;
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

function documentContextForSelection(
  datasetId: string,
  documentId: string,
  fileName: string,
  selection?: DocumentChatSelection,
) {
  return {
    dataset_id: datasetId,
    document_id: documentId,
    file_name: fileName,
    page: selection?.page,
    bbox: selection?.bbox,
    segment_id: selection?.segmentId,
    segment_number: selection?.segmentNumber,
    segment_group: selection?.group,
    selected_text: selection?.text,
    paragraph_text: selection?.context,
    ...(selection?.quickExplain ? { expand_paper_context: true } : {}),
  };
}

function scopedDocumentContext(
  datasetId: string,
  documentId: string,
  fileName: string,
  selection: DocumentChatSelection | undefined,
  snapshot: unknown,
) {
  if (
    snapshot && typeof snapshot === "object" && !Array.isArray(snapshot) &&
    (snapshot as Record<string, unknown>).dataset_id === datasetId &&
    (snapshot as Record<string, unknown>).document_id === documentId
  ) {
    return { ...snapshot, dataset_id: datasetId, document_id: documentId, file_name: fileName };
  }
  return documentContextForSelection(datasetId, documentId, fileName, selection);
}

const PdfTemporaryChat = forwardRef<PdfTemporaryChatHandle, PdfTemporaryChatProps>(function PdfTemporaryChat({
  datasetId,
  documentId,
  fileName,
  paperMode = false,
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
    assistant:"论文助手", hint:"细读论文，随时提问。", ready:"论文已就绪", scope:"回答基于当前文档", quote:"原文摘录", page:"第", remove:"移除原文摘录", companion:"阅读助手", welcome:"从方法开始读懂论文", intro:"沿着方法走一遍，再回原文核对设计、实验和边界。点击下方问题可先编辑再发送。",
    prompts:[
      ["方法怎么运作", "沿输入到输出走一遍", "请只基于当前论文解释方法如何运作。优先用论文自己的任务实例说明输入和预期输出；若没有实例，可用明确标注的教学示例。再按实际顺序讲清每一步的操作、输出及与下一步的连接；指出作者改变了哪个环节、为什么这样改。区分训练和推理；若不是训练型方法，就说明执行过程和运行条件。优先引用方法图与对应章节，找不到依据时明确说未找到。"],
      ["关键设计为何有效", "聚焦机制与必要公式", "请从当前论文选出最关键的1—3处机制，不要平均复述所有模块。逐一说明它解决的具体困难、如何连接前后步骤，以及原文给出的设计理由。若涉及核心公式，先说明计算目的，再解释主要符号、各项作用和训练目标。标出相关图、公式或章节；区分作者的解释和基于原文的推断。"],
      ["实验支持了什么", "核对主结果与消融", "请核查当前论文中最能检验核心设计的主实验和消融。保留数据集、指标、baseline、backbone及其他关键实验条件，给出原文报告的数字和图表出处；分别说明结果直接支持什么、仍不能证明什么。不要混用不同实验条件计算提升，也不要把作者的解释当成实验结论；缺少消融或比较条件时请说明。"],
      ["边界与待核查处", "分清限制、证据和推断", "请结合当前论文的方法假设和实验覆盖范围，说明适用条件与尚未验证之处。分开列出作者明确陈述的限制、实验直接显示的边界、以及你的分析或推断，并给出对应原文位置。未找到限制时不要虚构通用批评；最后提出2—3个值得回原文核查的具体问题。"],
    ],
  } : {
    assistant:"Paper assistant", hint:"Read closely. Ask freely.", ready:"Paper ready", scope:"Answers grounded in this document", quote:"Source quote", page:"Page", remove:"Remove source quote", companion:"Your reading companion", welcome:"Understand the method first", intro:"Follow the method, then check its design, evidence, and limits against the paper. Select a question to edit it before sending.",
    prompts:[
      ["How the method works", "Trace input to output", "Use only the current paper to explain how the method works. Start with the paper's own task example, input, and expected output; if it has no example, label any teaching example as your own. Then trace each step in order: its operation, output, and link to the next step. Identify what the authors changed and why. Distinguish training from inference; for a non-training method, explain its execution and operating conditions. Cite the method figure and relevant sections when available; say when a detail cannot be found."],
      ["Why these designs", "Focus on mechanisms and equations", "Choose the 1–3 mechanisms most important to the current paper's method rather than summarizing every module. For each, explain the difficulty it addresses, how it connects to adjacent steps, and the authors' rationale. For a central equation, explain its purpose before defining the main symbols, terms, and training objective. Cite the relevant figure, equation, or section, and distinguish the authors' explanation from your inference."],
      ["What the experiments show", "Check main results and ablations", "Examine the main results and ablations that test the current paper's central design. Preserve datasets, metrics, baselines, backbones, and other relevant experimental conditions. Quote the reported numbers with figure or table references, then separate what the results directly support from what they do not establish. Do not compute improvements across different settings or present the authors' interpretation as an experimental finding. State when ablations or comparison details are missing."],
      ["Where it may not apply", "Separate evidence from inference", "Using the current paper's assumptions and experimental coverage, explain its operating conditions and unresolved questions. Separate limitations explicitly stated by the authors, boundaries directly visible in the experiments, and your own analysis or inference; give source locations for each. Do not invent generic limitations when the paper gives none. End with 2–3 specific questions worth checking in the original paper."],
    ],
  };
  const isPaper = paperMode;
  const [dismissedSelection, setDismissedSelection] = useState<DocumentChatSelection>();
  const activeSelection = selection !== dismissedSelection ? selection : undefined;
  const chatRef = useRef<ChatImperativeProps>(null);
  const initialConversationIdRef = useRef(newPreviewConversationId());
  const conversationIdRef = useRef(initialConversationIdRef.current);
  const preparedSelectionRef = useRef<DocumentChatSelection>();
  const requestedQuickParseRef = useRef<DocumentChatSelection>();
  const activeSelectionRef = useRef<DocumentChatSelection>();
  activeSelectionRef.current = activeSelection;
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
  const parseErrorDescription = /dynamic_ocr_auth|ocr_auth.{0,40}mineru.{0,40}not set/i.test(parseError)
    ? <span>{t("knowledge.pdfChatParserRequired")} <a href="/settings?section=model_defaults&service=parsing">{t("pdfFormula.configureService")}</a></span>
    : parseError;
  const [parseAttempt, setParseAttempt] = useState(0);
  const [retrying, setRetrying] = useState(false);

  useEffect(() => {
    // Observation never creates paid parsing work.
    let cancelled = false;
    setParseState("loading");
    setParseError("");
    let timer: number | undefined;
    const poll = async () => {
      try {
        const result = await getDocumentParseStatus(datasetId, documentId);
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
    if (activeSelection.quickExplain) return;
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

  useEffect(() => {
    if (!activeSelection?.quickExplain || preparedSelectionRef.current === activeSelection) return;
    if (parseState === "loading" || parseState === "parsing") return;
    const request = activeSelection;
    const question = request.question?.trim() || t("knowledge.pdfQuickExplainPrompt");
    const prepareForRetry = () => {
      if (activeSelectionRef.current !== request) return;
      preparedSelectionRef.current = request;
      chatRef.current?.prepareMessage({ text: question, citeMessage: request.text, appendCitations: true, appendText: true });
      message.warning(t("knowledge.pdfQuickExplainSendFailed"));
    };
    if (parseState === "not_requested" && requestedQuickParseRef.current !== request) {
      requestedQuickParseRef.current = request;
      setParseState("parsing");
      void ensureDocumentParsed(datasetId, documentId).then(result => {
        setParseState(result.status);
        setParseError(result.error?.message || "");
        if (result.status === "not_requested") prepareForRetry();
        if (result.status === "parsing") setParseAttempt(value => value + 1);
      }).catch(error => {
        setParseError(getLocalizedErrorMessage(error));
        setParseState("failed");
      });
      return;
    }
    let attempts = 0;
    let timer: number;
    const sendWhenReady = () => {
      if (activeSelectionRef.current !== request || preparedSelectionRef.current === request) return;
      if (!chatRef.current) {
        if (attempts++ < 10) timer = window.setTimeout(sendWhenReady, 50);
        return;
      }
      preparedSelectionRef.current = request;
      if (parseState !== "parsed") { prepareForRetry(); return; }
      const conversationAtSend = conversationIdRef.current;
      void chatRef.current.sendMessage({
        text: question,
        citeMessage: request.text,
        clearInput: false,
        fileList: [],
        document_context: documentContextForSelection(datasetId, documentId, fileName, request),
      })
        .then(sent => {
          if (activeSelectionRef.current !== request || conversationIdRef.current !== conversationAtSend) return;
          if (!sent) { prepareForRetry(); return; }
          setDismissedSelection(request);
          onClearSelection?.();
        }).catch(prepareForRetry);
    };
    timer = window.setTimeout(sendWhenReady, 50);
    return () => window.clearTimeout(timer);
  }, [activeSelection, parseState, restartKey, datasetId, documentId, fileName, onClearSelection, t]);

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
      document_context: scopedDocumentContext(datasetId, documentId, fileName, activeSelection, extras?.document_context),
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
          <Alert className="pdf-temporary-chat__parse-status" type="error" showIcon message={t("knowledge.pdfChatParseFailed")} description={parseErrorDescription} action={<Button size="small" loading={retrying} onClick={async () => {
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
          description={parseErrorDescription}
          action={parseState !== "loading" ? <Space><Button size="small" onClick={() => setParseAttempt(value => value + 1)}>{t("knowledge.pdfChatRefreshStatus")}</Button><Button size="small" loading={retrying} onClick={async () => {
            setRetrying(true);
            try { await ensureDocumentParsed(datasetId, documentId); setParseAttempt(value => value + 1); }
            catch (error) { setParseError(getLocalizedErrorMessage(error)); }
            finally { setRetrying(false); }
          }}>{i18n.language.startsWith("zh") ? '准备文档' : 'Prepare document'}</Button></Space> : undefined}
        />}
        <ChatContainerComponent
          ref={chatRef}
          initialCard={isPaper ? <section className="paper-assistant-welcome">
            <h2>{copy.welcome}</h2>
            <p>{copy.intro}</p>
            <div className="paper-assistant-prompts">
              {copy.prompts.map(([title, description, prompt]) => <button type="button" key={title} onClick={() => chatRef.current?.prepareMessage({ text: prompt, appendCitations: true })}>
                <strong>{title}<span aria-hidden="true">＋</span></strong><span>{description}</span>
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
          showModelSelector={isPaper}
          composerVariant={isPaper ? "paper" : "default"}
          modelSelectionDraft={isPaper && !conversationCreated}
          allowLocalWorkspace={false}
          fixedThinkingDepth="low"
        />
      </div>
    </aside>
  );
});

export default PdfTemporaryChat;
