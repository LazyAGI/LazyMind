import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  CheckOutlined,
  CloseOutlined,
  DeleteOutlined,
  LinkOutlined,
  PushpinOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Alert, Button, Drawer, Modal, Skeleton, Tooltip, message } from "antd";
import { useTranslation } from "react-i18next";
import i18n from "@/i18n";
import { AgentAppsAuth } from "@/components/auth";
import { localizeErrorCode } from "@/components/request";
import { ChatConversationsRequestActionEnum } from "@/api/generated/chatbot-client";
import ChatContainerComponent from "../newChatContainer";
import type { ChatImperativeProps } from "../newChatContainer";
import type { ChatConfig } from "../ChatConfigs";
import {
  useChatThinkStore,
  type ThinkingDepth,
} from "@/modules/chat/store/chatThink";
import {
  CHAT_RESUME_STREAM_URL,
  CHAT_STREAM_URL,
} from "@/modules/chat/utils/request";
import { Method, SSE } from "@/modules/chat/utils/sse";
import { streamManager } from "@/modules/chat/utils/StreamManager";
import UIUtils from "@/modules/chat/utils/ui";
import {
  createSideChat,
  deleteSideChat,
  patchSideChatThinkingDepth,
  retainSideChat,
} from "./api";
import {
  buildSideChatStreamPayload,
  chatConfigFromSideChat,
  sideChatSourceKey,
  sideChatSourceText,
} from "./helpers";
import type {
  SideChatConversation,
  SideChatPanelProps,
  SideChatSource,
} from "./types";
import "./index.scss";

export type {
  OnOpenSideChat,
  SideChatConversation,
  SideChatPanelProps,
  SideChatSource,
} from "./types";

type SideChatPhase =
  | "idle"
  | "creating"
  | "ready"
  | "clearing"
  | "retaining"
  | "closing"
  | "error";

const DISCARD_RETRY_DELAYS_MS = [0, 100, 250, 500, 1_000, 2_000];

