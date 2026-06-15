import { FC, type ReactNode, useRef, useState, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { message } from "antd";
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
} from "@/modules/chat/utils/request";
import { useChatMessageStore } from "@/modules/chat/store/chatMessage";
import {
  useModelSelectionStore,
  MODEL_API_LABELS,
  parseModelSelectionFromModels,
} from "@/modules/chat/store/modelSelection";
import { allowedUploadTypes } from "@/modules/chat/components/ImageUpload";
import {
  CHAT_RESUME_CONVERSATION_KEY,
  CHAT_SELECT_CONVERSATION_EVENT,
} from "@/modules/chat/constants/chat";
import { buildChatMessageListFromHistory } from "@/modules/chat/utils/message";
import { buildEnvironmentContext } from "@/modules/chat/utils/environment";
import TaskCenter from "@/modules/chat/components/TaskCenter";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import type { SubAgentTask } from "@/modules/chat/store/taskCenter";

// Stable empty reference to avoid returning a fresh array from the zustand
// selector on every render, which (with useSyncExternalStore) would trigger an
// infinite re-render loop (React error #185).
const EMPTY_TASKS: SubAgentTask[] = [];

interface IChatLayoutProps {
  setIsChatContent: (isChatContent: boolean) => void;
  initchatConfig: ChatConfig;
  setChatConfigFn: (val: ChatConfig) => void;
  canChat: boolean;
  embeddingReady?: boolean | null;
  multimodalEmbeddingReady?: boolean | null;
  rerankReady?: boolean | null;
  chatDisabledReason?: string;
  chatDisabledDescription?: string;
  chatDisabledAction?: ReactNode;
}

