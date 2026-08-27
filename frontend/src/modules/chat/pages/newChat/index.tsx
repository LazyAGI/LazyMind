import { useState, useEffect, useRef, useCallback, useMemo } from "react";
import "./index.scss";
import DisclaimerIcon from "../../assets/icons/disclaimer_icon.svg?react";
import WarningIcon from "../../assets/icons/warning.svg?react";
import ChatInput, {
  ChatInputImperativeProps,
  type ShowcaseSelection,
} from "@/modules/chat/components/ChatInput";
import ChatLayout from "../chatLayout";
import { ChatConfig } from "@/modules/chat/components/ChatConfigs";
import { Button, Tooltip, message } from "antd";
import {
  CHAT_NEW_RUN_IN_BACKGROUND_KEY,
  CHAT_RESUME_CONVERSATION_KEY,
  CHAT_SELECT_CONVERSATION_EVENT,
} from "@/modules/chat/constants/chat";
import { allowedUploadTypes } from "@/modules/chat/components/ImageUpload";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useChatModelProviderGuard } from "@/modules/chat/hooks/useChatModelProviderGuard";
import { AgentAppsAuth } from "@/components/auth";
import { localizeErrorCode } from "@/components/request";
import PreferenceConfigNotice from "@/modules/chat/components/PreferenceConfigNotice";
import {
  ConversationSettingsApi,
  FALLBACK_CHAT_ENTRY_DEFAULTS,
  parseChatEntryDefaults,
  type ChatEntryDefaults,
  type ConversationRuntimeSettings,
} from "@/modules/chat/utils/request";
import { RightOutlined, ScheduleOutlined } from "@ant-design/icons";
import { useChatThinkStore } from "@/modules/chat/store/chatThink";
import FeaturedCases from "@/modules/showcase/FeaturedCases";
import {
  listShowcaseCases,
  matchesShowcaseEntryType,
  type ShowcaseCase,
  type ShowcaseCaseTask,
  type ShowcaseEntryType,
} from "@/modules/showcase/api";
import { useFeaturedCapabilityBinding } from "@/modules/showcase/useFeaturedCapabilityBinding";
import { getKnowledgeMarketItem } from "@/modules/knowledge/api/knowledgeMarket";

const FULL_CAPABILITY_TASK_VALUE = "__full_capability__";
const QUICK_SELECT_CAPABILITY_LIMIT = 5;

function readRunInBackgroundMode() {
  try {
    return sessionStorage.getItem(CHAT_NEW_RUN_IN_BACKGROUND_KEY) === "1";
  } catch {
    return false;
  }
}

function hasResumedConversation() {
  try {
    return Boolean(sessionStorage.getItem(CHAT_RESUME_CONVERSATION_KEY));
  } catch {
    return false;
  }
}

function persistRunInBackgroundMode(enabled: boolean) {
  try {
    sessionStorage.setItem(CHAT_NEW_RUN_IN_BACKGROUND_KEY, enabled ? "1" : "0");
  } catch {
    // ignore storage errors
  }
}

export function resolveChatEntryDefault(
  runInBackground: boolean,
  defaults: ChatEntryDefaults,
) {
  return defaults[runInBackground ? "new_task" : "quick_question"];
}