function sideChatRequestId() {
  return globalThis.crypto?.randomUUID?.() ??
    `sidechat-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function requestStatus(error: unknown) {
  return (error as { response?: { status?: number } })?.response?.status;
}

async function wait(milliseconds: number) {
  await new Promise<void>((resolve) => window.setTimeout(resolve, milliseconds));
}

function sourceCopy(
  source: SideChatSource | null,
  conversation: SideChatConversation | null,
) {
  return sideChatSourceText(source, conversation);
}

export default function SideChatPanel({
  open,
  parentConversationId,
  source,
  onClose,
  onRetained,
  canChat = true,
  embeddingReady,
  multimodalEmbeddingReady,
  rerankReady,
  returnFocusRef,
}: SideChatPanelProps) {
  const { t } = useTranslation();
  const tRef = useRef(t);
  tRef.current = t;
  const titleId = useId();
  const chatRef = useRef<ChatImperativeProps>(null);
  const childRef = useRef<SideChatConversation | null>(null);
  const retainedRef = useRef(false);
  const streamingRef = useRef(false);
  const requestPendingRef = useRef(false);
  const hasMessagesRef = useRef(false);
  const openRef = useRef(open);
  const requestGenerationRef = useRef(0);
  const activeSourceKeyRef = useRef("");
  const activeSourceRef = useRef<SideChatSource | null>(null);
  const discardPromisesRef = useRef(new Map<string, Promise<void>>());
  const thinkingSaveRef = useRef(0);
  const chatConfigRef = useRef<ChatConfig>({});
  const thinkingDepthRef = useRef<ThinkingDepth>("medium");
  const warnedSourceKeyRef = useRef("");

  const [phase, setPhase] = useState<SideChatPhase>("idle");
  const [child, setChild] = useState<SideChatConversation | null>(null);
  const [activeSource, setActiveSource] = useState<SideChatSource | null>(null);
  const [retained, setRetained] = useState(false);
  const [streaming, setStreaming] = useState(false);
  const [requestPending, setRequestPending] = useState(false);
  const [hasMessages, setHasMessages] = useState(false);
  const [chatConfig, setChatConfig] = useState<ChatConfig>({});
  const [thinkingDepth, setThinkingDepth] =
    useState<ThinkingDepth>("medium");
  const [actionError, setActionError] = useState("");
  const [clearConfirmOpen, setClearConfirmOpen] = useState(false);
  const [discardConfirmOpen, setDiscardConfirmOpen] = useState(false);

  openRef.current = open;
  const requestedSourceKey = useMemo(() => sideChatSourceKey(source), [source]);

  const discardChild = useCallback((conversationId: string) => {
    const existing = discardPromisesRef.current.get(conversationId);
    if (existing) return existing;
    const request = (async () => {
      streamManager.closeAndCleanup(conversationId);
      let lastError: unknown;
      for (const delay of DISCARD_RETRY_DELAYS_MS) {
        if (delay > 0) await wait(delay);
        try {
          await deleteSideChat(conversationId);
          return;
        } catch (error) {
          lastError = error;
          const status = requestStatus(error);
          if (status === 404) return;
          if (status !== 409) throw error;
        }
      }
      throw lastError;
    })().catch((error) => {
      discardPromisesRef.current.delete(conversationId);
      throw error;
    });
    discardPromisesRef.current.set(conversationId, request);
    return request;
  }, []);

  const startCreate = useCallback(
    async (requestedSource: SideChatSource | null) => {
      const generation = ++requestGenerationRef.current;
      setPhase("creating");
      setActionError("");
      setChild(null);
      childRef.current = null;
      retainedRef.current = false;
      requestPendingRef.current = false;
      hasMessagesRef.current = false;
      setRequestPending(false);
      setHasMessages(false);
      setRetained(false);
      try {
        const conversation = await createSideChat(
          parentConversationId,
          requestedSource,
          useChatThinkStore.getState().thinkingDepth,
        );
        if (
          generation !== requestGenerationRef.current ||
          !openRef.current
        ) {
          await discardChild(conversation.id).catch(() => undefined);
          return;
        }
        const inheritedConfig = chatConfigFromSideChat(conversation);
        childRef.current = conversation;
        chatConfigRef.current = inheritedConfig;
        thinkingDepthRef.current = conversation.thinkingDepth;
        setChild(conversation);
        setChatConfig(inheritedConfig);
        setThinkingDepth(conversation.thinkingDepth);
        setPhase("ready");
        requestAnimationFrame(() => chatRef.current?.focusInput?.());
      } catch {
        if (generation !== requestGenerationRef.current) return;
        setPhase("error");
        setActionError(tRef.current("chat.sideChat.loadFailed"));
      }
    },
    [discardChild, parentConversationId],
  );

  useEffect(() => {
    if (!open || !parentConversationId) return;
    const requestedSource = source ? { ...source } : null;
    activeSourceKeyRef.current = sideChatSourceKey(requestedSource);
    activeSourceRef.current = requestedSource;
    setActiveSource(requestedSource);
    void startCreate(requestedSource);

    return () => {
      requestGenerationRef.current += 1;
      const current = childRef.current;
      if (current && !retainedRef.current) {
        void discardChild(current.id).catch(() => {
          message.error(tRef.current("chat.sideChat.closeFailed"));
        });
      } else if (current) {
        streamManager.closeAndCleanup(current.id);
      }
      childRef.current = null;
    };
    // Source changes while the panel is open are handled separately so an
    // unretained conversation is never silently replaced.
  }, [discardChild, open, parentConversationId, startCreate]);

  useEffect(() => {
    if (!open || requestedSourceKey === activeSourceKeyRef.current) return;
    if (streamingRef.current || requestPendingRef.current) {
      if (warnedSourceKeyRef.current !== requestedSourceKey) {
        warnedSourceKeyRef.current = requestedSourceKey;
        message.warning(t("chat.sideChat.generatingUnavailable"));
      }
      return;
    }
    if (hasMessagesRef.current && !retainedRef.current) {
      if (warnedSourceKeyRef.current !== requestedSourceKey) {
        warnedSourceKeyRef.current = requestedSourceKey;
        message.warning(t("chat.sideChat.replaceBlocked"));
      }
      return;
    }

    const requestedSource = source ? { ...source } : null;
    const replace = async () => {
      const current = childRef.current;
      requestGenerationRef.current += 1;
      if (current && !retainedRef.current) {
        try {
          await discardChild(current.id);
        } catch {
          setActionError(t("chat.sideChat.closeFailed"));
          return;
        }
      }
      activeSourceKeyRef.current = requestedSourceKey;
      activeSourceRef.current = requestedSource;
      warnedSourceKeyRef.current = "";
      setActiveSource(requestedSource);
      await startCreate(requestedSource);
    };
    void replace();
  }, [
    discardChild,
    open,
    requestedSourceKey,
    source,
    startCreate,
    t,
  ]);

  const handleStreamingChange = useCallback((next: boolean) => {
    streamingRef.current = next;
    setStreaming(next);
    if (next) {
      requestPendingRef.current = false;
      setRequestPending(false);
    }
  }, []);

  const handleRequestPendingChange = useCallback((next: boolean) => {
    requestPendingRef.current = next;
    setRequestPending(next);
    if (next) {
      hasMessagesRef.current = true;
      setHasMessages(true);
    }
  }, []);

  const handleThinkingDepthChange = useCallback(
    async (next: ThinkingDepth) => {
      const current = childRef.current;
      if (!current || streamingRef.current || requestPendingRef.current) return;
      const previous = thinkingDepthRef.current;
      const save = ++thinkingSaveRef.current;
      thinkingDepthRef.current = next;
      setThinkingDepth(next);
      try {
        await patchSideChatThinkingDepth(current.id, next);
      } catch {
        if (save !== thinkingSaveRef.current) return;
        thinkingDepthRef.current = previous;
        setThinkingDepth(previous);
        message.error(t("chat.sideChat.settingsSaveFailed"));
      }
    },
    [t],
  );

  const openSSE = useCallback(
    (
      input: unknown[],
      action: ChatConversationsRequestActionEnum,
      callbacks: Record<string, (event: CustomEvent) => void>,
      extras?: Record<string, unknown>,
    ) => {
      const conversation = childRef.current;
      if (!conversation) {
        throw new Error("Side chat is not ready");
      }
      const prepareClientConversationId =
        extras?.__prepareClientConversationId;
      if (typeof prepareClientConversationId === "function") {
        prepareClientConversationId(conversation.id);
      }
      hasMessagesRef.current = true;
      setHasMessages(true);
      const requestedDepth = extras?.thinking_depth as
        | ThinkingDepth
        | undefined;
      return new SSE(CHAT_STREAM_URL, {
        method: Method.POST,
        headers: {
          "Content-Type": "application/json",
          Accept: "text/event-stream",
          ...AgentAppsAuth.getAuthHeaders(),
        },
        timeout: 1_800_000,
        payload: JSON.stringify(
          buildSideChatStreamPayload({
            conversationId: conversation.id,
            action,
            input,
            thinkingDepth: requestedDepth ?? thinkingDepthRef.current,
            chatConfig: chatConfigRef.current,
            modelLabel: t("chat.lazyMindModel"),
            locale: i18n.resolvedLanguage || i18n.language,
            clientRequestId: sideChatRequestId(),
          }),
        ),
        callbacks,
      });
    },
    [t],
  );

  const openResumeSSE = useCallback(
    (
      conversationId: string,
      callbacks: Record<string, (event: CustomEvent) => void>,
      cursor?: { historyId?: string; afterSequence?: number },
    ) =>
      new SSE(CHAT_RESUME_STREAM_URL, {
        method: Method.POST,
        headers: {
          "Content-Type": "application/json",
          Accept: "text/event-stream",
          ...AgentAppsAuth.getAuthHeaders(),
        },
        timeout: 1_800_000,
        payload: JSON.stringify({
          conversation_id: conversationId,
          history_id: cursor?.historyId,
          after_sequence: cursor?.afterSequence || undefined,
          basic_chat_only: true,
          use_memory: false,
        }),
        callbacks,
      }),
    [],
  );

  const handleRetain = useCallback(async () => {
    const current = childRef.current;
    if (
      !current ||
      retainedRef.current ||
      streamingRef.current ||
      requestPendingRef.current
    ) return;
    setPhase("retaining");
    setActionError("");
    try {
      const saved = await retainSideChat(current.id);
      childRef.current = saved;
      retainedRef.current = true;
      setChild(saved);
      setRetained(true);
      setPhase("ready");
      message.success(t("chat.sideChat.retainSuccess"));
      onRetained?.(saved);
    } catch {
      setPhase("ready");
      setActionError(t("chat.sideChat.retainFailed"));
    }
  }, [onRetained, t]);

  const discardAndClose = useCallback(async () => {
    if (streamingRef.current || requestPendingRef.current) {
      message.warning(t("chat.sideChat.generatingUnavailable"));
      return;
    }
    const current = childRef.current;
    setActionError("");
    if (current && !retainedRef.current) {
      setPhase("closing");
      try {
        await discardChild(current.id);
      } catch {
        setPhase("ready");
        setActionError(t("chat.sideChat.closeFailed"));
        return;
      }
    } else if (current) {
      streamManager.closeAndCleanup(current.id);
    }
    openRef.current = false;
    const focusTarget = returnFocusRef?.current;
    onClose();
    requestAnimationFrame(() => focusTarget?.focus());
  }, [discardChild, onClose, returnFocusRef, t]);

  const handleClose = useCallback(() => {
    if (streamingRef.current || requestPendingRef.current) {
      message.warning(t("chat.sideChat.generatingUnavailable"));
      return;
    }
    if (hasMessagesRef.current && !retainedRef.current) {
      setDiscardConfirmOpen(true);
      return;
    }
    void discardAndClose();
  }, [discardAndClose, t]);

  const handleClear = useCallback(async () => {
    const current = childRef.current;
    if (!current || streamingRef.current || requestPendingRef.current) return;
    setClearConfirmOpen(false);
    setPhase("clearing");
    setActionError("");
    try {
      if (!retainedRef.current) {
        await discardChild(current.id);
      } else {
        streamManager.closeAndCleanup(current.id);
      }
      childRef.current = null;
      retainedRef.current = false;
      hasMessagesRef.current = false;
      setHasMessages(false);
      await startCreate(activeSourceRef.current);
    } catch {
      setPhase("ready");
      setActionError(t("chat.sideChat.clearFailed"));
    }
  }, [discardChild, startCreate, t]);

  const loading = phase === "creating";
  const busy =
    loading ||
    phase === "clearing" ||
    phase === "retaining" ||
    phase === "closing" ||
    streaming ||
    requestPending;
  const sourceText = sourceCopy(activeSource, child);

  return (
    <>
      <Drawer
        className="side-chat-drawer"
        rootClassName="side-chat-drawer-root"
        width={420}
        open={open}
        closable={false}
        destroyOnHidden
        mask={false}
        keyboard={!busy}
        onClose={handleClose}
        title={
          <div className="side-chat-header">
            <div className="side-chat-heading">
              <span className="side-chat-heading-icon" aria-hidden="true">
                <LinkOutlined />
              </span>
              <div>
                <strong id={titleId}>{t("chat.sideChat.title")}</strong>
                <span>{t("chat.sideChat.description")}</span>
              </div>
            </div>
            <div className="side-chat-header-actions">
              <Tooltip title={t("chat.sideChat.clear")}>
                <Button
                  type="text"
                  size="small"
                  icon={<DeleteOutlined />}
                  aria-label={t("chat.sideChat.clear")}
                  disabled={!child || busy}
                  onClick={() => setClearConfirmOpen(true)}
                />
              </Tooltip>
              <Tooltip
                title={
                  streaming || requestPending
                    ? t("chat.sideChat.generatingUnavailable")
                    : retained
                      ? t("chat.sideChat.retained")
                      : t("chat.sideChat.retain")
                }
              >
                <Button
                  type={retained ? "default" : "primary"}
                  size="small"
                  icon={retained ? <CheckOutlined /> : <PushpinOutlined />}
                  aria-label={
                    retained
                      ? t("chat.sideChat.retained")
                      : t("chat.sideChat.retain")
                  }
                  disabled={!child || !hasMessages || busy || retained}
                  onClick={() => void handleRetain()}
                >
                  {retained
                    ? t("chat.sideChat.retained")
                    : t("chat.sideChat.retain")}
                </Button>
              </Tooltip>
              <Tooltip
                placement="left"
                title={
                  streaming || requestPending
                    ? t("chat.sideChat.generatingUnavailable")
                    : t("chat.sideChat.close")
                }
              >
                <Button
                  type="text"
                  size="small"
                  icon={<CloseOutlined />}
                  aria-label={t("chat.sideChat.close")}
                  disabled={requestPending || streaming || phase === "closing"}
                  onClick={handleClose}
                />
              </Tooltip>
            </div>
          </div>
        }
        aria-labelledby={titleId}
      >
        <div className="side-chat-panel">
          <section
            className="side-chat-source-card"
            aria-labelledby={`${titleId}-source`}
          >
            <div className="side-chat-source-label">
              <span id={`${titleId}-source`}>{t("chat.sideChat.source")}</span>
              {child?.parentDisplayName ? (
                <small>
                  {t("chat.sideChat.sourceFromConversation", {
                    name: child.parentDisplayName,
                  })}
                </small>
              ) : null}
            </div>
            {sourceText ? (
              <blockquote>{sourceText}</blockquote>
            ) : (
              <p>{t("chat.sideChat.sourceUnavailable")}</p>
            )}
            <span className="side-chat-inherited-hint">
              {t("chat.sideChat.inheritedContext")}
            </span>
          </section>

          {actionError ? (
            <Alert
              className="side-chat-alert"
              type="error"
              showIcon
              message={actionError}
              action={
                phase === "error" ? (
                  <Button
                    type="text"
                    size="small"
                    icon={<ReloadOutlined />}
                    onClick={() => void startCreate(activeSourceRef.current)}
                  >
                    {t("chat.sideChat.retry")}
                  </Button>
                ) : undefined
              }
            />
          ) : null}

          <div className="side-chat-live-status" aria-live="polite">
            {loading || phase === "clearing"
              ? t("chat.sideChat.loading")
              : ""}
          </div>

          {loading ? (
            <div className="side-chat-loading" aria-busy="true">
              <Skeleton active paragraph={{ rows: 4 }} />
              <Skeleton active paragraph={{ rows: 2 }} />
            </div>
          ) : child ? (
            <div className="side-chat-conversation">
              <ChatContainerComponent
                key={child.id}
                ref={chatRef}
                sessionId={child.id}
                concurrentStream
                canChat={canChat && phase !== "clearing"}
                onOpenSSE={openSSE}
                onOpenResumeSSE={openResumeSSE}
                onConversationIdChange={() => undefined}
                parseErrorData={(data) => {
                  const parsed = UIUtils.jsonParser(data) || {};
                  return localizeErrorCode(
                    `${parsed.error_code || parsed.code || ""}`,
                    localizeErrorCode("2000509"),
                  );
                }}
                setIsChatContent={() => undefined}
                showHistoryButton={false}
                showSkillDeposit={false}
                showConversationConfig={false}
                showModelSelector
                allowKnowledgeBaseSelection={false}
                conversationTrailEnabled={false}
                chatConfig={chatConfig}
                setChatConfigFn={() => undefined}
                thinkingDepth={thinkingDepth}
                onThinkingDepthChange={handleThinkingDepthChange}
                onStreamingChange={handleStreamingChange}
                onRequestPendingChange={handleRequestPendingChange}
                embeddingReady={embeddingReady}
                multimodalEmbeddingReady={multimodalEmbeddingReady}
                rerankReady={rerankReady}
                disabledReason={t("chat.sideChat.unavailable")}
              />
            </div>
          ) : phase !== "error" ? (
            <div className="side-chat-empty" />
          ) : null}
        </div>
      </Drawer>

      <Modal
        open={clearConfirmOpen}
        title={t("chat.sideChat.clearTitle")}
        okText={t("chat.sideChat.clear")}
        cancelText={t("common.cancel")}
        okButtonProps={{ danger: true }}
        onOk={() => void handleClear()}
        onCancel={() => setClearConfirmOpen(false)}
        destroyOnHidden
      >
        <p>{t("chat.sideChat.clearDescription")}</p>
      </Modal>

      <Modal
        open={discardConfirmOpen}
        title={t("chat.sideChat.closeConfirmTitle")}
        okText={t("chat.sideChat.closeAndDiscard")}
        cancelText={t("chat.sideChat.continue")}
        okButtonProps={{ danger: true }}
        onOk={() => {
          setDiscardConfirmOpen(false);
          void discardAndClose();
        }}
        onCancel={() => setDiscardConfirmOpen(false)}
        destroyOnHidden
      >
        <p>{t("chat.sideChat.closeConfirmDescription")}</p>
      </Modal>
    </>
  );
}