const ChatLayout: FC<IChatLayoutProps> = (props) => {
  const { t } = useTranslation();
  const {
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
  } = props;
  const [sessionId, setSessionId] = useState("");
  const [chatConfig, setChatConfig] = useState<ChatConfig>(
    initchatConfig || {},
  );
  const [knowledgeRefreshKey, setKnowledgeRefreshKey] = useState(0);
  const [isTaskPanelCollapsed, setIsTaskPanelCollapsed] = useState(false);
  const [panelWidth, setPanelWidth] = useState<number>(0); // 0 = use CSS default
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
  const [isRestoringConversation, setIsRestoringConversation] = useState(() => {
    try {
      return Boolean(sessionStorage.getItem(CHAT_RESUME_CONVERSATION_KEY));
    } catch {
      return false;
    }
  });

  const { pendingMessage, clearPendingMessage } = useChatMessageStore();
  const { getModelSelection, setModelSelection } = useModelSelectionStore();

  const chatRef = useRef<ChatImperativeProps>(null);

  const tasks = useTaskCenterStore((s) =>
    sessionId ? s.tasksByConversation[sessionId] ?? EMPTY_TASKS : EMPTY_TASKS,
  );
  const loadConversationTasks = useTaskCenterStore(
    (s) => s.loadConversationTasks,
  );

  useEffect(() => {
    if (sessionId) {
      loadConversationTasks(sessionId);
    }
  }, [sessionId, loadConversationTasks]);

  // Auto-expand the task panel the first time a SubAgent task appears in the current session.
  const prevTasksLengthRef = useRef(0);
  useEffect(() => {
    const prev = prevTasksLengthRef.current;
    prevTasksLengthRef.current = tasks.length;
    if (prev === 0 && tasks.length > 0) {
      setIsTaskPanelCollapsed(false);
    }
  }, [tasks.length]);

  const [isDragging, setIsDragging] = useState(false);
  const dragCounterRef = useRef(0);

  useEffect(() => {
    setChatConfigFn(initchatConfig);
    setChatConfig(initchatConfig);
  }, [initchatConfig]);

  useEffect(() => {
    if (pendingMessage) {
      const timer = setTimeout(() => {
        chatRef.current?.sendMessage(pendingMessage);
        clearPendingMessage();
      }, 100);

      return () => clearTimeout(timer);
    }
    return undefined;
  }, [pendingMessage, clearPendingMessage]);

  useEffect(() => {
    const conversationId = sessionStorage.getItem(CHAT_RESUME_CONVERSATION_KEY);
    if (!conversationId) {
      return;
    }
    setIsRestoringConversation(true);
    const resolveConversationId = (id: string): Promise<string> => {
      if (!id || !id.startsWith("temp_")) {
        return Promise.resolve(id);
      }
      return ChatServiceApi()
        .conversationServiceListConversations({ pageToken: "", pageSize: 5 })
        .then((listRes) => {
          const conversations = listRes?.data?.conversations ?? [];
          const latest = conversations[0];
          return latest?.conversation_id ?? id;
        })
        .catch(() => id);
    };

    resolveConversationId(conversationId)
      .then((resolvedId) => {
        if (resolvedId !== conversationId) {
          sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, resolvedId);
        }
        return ChatServiceApi()
          .conversationServiceGetChatStatus({ conversationId: resolvedId })
          .then((res) => ({
            resolvedId,
            isGenerating: !!res.data?.is_generating,
          }));
      })
      .catch(() => ({ resolvedId: conversationId, isGenerating: false }))
      .then(({ resolvedId, isGenerating }) => {
        setIsChatContent(true);
        return ChatServiceApi()
          .conversationServiceGetConversationDetail({
            conversation: resolvedId,
          })
          .then((detailRes) =>
            ChatServiceApi()
              .conversationServiceGetConversationHistory({
                name: resolvedId,
              })
              .then((historyRes) => ({
                detailRes,
                historyRes,
                resolvedId,
                isGenerating,
              })),
          );
      })
      .then(({ detailRes, historyRes, resolvedId, isGenerating }) => {
        const conversation = detailRes.data.conversation;
        const history = historyRes.data.history;
        const tempData = {
          knowledgeBaseId: conversation?.search_config?.dataset_list
            ?.map((d: any) => d.id)
            .filter((id: string) => !!id),
          creators: conversation?.search_config?.creators,
          tags: conversation?.search_config?.tags,
          databaseBaseId: conversation?.search_config?.database_ids?.[0],
        };
        setChatConfig(tempData);
        setChatConfigFn(tempData);
        setKnowledgeRefreshKey((key) => key + 1);
        setConversationId(resolvedId);

        const modelSelection = parseModelSelectionFromModels(
          (conversation as any)?.models,
        );
        setModelSelection(resolvedId, modelSelection);

        const list = buildChatMessageListFromHistory(history, {
          isGenerating,
        });
        chatRef.current?.replaceMessageList(resolvedId, list);
        if (isGenerating) {
          chatRef.current?.openResumeSSE?.(resolvedId);
        } else {
          sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
        }
        setIsRestoringConversation(false);
      })
      .catch(() => {
        sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
        setIsRestoringConversation(false);
      });
  }, []);

  function onOpenSSE(
    input: Query[],
    action: ChatConversationsRequestActionEnum,
    callbacks: Record<string, (e: CustomEvent) => void>,
  ) {
    const modelSelection = getModelSelection(sessionId);

    const hasUploadedFiles = input?.some(
      (q: Query) => q.input_type === "image" || q.input_type === "file",
    );
    const useKnowledgeBase =
      modelSelection === "value_engineering" || modelSelection === "both";
    const datasetList =
      hasUploadedFiles || !useKnowledgeBase
        ? []
        : chatConfig?.knowledgeBaseId?.length
          ? chatConfig.knowledgeBaseId.map((k) => ({ id: k }))
          : [];

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
        models:
          modelSelection === "both"
            ? [MODEL_API_LABELS.lazyMind, MODEL_API_LABELS.deepSeek]
            : modelSelection === "value_engineering"
              ? [MODEL_API_LABELS.lazyMind]
              : [MODEL_API_LABELS.deepSeek],
        // enable_thinking: think ? true : false,
        stream: true,
        input,
        mode: "auto",
        create_time: new Date().toISOString(),
        environment_context: buildEnvironmentContext(),
      }),
      callbacks,
    });
  }

  function onOpenResumeSSE(
    conversationId: string,
    callbacks: Record<string, (e: CustomEvent) => void>,
  ) {
    return new SSE(CHAT_RESUME_STREAM_URL, {
      method: Method.POST,
      headers: {
        "Content-Type": "application/json",
        Accept: "text/event-stream",
        ...AgentAppsAuth.getAuthHeaders(),
      },
      timeout: 1800000,
      payload: JSON.stringify({ conversation_id: conversationId }),
      callbacks,
    });
  }

  const sessionIdRef = useRef(sessionId);
  sessionIdRef.current = sessionId;

  const setConversationId = useCallback((id: string) => {
    if (id === sessionIdRef.current) return;
    setSessionId(id);
    window.dispatchEvent(
      new CustomEvent(CHAT_SELECT_CONVERSATION_EVENT, {
        detail: { conversationId: id, source: "chat" },
      }),
    );
  }, []);

  const loadConversation = useCallback((conversationId: string) => {
    setIsRestoringConversation(true);
    ChatServiceApi()
      .conversationServiceGetConversationDetail({
        conversation: conversationId,
      })
      .then((detailRes) =>
        ChatServiceApi()
          .conversationServiceGetConversationHistory({
            name: conversationId,
          })
          .then((historyRes) => ({ detailRes, historyRes })),
      )
      .then(({ detailRes, historyRes }) => {
        const conversation = detailRes.data.conversation;
        const resolvedId = conversation?.conversation_id || conversationId;
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

        const modelSelection = parseModelSelectionFromModels(
          (conversation as any)?.models,
        );
        if (resolvedId) {
          setModelSelection(resolvedId, modelSelection);
        }

        setConversationId(resolvedId);

        const history = historyRes.data.history;
        const list = buildChatMessageListFromHistory(history, {
          fallbackCreateTime: "xxx-xxx-xxx",
        });
        chatRef.current?.replaceMessageList(resolvedId, list);
      })
      .finally(() => {
        setIsRestoringConversation(false);
      });
  }, [setConversationId, setChatConfigFn, setModelSelection]);

  useEffect(() => {
    const handleConversationSelect = (event: Event) => {
      const detail =
        (event as CustomEvent<{ conversationId?: string; source?: string }>)
          .detail || {};
      if (detail.source !== "sidebar") {
        return;
      }
      const conversationId = detail.conversationId || "";
      if (!conversationId) {
        setIsRestoringConversation(false);
        chatRef.current?.createNewChat();
        return;
      }
      if (conversationId === sessionId) {
        return;
      }
      setIsChatContent(true);
      loadConversation(conversationId);
    };

    window.addEventListener(
      CHAT_SELECT_CONVERSATION_EVENT,
      handleConversationSelect,
    );
    return () => {
      window.removeEventListener(
        CHAT_SELECT_CONVERSATION_EVENT,
        handleConversationSelect,
      );
    };
  }, [sessionId, setIsChatContent, loadConversation]);

  function parseErrorData(data: string) {
    const dataObject = UIUtils.jsonParser(data) || {};
    return dataObject.message;
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

  return (
    <div
      className="detail-container"
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
      <ChatContainerComponent
        ref={chatRef}
        canChat={canChat}
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
        knowledgeRefreshKey={knowledgeRefreshKey}
        embeddingReady={embeddingReady}
        multimodalEmbeddingReady={multimodalEmbeddingReady}
        rerankReady={rerankReady}
        disabledReason={chatDisabledReason}
        disabledDescription={chatDisabledDescription}
        disabledAction={chatDisabledAction}
      />
      {tasks.length > 0 && isTaskPanelCollapsed && (
        <button
          type="button"
          className="task-panel-restore-btn"
          onClick={() => setIsTaskPanelCollapsed(false)}
          title={t("taskCenter.panelTitle")}
        >
          <span className="task-panel-restore-icon">&#8249;</span>
          <span className="task-panel-restore-label">{t("taskCenter.panelTitle")} ({tasks.length})</span>
        </button>
      )}
      {tasks.length > 0 && !isTaskPanelCollapsed && (
        <div
          className="right-box"
          style={panelWidth ? { width: panelWidth, minWidth: panelWidth } : undefined}
        >
          <div className="right-box-resize-handle" onMouseDown={onPanelResizeStart} />
          <TaskCenter
            sessionId={sessionId}
            onClose={() => setIsTaskPanelCollapsed(true)}
          />
        </div>
      )}
    </div>
  );
};

export default ChatLayout;
