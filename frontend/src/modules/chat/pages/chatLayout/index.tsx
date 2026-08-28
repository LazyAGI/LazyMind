import { FC, type ReactNode, useRef, useState, useEffect, useCallback, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { localizeErrorCode } from "@/components/request";
import { message } from "antd";
import { MessageOutlined, UnorderedListOutlined } from "@ant-design/icons";
import { AgentAppsAuth } from "@/components/auth";
import {
  ChatConversationsRequestActionEnum,
  Query,
} from "@/api/generated/chatbot-client";

import ChatContainerComponent, {
  ChatImperativeProps,
} from "@/modules/chat/components/newChatContainer";
import "./index.scss";
import UIUtils from "@/modules/chat/utils/ui";
import InitialCard from "@/modules/chat/components/InitialCard";
import { ChatConfig } from "@/modules/chat/components/ChatConfigs";
import { Method, SSE } from "@/modules/chat/utils/sse";
import {
  CHAT_RESUME_STREAM_URL,
  CHAT_STREAM_URL,
  ChatServiceApi,
  parseConversationRuntimeSettings,
  resolveConversationThinkingDepth,
  type ConversationRuntimeSettings,
} from "@/modules/chat/utils/request";
import { draftStore, buildWorkflowSearchConfig, useWorkflowStore } from "@/modules/chat/store/workflowPanel";
import { useChatMessageStore } from "@/modules/chat/store/chatMessage";
import {
  DEVELOPER_ACTIVE_EVENT,
  isDeveloperModeActive,
} from "@/utils/developerMode";
import { allowedUploadTypes } from "@/modules/chat/components/ImageUpload";
import {
  CHAT_SELECT_CONVERSATION_EVENT,
  WORKFLOW_PANEL_EXPANDED_EVENT,
  WORKFLOW_PANEL_EXPANDED_STORAGE_PREFIX,
} from "@/modules/chat/constants/chat";
import { buildChatMessageListFromHistory } from "@/modules/chat/utils/message";
import { buildEnvironmentContext } from "@/modules/chat/utils/environment";
import TaskCenter from "@/modules/chat/components/TaskCenter";
import { taskCenterDisplayCount } from "@/modules/chat/components/TaskCenter/taskTimeline";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import type { SubAgentTask } from "@/modules/chat/store/taskCenter";
import { useChatInputStore } from "@/modules/chat/store/chatInput";
import { useChatThinkStore } from "@/modules/chat/store/chatThink";

// Stable empty reference to avoid returning a fresh array from the zustand
// selector on every render, which (with useSyncExternalStore) would trigger an
// infinite re-render loop (React error #185).
const EMPTY_TASKS: SubAgentTask[] = [];
const CONVERSATION_HISTORY_RETRY_DELAYS_MS = [0, 500, 1500];

async function loadConversationHistory(conversationId: string) {
  let lastError: unknown;
  for (const delayMs of CONVERSATION_HISTORY_RETRY_DELAYS_MS) {
    if (delayMs > 0) {
      await new Promise((resolve) => setTimeout(resolve, delayMs));
    }
    try {
      return await ChatServiceApi()
        .conversationServiceGetConversationHistory({
          name: conversationId,
        });
    } catch (error) {
      lastError = error;
    }
  }
  throw lastError;
}

interface IChatLayoutProps {
  conversationId?: string;
  setIsChatContent: (isChatContent: boolean) => void;
  initchatConfig: ChatConfig;
  setChatConfigFn: (val: ChatConfig) => void;
  canChat: boolean;
  embeddingReady?: boolean | null;
  multimodalEmbeddingReady?: boolean | null;
  rerankReady?: boolean | null;
  chatDisabledReason?: string;
  chatDisabledDescription?: ReactNode;
  chatDisabledAction?: ReactNode;
  /** Workflow settings selected on the welcome screen before the first message is sent. */
  initPendingConversationSettings?: ConversationRuntimeSettings | null;
}

const ChatLayout: FC<IChatLayoutProps> = (props) => {
  const { t, i18n } = useTranslation();
  const {
    conversationId: routeConversationId,
    setIsChatContent,
    initchatConfig,
    setChatConfigFn,
    canChat,
    embeddingReady,
    multimodalEmbeddingReady,
    rerankReady,
    chatDisabledReason,
    chatDisabledDescription,
    chatDisabledAction,
    initPendingConversationSettings,
  } = props;
  const [sessionId, setSessionId] = useState("");
  const [chatConfig, setChatConfig] = useState<ChatConfig>(
    initchatConfig || {},
  );
  // Pending workflow settings from the chat config popover before a conversation is created.
  // Initialised from the welcome-screen selection when provided.
  const pendingConversationSettingsRef = useRef<ConversationRuntimeSettings | null>(
    initPendingConversationSettings ?? null,
  );
  // Workflow settings loaded from conversation detail (for existing conversations).
  const [conversationSettings, setConversationSettings] = useState<ConversationRuntimeSettings | undefined>(undefined);
  const [knowledgeRefreshKey, setKnowledgeRefreshKey] = useState(0);
  const [isTaskPanelCollapsed, setIsTaskPanelCollapsed] = useState(false);
  const [panelWidth, setPanelWidth] = useState<number>(0); // 0 = use CSS default
  const [workflowPanelExpanded, setWorkflowPanelExpanded] = useState(false);
  const [expandedRailTab, setExpandedRailTab] = useState<"chat" | "tasks">("chat");
  const [developerModeActive, setDeveloperModeActiveState] = useState(
    isDeveloperModeActive,
  );

  useEffect(() => {
    const handleDeveloperModeChange = (event: Event) => {
      const detail = (event as CustomEvent<{ active?: boolean }>).detail;
      setDeveloperModeActiveState(
        typeof detail?.active === "boolean"
          ? detail.active
          : isDeveloperModeActive(),
      );
    };
    window.addEventListener(DEVELOPER_ACTIVE_EVENT, handleDeveloperModeChange);
    return () => {
      window.removeEventListener(DEVELOPER_ACTIVE_EVENT, handleDeveloperModeChange);
    };
  }, []);

  useEffect(() => {
    let restoredExpanded = false;
    try {
      restoredExpanded = localStorage.getItem(
        `${WORKFLOW_PANEL_EXPANDED_STORAGE_PREFIX}${sessionId}`,
      ) === "true";
    } catch {
      // Keep the default compact layout when browser storage is unavailable.
    }
    setWorkflowPanelExpanded(restoredExpanded);
    if (restoredExpanded) setExpandedRailTab("chat");

    const handleExpandedChange = (event: Event) => {
      const detail = (event as CustomEvent<{ conversationId: string; expanded: boolean }>).detail;
      if (detail.conversationId === sessionId) {
        setWorkflowPanelExpanded(detail.expanded);
        if (detail.expanded) setExpandedRailTab("chat");
      }
    };
    window.addEventListener(WORKFLOW_PANEL_EXPANDED_EVENT, handleExpandedChange);
    return () => window.removeEventListener(WORKFLOW_PANEL_EXPANDED_EVENT, handleExpandedChange);
  }, [sessionId]);

  // Keep pending settings in sync with the welcome screen while no conversation is active.
  useEffect(() => {
    if (!sessionId) {
      pendingConversationSettingsRef.current = initPendingConversationSettings ?? null;
    }
  }, [initPendingConversationSettings, sessionId]);

  // Load persisted workflow settings once a real conversation id is available.
  useEffect(() => {
    if (!sessionId || sessionId.startsWith('temp_')) {
      if (!sessionId) {
        setConversationSettings(undefined);
      }
      return;
    }
    let cancelled = false;
    ChatServiceApi()
      .conversationServiceGetConversationDetail({ conversation: sessionId })
      .then((detailRes) => {
        if (cancelled) {
          return;
        }
        setConversationSettings(
          parseConversationRuntimeSettings(detailRes.data.conversation),
        );
        useChatThinkStore.getState().setThinkingDepth(
          resolveConversationThinkingDepth(detailRes.data.conversation),
        );
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [sessionId]);
  const panelDragRef = useRef<{ startX: number; startW: number } | null>(null);

  const onPanelResizeStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    const panel = (e.currentTarget as HTMLElement).parentElement;
    if (!panel) return;
    panelDragRef.current = { startX: e.clientX, startW: panel.offsetWidth };
    const onMove = (me: MouseEvent) => {
      if (!panelDragRef.current) return;
      const delta = panelDragRef.current.startX - me.clientX;
      const next = Math.max(260, Math.min(700, panelDragRef.current.startW + delta));
      setPanelWidth(next);
    };
    const onUp = () => {
      panelDragRef.current = null;
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, []);
  const [isRestoringConversation, setIsRestoringConversation] = useState(
    Boolean(routeConversationId),
  );

  const { pendingMessage, clearPendingMessage } = useChatMessageStore();

  const chatRef = useRef<ChatImperativeProps>(null);
  const loadConversationRequestRef = useRef(0);

  const autoRunning = useWorkflowStore((s) =>
    sessionId ? (s.autoRunningByConversation[sessionId] ?? false) : false,
  );
  const workflowSession = useWorkflowStore((s) =>
    sessionId ? s.sessionByConversation[sessionId] ?? null : null,
  );
  const hasWorkflowSession = workflowSession !== null;
  const workflowDefinitionChanged = useWorkflowStore((s) =>
    sessionId
      ? s.sessionByConversation[sessionId]?.runtime_error_code ===
        "WORKFLOW_DEFINITION_CHANGED"
      : false,
  );
  const chatEnabled = canChat && !workflowDefinitionChanged && !isRestoringConversation;

  // When the user changes KB selection during an active workflow session, persist it on the
  // conversation so analyze_subject KB prefetch inherits filters.kb_id.
  const kbSyncInitializedRef = useRef(false);
  useEffect(() => {
    kbSyncInitializedRef.current = false;
  }, [sessionId]);
  useEffect(() => {
    if (!sessionId || sessionId.startsWith('temp_')) {
      return;
    }
    if (!kbSyncInitializedRef.current) {
      kbSyncInitializedRef.current = true;
      return;
    }
    const session = useWorkflowStore.getState().sessionByConversation[sessionId];
    if (!session?.session_id) {
      return;
    }
    if (session.status !== 'active' && session.status !== 'waiting') {
      return;
    }
    const searchConfig = buildWorkflowSearchConfig(chatConfig);
    void useWorkflowStore.getState().syncSessionSearchConfig(
      sessionId,
      session.session_id,
      searchConfig,
    );
  }, [
    sessionId,
    hasWorkflowSession,
    chatConfig?.knowledgeBaseId,
    chatConfig?.creators,
    chatConfig?.tags,
  ]);

  const tasks = useTaskCenterStore((s) =>
    sessionId ? s.tasksByConversation[sessionId] ?? EMPTY_TASKS : EMPTY_TASKS,
  );
  const taskDataLoading = useTaskCenterStore((s) =>
    sessionId ? Boolean(s._loadingTasks[sessionId]) : false,
  );
  const taskDataLoadError = useTaskCenterStore((s) =>
    sessionId ? Boolean(s._taskLoadErrors[sessionId]) : false,
  );
  const taskDisplayCount = useMemo(
    () =>
      taskCenterDisplayCount(
        tasks,
        workflowSession?.steps,
        developerModeActive,
      ),
    [developerModeActive, tasks, workflowSession?.steps],
  );
  const hasTaskPanelContent =
    taskDisplayCount > 0 ||
    taskDataLoadError ||
    (hasWorkflowSession && taskDataLoading);
  const refreshConversationExecution = useTaskCenterStore(
    (s) => s.refreshConversationExecution,
  );
  const subscribeConvEvents = useTaskCenterStore((s) => s.subscribeConvEvents);
  const unsubscribeConvEvents = useTaskCenterStore((s) => s.unsubscribeConvEvents);

  useEffect(() => {
    if (!sessionId) return;
    subscribeConvEvents(sessionId);
    void refreshConversationExecution(sessionId);
    return () => {
      unsubscribeConvEvents(sessionId);
    };
  }, [sessionId, refreshConversationExecution, subscribeConvEvents, unsubscribeConvEvents]);

  // Auto-expand the task panel the first time visible task execution appears.
  // The display count also covers hosted workflow attempts that have no SubAgent row.
  const prevTaskDisplayCountRef = useRef(0);
  useEffect(() => {
    const prev = prevTaskDisplayCountRef.current;
    prevTaskDisplayCountRef.current = taskDisplayCount;
    if (prev === 0 && taskDisplayCount > 0) {
      setIsTaskPanelCollapsed(false);
    }
  }, [taskDisplayCount]);

  // Also auto-expand when a workflow session first appears (even with no tasks yet).
  const prevHasWorkflowSessionRef = useRef(false);
  useEffect(() => {
    const prev = prevHasWorkflowSessionRef.current;
    prevHasWorkflowSessionRef.current = hasWorkflowSession;
    if (!prev && hasWorkflowSession && isDeveloperModeActive()) {
      setIsTaskPanelCollapsed(false);
    }
  }, [hasWorkflowSession]);

  const [isDragging, setIsDragging] = useState(false);
  const dragCounterRef = useRef(0);

  useEffect(() => {
    setChatConfigFn(initchatConfig);
    setChatConfig(initchatConfig);
  }, [initchatConfig]);

  useEffect(() => {
    if (pendingMessage && chatEnabled) {
      const timer = setTimeout(() => {
        chatRef.current?.sendMessage(pendingMessage);
        clearPendingMessage();
      }, 100);

      return () => clearTimeout(timer);
    }
    return undefined;
  }, [pendingMessage, chatEnabled, clearPendingMessage]);

  async function onOpenSSE(
    input: Query[],
    action: ChatConversationsRequestActionEnum,
    callbacks: Record<string, (e: CustomEvent) => void>,
    extras?: Record<string, unknown>,
  ) {
    // Flush any pending slot drafts before sending so the AI sees the latest content.
    // Draft keys use the workflow session_id (not the conversation_id), so pass the
    // workflow session_id when one is active; fall back to conversationId otherwise.
    const activeWorkflowSession = useWorkflowStore.getState().sessionByConversation[sessionId];
    const draftSessionId = activeWorkflowSession?.session_id ?? sessionId;
    await draftStore.flushAllDrafts(draftSessionId);

    const hasUploadedFiles = input?.some(
      (q: Query) => q.input_type === "image" || q.input_type === "file",
    );
    const hasWorkflowMention = Array.isArray(extras?.mentions) &&
      extras.mentions.some(
        (mention) => (mention as { type?: unknown })?.type === "workflow",
      );
    const configSnapshot = extras?.chat_config_snapshot as ChatConfig | undefined;
    const effectiveChatConfig = configSnapshot ?? chatConfig;
    if (configSnapshot) {
      // Keep the newly mounted chat composer aligned with the exact selection
      // used for this message; later workflow-session syncs must not overwrite
      // the persisted request scope with an empty transition-state value.
      setChatConfig(configSnapshot);
      setChatConfigFn(configSnapshot);
    }
    const datasetList =
      (hasUploadedFiles && !hasWorkflowMention) ||
      !effectiveChatConfig?.knowledgeBaseId?.length
        ? []
        : effectiveChatConfig.knowledgeBaseId.map((k) => ({ id: k }));

    // Attach active workflow session context so Go/Python can inject advance_step
    // instead of cold-start trigger tools on follow-up messages.
    const activeSession = useWorkflowStore.getState().sessionByConversation[sessionId];
    const workflowContext =
      activeSession?.status === "active" ||
      activeSession?.status === "waiting" ||
      activeSession?.status === "failed" ||
      activeSession?.status === "completed"
        ? {
            session_id: activeSession.session_id,
            workflow_id: activeSession.workflow_id,
            current_step: activeSession.current_step_id,
          }
        : undefined;

    // Attach focused_tab and focused_sort_order so the AI knows what the user is looking at.
    const { focusedTabByConversation, focusedSortOrderByConversation } =
      useWorkflowStore.getState();
    const focusedTab = focusedTabByConversation[sessionId];
    const focusedSortOrder = focusedSortOrderByConversation[sessionId];
    const workflowUIState =
      focusedTab || focusedSortOrder !== undefined
        ? {
            focused_tab: focusedTab,
            focused_sort_order: focusedSortOrder,
          }
        : undefined;

    // Collect pending artifact references from the chat input store.
    const { getArtifactRefs, clearArtifactRefs } = useChatInputStore.getState();
    const artifactRefs = getArtifactRefs(sessionId);
    // Clear after reading so they are not repeated in the next message.
    if (artifactRefs.length > 0) {
      clearArtifactRefs(sessionId);
    }

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
            database_ids: [effectiveChatConfig?.databaseBaseId]?.filter((id) => !!id),
            creators: effectiveChatConfig?.creators,
            tags: effectiveChatConfig?.tags,
          },
        },
        models: [t("chat.lazyMindModel")],
        thinking_depth:
          extras?.thinking_depth ?? useChatThinkStore.getState().thinkingDepth,
        // enable_thinking: think ? true : false,
        stream: true,
        input,
        mode: "auto",
        create_time: new Date().toISOString(),
        environment_context: buildEnvironmentContext(
          i18n.resolvedLanguage || i18n.language,
        ),
        ...(workflowContext ? { workflow_context: workflowContext } : {}),
        ...(workflowUIState ? { workflow_ui_state: workflowUIState } : {}),
        ...(artifactRefs.length > 0 ? { artifact_refs: artifactRefs } : {}),
        ...(extras?.run_in_background ? { run_in_background: true } : {}),
        ...(Array.isArray(extras?.mentions) && extras.mentions.length > 0
          ? { mentions: extras.mentions }
          : {}),
        ...(Array.isArray(extras?.cite_history_ids) && extras.cite_history_ids.length > 0
          ? { cite_history_ids: extras.cite_history_ids }
          : {}),
        // If the user changed workflow settings before a conversation was created,
        // carry them in the first request so Go can persist them on ensureConversation.
        // Only send the three known fields to avoid polluting the payload with API response leftovers.
        ...(() => {
          const pending = pendingConversationSettingsRef.current;
          if (!sessionId && pending) {
            const clean: Record<string, unknown> = {};
            if (pending.enable_workflow != null) clean.enable_workflow = pending.enable_workflow;
            if (pending.enable_subagent != null) clean.enable_subagent = pending.enable_subagent;
            if (pending.workflow_mode != null) clean.workflow_mode = pending.workflow_mode;
            if (pending.chat_executor != null) clean.chat_executor = pending.chat_executor;
            return { initial_conversation_settings: clean };
          }
          return {};
        })(),
      }),
      callbacks,
    });
  }

  function onOpenResumeSSE(
    conversationId: string,
    callbacks: Record<string, (e: CustomEvent) => void>,
    cursor?: { historyId?: string; afterSequence?: number },
  ) {
    return new SSE(CHAT_RESUME_STREAM_URL, {
      method: Method.POST,
      headers: {
        "Content-Type": "application/json",
        Accept: "text/event-stream",
        ...AgentAppsAuth.getAuthHeaders(),
      },
      timeout: 1800000,
      payload: JSON.stringify({
        conversation_id: conversationId,
        history_id: cursor?.historyId,
        after_sequence: cursor?.afterSequence || undefined,
      }),
      callbacks,
    });
  }

  const sessionIdRef = useRef(sessionId);
  sessionIdRef.current = sessionId;

  const setConversationId = useCallback((id: string) => {
    if (id === sessionIdRef.current) return;
    sessionIdRef.current = id;
    setSessionId(id);
    window.dispatchEvent(
      new CustomEvent(CHAT_SELECT_CONVERSATION_EVENT, {
        detail: { conversationId: id, source: "chat" },
      }),
    );
  }, []);

  const loadConversation = useCallback(async (conversationId: string) => {
    const requestId = ++loadConversationRequestRef.current;
    setIsRestoringConversation(true);
    try {
      let isGenerating = false;
      try {
        const status = await ChatServiceApi().conversationServiceGetChatStatus({ conversationId });
        isGenerating = !!status.data?.is_generating;
      } catch {
        isGenerating = false;
      }
      const [detailRes, historyRes] = await Promise.all([
        ChatServiceApi().conversationServiceGetConversationDetail({ conversation: conversationId }),
        loadConversationHistory(conversationId),
      ]);
      if (requestId !== loadConversationRequestRef.current) return;
      const conversation = detailRes.data.conversation;
      useChatThinkStore.getState().setThinkingDepth(
        resolveConversationThinkingDepth(conversation),
      );
      const tempData = {
        knowledgeBaseId: conversation?.search_config?.dataset_list
          ?.map((dataset: any) => dataset.id)
          .filter((id: string) => !!id),
        creators: conversation?.search_config?.creators,
        tags: conversation?.search_config?.tags,
        databaseBaseId: conversation?.search_config?.database_ids?.[0],
      };
      setChatConfig(tempData);
      setChatConfigFn(tempData);
      setKnowledgeRefreshKey((key) => key + 1);
      setConversationSettings(parseConversationRuntimeSettings(conversation));
      setConversationId(conversationId);

      const list = buildChatMessageListFromHistory(historyRes.data.history, {
        fallbackCreateTime: "xxx-xxx-xxx",
        isGenerating,
      });
      chatRef.current?.replaceMessageList(conversationId, list);
      if (isGenerating) {
        chatRef.current?.openResumeSSE?.(conversationId);
      }
    } catch {
      if (requestId === loadConversationRequestRef.current) {
        setIsChatContent(false);
        message.error(localizeErrorCode("2000509"));
      }
    } finally {
      if (requestId === loadConversationRequestRef.current) {
        setIsRestoringConversation(false);
      }
    }
  }, [setConversationId, setChatConfigFn, setIsChatContent]);

  // Route changes own conversation loading, including browser reload/back/forward.
  useEffect(() => {
    const conversationId = routeConversationId || "";
    if (!conversationId) {
      // The layout also mounts before the first conversation receives a real ID.
      // Nothing needs clearing until a routed/active conversation actually exists.
      if (!sessionIdRef.current) {
        setIsRestoringConversation(false);
        return;
      }
      loadConversationRequestRef.current += 1;
      chatRef.current?.disconnectConversationStream?.(sessionIdRef.current);
      sessionIdRef.current = "";
      setSessionId("");
      setIsRestoringConversation(false);
      setConversationSettings(undefined);
      setChatConfig({});
      setChatConfigFn({});
      chatRef.current?.createNewChat();
      return;
    }
    if (conversationId === sessionIdRef.current) {
      return;
    }
    if (sessionIdRef.current) {
      chatRef.current?.disconnectConversationStream?.(sessionIdRef.current);
      setConversationId(conversationId);
      chatRef.current?.replaceMessageList(conversationId, []);
    }
    setIsChatContent(true);
    void loadConversation(conversationId);
    return () => {
      loadConversationRequestRef.current += 1;
    };
  }, [loadConversation, routeConversationId, setChatConfigFn, setConversationId, setIsChatContent]);

  function parseErrorData(data: string) {
    const dataObject = UIUtils.jsonParser(data) || {};
    return localizeErrorCode(
      `${dataObject.error_code || dataObject.code || ""}`,
      localizeErrorCode("2000509"),
    );
  }

  const isFileTypeSupported = (file: File): boolean => {
    const ext = file.name.substring(file.name.lastIndexOf(".")).toLowerCase();
    return allowedUploadTypes.includes(ext);
  };

  const handleDragEnter = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    if (!canChat) {
      return;
    }
    // Ignore internal DOM drag-and-drop (e.g. workflow panel card sorting).
    if (!Array.from(e.dataTransfer.types).includes('Files')) {
      return;
    }
    dragCounterRef.current++;
    if (e.dataTransfer.items && e.dataTransfer.items.length > 0) {
      setIsDragging(true);
    }
  };

  const handleDragLeave = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounterRef.current--;
    if (dragCounterRef.current === 0) {
      setIsDragging(false);
    }
  };

  const handleDragOver = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
  };

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);
    dragCounterRef.current = 0;

    if (!canChat) {
      if (chatDisabledReason) {
        message.warning(chatDisabledReason);
      }
      return;
    }

    const files = Array.from(e.dataTransfer.files);

    if (files.length === 0) {
      return;
    }

    const unsupportedFiles = files.filter((file) => !isFileTypeSupported(file));

    if (unsupportedFiles.length > 0) {
      message.error(t("chat.unsupportedFileTypeDrag"));
      return;
    }

    (chatRef.current as any)?.uploadFiles?.(files);
  };

  const isTaskPanelRestoreVisible =
    !workflowPanelExpanded && hasTaskPanelContent && isTaskPanelCollapsed;

  return (
    <div
      className={`detail-container${workflowPanelExpanded ? " detail-container--workflow-expanded" : ""}`}
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {}
      {isDragging && (
        <div className="drag-overlay">
          <div className="drag-overlay-content">
            <div className="drag-icon">📁</div>
            <div className="drag-text">{t("chat.dragToUpload")}</div>
            <div className="drag-hint">{t("chat.dragSupportedFormats")}</div>
          </div>
        </div>
      )}
      {workflowPanelExpanded && (
        <div className="expanded-rail-tabs" role="tablist">
          <button
            type="button"
            role="tab"
            aria-selected={expandedRailTab === "chat"}
            className={expandedRailTab === "chat" ? "active" : ""}
            onClick={() => setExpandedRailTab("chat")}
          >
            <MessageOutlined aria-hidden />
            <span>{t("chat.workflowRailConversation")}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={expandedRailTab === "tasks"}
            className={expandedRailTab === "tasks" ? "active" : ""}
            onClick={() => setExpandedRailTab("tasks")}
          >
            <UnorderedListOutlined aria-hidden />
            <span>{t("taskCenter.panelTitle")}</span>
            {taskDisplayCount > 0 && <span className="expanded-rail-tabs__count">{taskDisplayCount}</span>}
          </button>
        </div>
      )}
      <div className={`chat-conversation-pane${workflowPanelExpanded && expandedRailTab !== "chat" ? " chat-conversation-pane--hidden" : ""}${isTaskPanelRestoreVisible ? " chat-conversation-pane--task-restore-visible" : ""}`}>
      <ChatContainerComponent
        ref={chatRef}
        canChat={chatEnabled}
        initialCard={isRestoringConversation ? null : <InitialCard />}
        sessionId={sessionId}
        onOpenSSE={onOpenSSE}
        onOpenResumeSSE={onOpenResumeSSE}
        onConversationIdChange={setConversationId}
        parseErrorData={parseErrorData}
        showHistoryButton={false}
        setIsChatContent={setIsChatContent}
        chatConfig={chatConfig}
        setChatConfig={setChatConfig}
        setChatConfigFn={setChatConfigFn}
        onConversationSettingsChange={(settings) => {
          if (!sessionId) {
            pendingConversationSettingsRef.current = settings;
          } else {
            setConversationSettings(settings);
          }
        }}
        initialConversationSettings={conversationSettings}
        hasWorkflowSession={hasWorkflowSession}
        knowledgeRefreshKey={knowledgeRefreshKey}
        embeddingReady={embeddingReady}
        multimodalEmbeddingReady={multimodalEmbeddingReady}
        rerankReady={rerankReady}
        disabledReason={
          workflowDefinitionChanged
            ? t("chat.workflowDefinitionChanged")
            : autoRunning
              ? t("chat.autoAdvanceRunning")
              : chatDisabledReason
        }
        disabledDescription={
          autoRunning || workflowDefinitionChanged ? undefined : chatDisabledDescription
        }
        disabledAction={
          autoRunning || workflowDefinitionChanged ? undefined : chatDisabledAction
        }
      />
      </div>
      {isTaskPanelRestoreVisible && (
        <button
          type="button"
          className="task-panel-restore-btn"
          onClick={() => setIsTaskPanelCollapsed(false)}
          title={t("taskCenter.panelTitle")}
          >
            <span className="task-panel-restore-icon">&#8249;</span>
            <span className="task-panel-restore-label">
              {taskDisplayCount > 0
              ? `${t("taskCenter.panelTitle")} (${taskDisplayCount})`
              : t("taskCenter.panelTitle")}
          </span>
          </button>
        )}
        {((hasTaskPanelContent && !workflowPanelExpanded && !isTaskPanelCollapsed) || workflowPanelExpanded) && (
        <div
          className={`right-box${!developerModeActive && !workflowPanelExpanded ? " right-box--ordinary" : ""}${workflowPanelExpanded ? " right-box--expanded-tab" : ""}${workflowPanelExpanded && expandedRailTab !== "tasks" ? " right-box--tab-hidden" : ""}`}
          style={!workflowPanelExpanded && panelWidth ? { width: panelWidth, minWidth: panelWidth } : undefined}
          aria-hidden={workflowPanelExpanded && expandedRailTab !== "tasks"}
        >
          <div className="right-box-resize-handle" onMouseDown={onPanelResizeStart} />
          <TaskCenter
            sessionId={sessionId}
            onClose={workflowPanelExpanded ? undefined : () => setIsTaskPanelCollapsed(true)}
            showHeader={!workflowPanelExpanded}
            developerMode={developerModeActive}
            workflowSteps={workflowSession?.steps}
          />
        </div>
      )}
    </div>
  );
};

export default ChatLayout;