const NewChatPage = () => {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage || i18n.language;
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const modelProviderGuard = useChatModelProviderGuard();
  const isAdmin = AgentAppsAuth.getUserInfo()?.role === 'system-admin';
  const getGreeting = () => {
    const currentHour = new Date().getHours();
    return currentHour < 12 ? t("chat.greetingMorning") : t("chat.greetingAfternoon");
  };
  const [inputValue, setInputValue] = useState("");
  const [isChatContent, setIsChatContent] = useState(false);
  const [chatConfig, setChatConfig] = useState<ChatConfig>({});
  const [chatLayoutMounted, setChatLayoutMounted] = useState(false);
  const [runInBackground, setRunInBackground] = useState(readRunInBackgroundMode);
  const [entryDefaults, setEntryDefaults] = useState<ChatEntryDefaults>(
    FALLBACK_CHAT_ENTRY_DEFAULTS,
  );
  const [entryDefaultsStatus, setEntryDefaultsStatus] = useState<
    "loading" | "ready" | "error"
  >("loading");
  const entryDefaultsRef = useRef(entryDefaults);
  entryDefaultsRef.current = entryDefaults;
  const freshEntryRef = useRef(!hasResumedConversation());
  const [welcomeKnowledgeRefreshKey, setWelcomeKnowledgeRefreshKey] =
    useState(0);
  const newChatInputRef = useRef<ChatInputImperativeProps>(null);
  // Stash workflow settings changed in the welcome-screen ChatInput before a conversation is created.
  const [pendingConversationSettings, setPendingConversationSettings] =
    useState<ConversationRuntimeSettings>(() => ({
      ...resolveChatEntryDefault(
        readRunInBackgroundMode(),
        FALLBACK_CHAT_ENTRY_DEFAULTS,
      ).conversation_settings,
    }));

  const [isDragging, setIsDragging] = useState(false);
  const [showcaseCases, setShowcaseCases] = useState<ShowcaseCase[]>([]);
  const [showcaseCasesLoading, setShowcaseCasesLoading] = useState(true);
  const showcaseCaseId = searchParams.get("showcase_case");
  const showcaseTaskId = searchParams.get("showcase_task");
  const showcaseEntryType: ShowcaseEntryType = runInBackground ? "work" : "chat";
  const availableShowcaseCases = useMemo(
    () => showcaseCases.filter((item) => matchesShowcaseEntryType(item.type, showcaseEntryType)),
    [showcaseCases, showcaseEntryType],
  );
  const quickSelectShowcaseCases = useMemo(
    () => availableShowcaseCases
      .filter((item) => item.featured)
      .slice(0, QUICK_SELECT_CAPABILITY_LIMIT),
    [availableShowcaseCases],
  );
  const showcaseCase = useMemo(
    () => availableShowcaseCases.find((item) => item.id === showcaseCaseId) ?? null,
    [availableShowcaseCases, showcaseCaseId],
  );
  const resolveShowcasePrompt = useCallback((
    item: ShowcaseCase,
    task?: ShowcaseCaseTask,
  ) => {
    if (task) return task.prompt;
    if (item.tasks.length === 1) return item.tasks[0].prompt;
    const capabilities = item.tasks
      .map((candidate) => candidate.title)
      .join(t("showcase.capabilitySeparator"));
    return t("showcase.fullCapabilityPrompt", { title: item.title, capabilities });
  }, [t]);
  const selectedShowcaseTask = showcaseTaskId
    ? showcaseCase?.tasks.find((task) => task.id === showcaseTaskId)
    : undefined;
  const selectedShowcasePrompt = useMemo(
    () => showcaseCase ? resolveShowcasePrompt(showcaseCase, selectedShowcaseTask) : "",
    [resolveShowcasePrompt, selectedShowcaseTask, showcaseCase],
  );
  const {
    mentions: showcaseBoundMentions,
    retry: retryShowcaseCapability,
    status: showcaseCapabilityStatus,
  } = useFeaturedCapabilityBinding(showcaseCase);
  const officialKnowledgeId = searchParams.get("officialKnowledge");

  const loadEntryDefaults = useCallback(async (signal?: AbortSignal) => {
    setEntryDefaultsStatus("loading");
    try {
      const response = await ConversationSettingsApi().getChatSettings({ signal });
      if (signal?.aborted) return;
      setEntryDefaults(parseChatEntryDefaults(response.data));
      setEntryDefaultsStatus("ready");
    } catch {
      if (!signal?.aborted) {
        setEntryDefaults(FALLBACK_CHAT_ENTRY_DEFAULTS);
        setEntryDefaultsStatus("error");
      }
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void loadEntryDefaults(controller.signal);
    return () => controller.abort();
  }, [loadEntryDefaults]);

  useEffect(() => {
    if (isChatContent || !freshEntryRef.current) return;
    const profile = resolveChatEntryDefault(runInBackground, entryDefaults);
    useChatThinkStore.getState().setThinkingDepth(profile.thinking_depth);
    setPendingConversationSettings({ ...profile.conversation_settings });
  }, [entryDefaults, isChatContent, runInBackground]);
  const dragCounterRef = useRef(0);
  const entryDefaultsLoading = entryDefaultsStatus === "loading";
  const entryDefaultsUnavailable = entryDefaultsStatus !== "ready";
  const isChatDisabled = !modelProviderGuard.canChat;
  const isShowcaseCapabilityPreparing = showcaseCapabilityStatus === "preparing";
  const showcaseCapabilityFailed = showcaseCapabilityStatus === "failed";
  const isWelcomeInputDisabled =
    isChatDisabled
    || isShowcaseCapabilityPreparing
    || showcaseCapabilityFailed
    || entryDefaultsUnavailable;
  const runtimeInitializingReason = runInBackground
    ? t("runtime.aiServiceInitializingWorkflow")
    : t("runtime.aiServiceInitializingMessage");
  const chatDisabledReason = modelProviderGuard.needsModelProviderConfig
    ? t("chat.modelProviderRequiredTitle")
    : modelProviderGuard.status === "error"
      ? localizeErrorCode("2000509")
      : modelProviderGuard.isRuntimeInitializing
        ? runtimeInitializingReason
        : modelProviderGuard.isChecking
          ? t("chat.modelProviderChecking")
          : t("chat.modelProviderRequiredTitle");
  const chatDisabledDescription = modelProviderGuard.needsModelProviderConfig
    ? t("chat.modelProviderRequiredDesc")
    : modelProviderGuard.status === "error"
      ? localizeErrorCode("2000509")
      : modelProviderGuard.isRuntimeInitializing
        ? undefined
        : modelProviderGuard.isChecking
          ? t("chat.modelProviderCheckingDesc")
          : t("chat.modelProviderRequiredDesc");
  const chatDisabledAction = modelProviderGuard.isChecking ? null : modelProviderGuard.status === "error" ? (
    <Button size="small" onClick={() => void modelProviderGuard.refresh()}>
      {t("chat.retryCheckModelProvider")}
    </Button>
  ) : (
    <Button type="primary" size="small" onClick={() => navigate("/settings?section=models")}>
      {t("chat.goConfigureModelProvider")}
    </Button>
  );

  // Warn when knowledge base is selected but embedding is not ready.
  const hasKnowledgeBase = Boolean(chatConfig.knowledgeBaseId?.length);
  const showEmbeddingWarning = hasKnowledgeBase && modelProviderGuard.embeddingReady === false;
  // Warn when VLM is not configured (informational only, does not block any feature).
  const showVlmWarning = modelProviderGuard.vlmReady === false;
  const vlmWarningText = isAdmin ? t("chat.vlmNotReadyWarningAdmin") : t("chat.vlmNotReadyWarning");
  const mergeVlmWarningIntoDisabledNotice = showVlmWarning && modelProviderGuard.status === "missing";
  const chatDisabledDescriptionContent = mergeVlmWarningIntoDisabledNotice ? (
    <>
      <span>{chatDisabledDescription}</span>
      <span>{vlmWarningText}</span>
    </>
  ) : chatDisabledDescription;
  const hideSharedNoticeForRuntime =
    modelProviderGuard.isRuntimeInitializing &&
    !modelProviderGuard.needsModelProviderConfig &&
    modelProviderGuard.status !== "error";
  const inputDisabledReason = entryDefaultsLoading
    ? t("settingsPage.tasks.entryDefaultsLoading")
    : entryDefaultsStatus === "error"
      ? t("settingsPage.tasks.entryDefaultsLoadFailed")
      : isShowcaseCapabilityPreparing
        ? t("showcase.preparingCapability")
        : showcaseCapabilityFailed
          ? t("showcase.capabilityPrepareFailed")
          : hideSharedNoticeForRuntime
            ? undefined
            : chatDisabledReason;
  const inputDisabledDescription =
    entryDefaultsUnavailable
    || isShowcaseCapabilityPreparing
    || showcaseCapabilityFailed
    || hideSharedNoticeForRuntime
    ? undefined
    : chatDisabledDescriptionContent;
  const inputDisabledAction = showcaseCapabilityFailed ? (
    <Button size="small" onClick={retryShowcaseCapability}>
      {t("showcase.retryCapabilityPrepare")}
    </Button>
  ) : entryDefaultsUnavailable || isShowcaseCapabilityPreparing || hideSharedNoticeForRuntime
    ? undefined
    : chatDisabledAction;
  const hidePreferenceConfigNotice =
    !modelProviderGuard.isConfigurationReady;

  useEffect(() => {
    if (showcaseCapabilityStatus === "failed") {
      message.error(t("showcase.capabilityPrepareFailed"));
    }
  }, [showcaseCapabilityStatus, t]);

  useEffect(() => {
    if (!isChatContent) {
      newChatInputRef.current?.clearFiles();
      setInputValue("");
    }
  }, [isChatContent]);

  useEffect(() => {
    const controller = new AbortController();
    setShowcaseCasesLoading(true);
    listShowcaseCases({}, { signal: controller.signal })
      .then((response) => setShowcaseCases(response.cases ?? []))
      .catch(() => {
        if (!controller.signal.aborted) {
          setShowcaseCases([]);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setShowcaseCasesLoading(false);
        }
      });
    return () => controller.abort();
  }, [locale]);

  useEffect(() => {
    if (!showcaseCaseId) {
      if (!officialKnowledgeId) setInputValue("");
      return;
    }
    if (showcaseCasesLoading) return;
    if (!showcaseCase) {
      setInputValue("");
      setSearchParams({}, { replace: true });
      return;
    }
    if (showcaseTaskId && !selectedShowcaseTask) {
      setSearchParams({ showcase_case: showcaseCase.id }, { replace: true });
    }
    setInputValue(selectedShowcasePrompt);
  }, [
    officialKnowledgeId,
    selectedShowcaseTask,
    selectedShowcasePrompt,
    setSearchParams,
    showcaseCase,
    showcaseCaseId,
    showcaseCasesLoading,
    showcaseTaskId,
  ]);

  useEffect(() => {
    if (!officialKnowledgeId || showcaseCaseId) return;

    const controller = new AbortController();
    getKnowledgeMarketItem(officialKnowledgeId, { signal: controller.signal })
      .then((item) => {
        if (!item.online_access_url) {
          message.info(t("knowledge.onlineQueryUnavailable"));
          return;
        }
        setInputValue(
          t("knowledge.onlineQueryPrompt", {
            name: item.name,
            url: item.online_access_url,
          }),
        );
      })
      .catch(() => {
        // The shared request interceptor displays the localized error.
      });

    return () => controller.abort();
  }, [locale, officialKnowledgeId, showcaseCaseId, t]);

  const selectShowcaseTask = (item: ShowcaseCase, taskId?: string | null) => {
    const task = taskId && taskId !== FULL_CAPABILITY_TASK_VALUE
      ? item.tasks.find((candidate) => candidate.id === taskId)
      : undefined;
    setInputValue(resolveShowcasePrompt(item, task));
    setSearchParams(task
      ? { showcase_case: item.id, showcase_task: task.id }
      : { showcase_case: item.id });
  };

  const handleFeaturedCaseTry = (item: ShowcaseCase) => {
    selectShowcaseTask(item);
  };

  const handleWelcomeInputChange = (value: string) => {
    setInputValue(value);
  };

  const handleSetIsChatContent = (value: boolean) => {
    freshEntryRef.current = !value;
    if (value && !chatLayoutMounted) {
      setChatLayoutMounted(true);
    }
    if (!value) {
      const nextRunInBackground = readRunInBackgroundMode();
      setRunInBackground(nextRunInBackground);
      setWelcomeKnowledgeRefreshKey((key) => key + 1);
      // Reset pending settings and KB config so a fresh new conversation starts clean.
      setPendingConversationSettings({
        ...resolveChatEntryDefault(
          nextRunInBackground,
          entryDefaultsRef.current,
        ).conversation_settings,
      });
      setChatConfig({});
      setSearchParams({}, { replace: true });
    }
    setIsChatContent(value);
  };

  useEffect(() => {
    if (
      sessionStorage.getItem(CHAT_RESUME_CONVERSATION_KEY) &&
      !chatLayoutMounted
    ) {
      freshEntryRef.current = false;
      setChatLayoutMounted(true);
      setIsChatContent(true);
    }
  }, [chatLayoutMounted]);

  useEffect(() => {
    const handleConversationSelect = (event: Event) => {
      const detail = (
        event as CustomEvent<{
          conversationId?: string;
          runInBackground?: boolean;
        }>
      ).detail;
      const conversationId = detail?.conversationId || "";
      if (!conversationId) {
        freshEntryRef.current = true;
        const nextRunInBackground =
          detail?.runInBackground ?? readRunInBackgroundMode();
        setRunInBackground(nextRunInBackground);
        persistRunInBackgroundMode(nextRunInBackground);
        setWelcomeKnowledgeRefreshKey((key) => key + 1);
        setIsChatContent(false);
        setChatConfig({});
        setPendingConversationSettings({
          ...resolveChatEntryDefault(
            nextRunInBackground,
            entryDefaultsRef.current,
          ).conversation_settings,
        });
        return;
      }
      freshEntryRef.current = false;
      setRunInBackground(false);
      persistRunInBackgroundMode(false);
      setChatLayoutMounted(true);
      setIsChatContent(true);
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
  }, []);

  const isFileTypeSupported = (file: File): boolean => {
    const ext = file.name.substring(file.name.lastIndexOf(".")).toLowerCase();
    return allowedUploadTypes.includes(ext);
  };

  const handleDragEnter = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    if (isWelcomeInputDisabled) {
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

    if (isWelcomeInputDisabled) {
      message.warning(inputDisabledReason || chatDisabledReason);
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

    newChatInputRef.current?.uploadFiles(files);
  };

  const showcaseSelection: ShowcaseSelection = {
    skill: {
      value: showcaseCase?.id,
      selectedLabel: showcaseCase?.title,
      options: quickSelectShowcaseCases.map((item) => ({
        value: item.id,
        label: item.title,
        description: `${item.category}${t("showcase.capabilitySeparator")}${item.output_label}`,
      })),
      ariaLabel: t("showcase.selectedSkill"),
      placeholder: t("showcase.chooseSkill"),
      heading: t("showcase.quickSelectTitle"),
      subheading: t("showcase.quickSelectDescription"),
      moreLabel: t("showcase.exploreMoreCapabilities"),
      onMore: () => navigate(`/agent/chat/cases?type=${showcaseEntryType}`),
      disabled: showcaseCasesLoading || availableShowcaseCases.length === 0,
      onChange: (skillId) => {
        const item = availableShowcaseCases.find((candidate) => candidate.id === skillId);
        if (item) selectShowcaseTask(item);
      },
    },
    task: showcaseCase
      ? {
          value: selectedShowcaseTask?.id ?? FULL_CAPABILITY_TASK_VALUE,
          selectedLabel: selectedShowcaseTask?.title ?? t("showcase.fullCapability"),
          options: [
            {
              value: FULL_CAPABILITY_TASK_VALUE,
              label: t("showcase.fullCapability"),
            },
            ...(showcaseCase.tasks.length > 1
              ? showcaseCase.tasks.map((task) => ({
                  value: task.id,
                  label: task.title,
                  description: task.description,
                }))
              : []),
          ],
          ariaLabel: t("showcase.chooseTask"),
          heading: t("showcase.selectFunction"),
          valuePrefix: t("showcase.functionPrefix"),
          disabled: showcaseCase.tasks.length <= 1,
          onChange: (taskId) => selectShowcaseTask(showcaseCase, taskId),
        }
      : undefined,
  };
  const shouldShowFeaturedCases =
    inputValue.trim().length === 0 || Boolean(showcaseCase);

  return (
    <div className="new-chat-page">
      {}
      {chatLayoutMounted && (
        <div style={{ display: isChatContent ? "block" : "none" }}>
          <ChatLayout
            setIsChatContent={handleSetIsChatContent}
            setChatConfigFn={setChatConfig}
            initchatConfig={chatConfig}
            canChat={modelProviderGuard.canChat}
            embeddingReady={modelProviderGuard.embeddingReady}
            multimodalEmbeddingReady={modelProviderGuard.multimodalEmbeddingReady}
            rerankReady={modelProviderGuard.rerankReady}
            chatDisabledReason={inputDisabledReason}
            chatDisabledDescription={inputDisabledDescription}
            chatDisabledAction={inputDisabledAction}
            initPendingConversationSettings={pendingConversationSettings}
          />
        </div>
      )}
      <div
        style={{ display: isChatContent ? "none" : "block" }}
        onDragEnter={handleDragEnter}
        onDragLeave={handleDragLeave}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
      >
        <div className="new-chat-container">
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
          <div className="new-chat-main">
            <div className="chat-content-container">
              <div className="bg"></div>
              <div className="chat-content">
                <div className="greeting-section">
                  <h1 className="greeting-text">
                    {getGreeting()}
                    {t(runInBackground ? "chat.taskGreetingSuffix" : "chat.greetingSuffix")}
                  </h1>
                </div>

                <div className="input-section">
                  {runInBackground ? (
                    <button
                      type="button"
                      className="task-mode-notice"
                      onClick={() => navigate("/task-center")}
                      aria-label={t("chat.taskModeNoticeAction")}
                    >
                      <span className="task-mode-notice-icon" aria-hidden="true">
                        <ScheduleOutlined />
                      </span>
                      <span className="task-mode-notice-text">
                        {t("chat.taskModeNotice")}
                      </span>
                      <RightOutlined className="task-mode-notice-arrow" aria-hidden="true" />
                    </button>
                  ) : null}
                  {entryDefaultsStatus === "loading" ? (
                    <div className="model-provider-warning-banner" role="status">
                      <span className="model-provider-warning-text">
                        {t("settingsPage.tasks.entryDefaultsLoading")}
                      </span>
                    </div>
                  ) : null}
                  {entryDefaultsStatus === "error" ? (
                    <div className="model-provider-warning-banner" role="alert">
                      <span className="model-provider-warning-text">
                        {t("settingsPage.tasks.entryDefaultsLoadFailed")}
                      </span>
                      <Button
                        size="small"
                        className="model-provider-warning-action"
                        onClick={() => void loadEntryDefaults()}
                      >
                        {t("settingsPage.tasks.retryEntryDefaults")}
                      </Button>
                    </div>
                  ) : null}
                  {showEmbeddingWarning ? (
                    <div className="model-provider-warning-banner embedding-warning-banner" role="alert">
                      <span className="model-provider-warning-text">
                        {t("chat.embeddingNotReadyWarning")}
                      </span>
                    </div>
                  ) : null}
                  {showVlmWarning && !mergeVlmWarningIntoDisabledNotice ? (
                    <div className="model-provider-warning-banner vlm-warning-banner" role="alert">
                      <span className="model-provider-warning-text">
                        {vlmWarningText}
                      </span>
                      <Button
                        type="primary"
                        size="small"
                        className="model-provider-warning-action"
                        onClick={() => navigate("/settings?section=models")}
                      >
                        {t("knowledge.goToConfig")}
                      </Button>
                    </div>
                  ) : null}
                  {modelProviderGuard.isRuntimeInitializing ? (
                    <div
                      className="model-provider-warning-banner"
                      role="status"
                    >
                      <span className="model-provider-warning-text">
                        {runtimeInitializingReason}
                      </span>
                    </div>
                  ) : null}
                  <PreferenceConfigNotice
                    hidden={hidePreferenceConfigNotice}
                  />
                  {showcaseCase ? (
                    <div className="showcase-template-banner" role="status">
                      <div>
                        <strong>{t("showcase.loadedCase", { title: showcaseCase.title })}</strong>
                        <span>
                          {showcaseCase.attachment_hint
                            ? t("showcase.uploadSuggestion", { hint: showcaseCase.attachment_hint })
                            : t("showcase.promptReady")}
                        </span>
                      </div>
                      <button
                        type="button"
                        onClick={() => {
                          setInputValue("");
                          newChatInputRef.current?.clearFiles();
                          setSearchParams({}, { replace: true });
                        }}
                      >
                        {t("showcase.clearCase")}
                      </button>
                    </div>
                  ) : null}
                  <ChatInput
                    ref={newChatInputRef}
                    value={inputValue}
                    onChange={handleWelcomeInputChange}
                    openHistory={() => handleSetIsChatContent(true)}
                    openNewChat={() => handleSetIsChatContent(false)}
                    isChatContent={isChatContent}
                    showHistoryList={false}
                    showHistoryButton={false}
                    knowledgeRefreshKey={welcomeKnowledgeRefreshKey}
                    configResetKey={welcomeKnowledgeRefreshKey}
                    setIsChatContent={(value) => {
                      if (value) {
                        setInputValue("");
                        setSearchParams({}, { replace: true });
                      }
                      handleSetIsChatContent(value);
                    }}
                    chatConfig={chatConfig}
                    setChatConfig={setChatConfig}
                    disabled={isWelcomeInputDisabled}
                    embeddingReady={modelProviderGuard.embeddingReady}
                    multimodalEmbeddingReady={modelProviderGuard.multimodalEmbeddingReady}
                    rerankReady={modelProviderGuard.rerankReady}
                    disabledReason={inputDisabledReason}
                    disabledDescription={inputDisabledDescription}
                    disabledAction={inputDisabledAction}
                    placeholder={
                      runInBackground
                        ? t("chat.taskInputPlaceholder")
                        : undefined
                    }
                    onConversationSettingsChange={(settings) => {
                      setPendingConversationSettings(settings);
                    }}
                    initialConversationSettings={pendingConversationSettings}
                    runInBackground={runInBackground}
                    showcaseSelection={showcaseSelection}
                    boundMentions={showcaseBoundMentions}
                    showPromptSuggestions={!showcaseCase}
                  />
                  {shouldShowFeaturedCases ? (
                    <FeaturedCases
                      type={showcaseEntryType}
                      items={showcaseCases}
                      isLoading={showcaseCasesLoading}
                      onTry={handleFeaturedCaseTry}
                    />
                  ) : null}
                </div>
              </div>
            </div>
          </div>

          <div className="disclaimer-section">
            <div className="tip-box">
              <DisclaimerIcon />
              <span className="disclaimer-text">
                {t("chat.disclaimerAI")}
              </span>
            </div>
            <div className="tip-box">
              <WarningIcon />
              <span className="disclaimer-text">
                {t("chat.disclaimerSecurity")}
                <Tooltip title={<span>{t("chat.disclaimerTooltip")}</span>}>
                  <span style={{ cursor: "pointer", marginLeft: 4 }}>
                    {t("chat.disclaimerSensitive")}
                  </span>
                </Tooltip>
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

export default NewChatPage;
