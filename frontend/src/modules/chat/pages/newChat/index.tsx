import { useState, useEffect, useRef } from "react";
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
import type { ConversationWorkflowSettings } from "@/modules/chat/utils/request";
import { RightOutlined, ScheduleOutlined } from "@ant-design/icons";
import { useChatThinkStore } from "@/modules/chat/store/chatThink";
import FeaturedCases from "@/modules/showcase/FeaturedCases";
import { getShowcaseCase, type ShowcaseCase } from "@/modules/showcase/api";

function readRunInBackgroundMode() {
  try {
    return sessionStorage.getItem(CHAT_NEW_RUN_IN_BACKGROUND_KEY) === "1";
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

function getInitialWorkflowSettings(
  runInBackground: boolean,
): ConversationWorkflowSettings | null {
  return runInBackground ? null : { enable_workflow: false };
}

function getShowcasePrompt(
  item: ShowcaseCase,
  taskId: string | null,
  secondaryId?: string,
) {
  const taskPrompt = item.tasks?.find((task) => task.id === taskId)?.prompt;
  const secondaryPrompt = item.secondary_options?.find(
    (option) => option.id === secondaryId,
  )?.prompt;
  const basePrompt = taskPrompt || item.prompt;
  return secondaryPrompt ? `${basePrompt}\n\n${secondaryPrompt}` : basePrompt;
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
  const [welcomeKnowledgeRefreshKey, setWelcomeKnowledgeRefreshKey] =
    useState(0);
  const newChatInputRef = useRef<ChatInputImperativeProps>(null);
  // Stash workflow settings changed in the welcome-screen ChatInput before a conversation is created.
  const [pendingWorkflowSettings, setPendingWorkflowSettings] =
    useState<ConversationWorkflowSettings | null>(() =>
      getInitialWorkflowSettings(readRunInBackgroundMode()),
    );

  const [isDragging, setIsDragging] = useState(false);
  const [showcaseCase, setShowcaseCase] = useState<ShowcaseCase | null>(null);
  const showcaseCaseId = searchParams.get("showcase_case");
  const showcaseTaskId = searchParams.get("showcase_task");
  const [selectedShowcaseSecondaryId, setSelectedShowcaseSecondaryId] = useState<string>();

  useEffect(() => {
    useChatThinkStore
      .getState()
      .setThinkingDepth(runInBackground ? "high" : "medium");
  }, [runInBackground]);
  const dragCounterRef = useRef(0);
  const isChatDisabled = !modelProviderGuard.canChat;
  const isWelcomeInputDisabled = isChatDisabled;
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
    <Button type="primary" size="small" onClick={() => navigate("/model-providers/default-services")}>
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
  const inputDisabledReason = hideSharedNoticeForRuntime
    ? undefined
    : chatDisabledReason;
  const inputDisabledDescription = hideSharedNoticeForRuntime
    ? undefined
    : chatDisabledDescriptionContent;
  const inputDisabledAction = hideSharedNoticeForRuntime
    ? undefined
    : chatDisabledAction;
  const hidePreferenceConfigNotice =
    !modelProviderGuard.isConfigurationReady;

  useEffect(() => {
    if (!isChatContent) {
      newChatInputRef.current?.clearFiles();
      setInputValue("");
    }
  }, [isChatContent]);

  useEffect(() => {
    if (!showcaseCaseId) {
      setShowcaseCase(null);
      setSelectedShowcaseSecondaryId(undefined);
      setInputValue("");
      return;
    }

    const controller = new AbortController();
    setShowcaseCase(null);
    setSelectedShowcaseSecondaryId(undefined);
    setInputValue("");
    getShowcaseCase(showcaseCaseId, { signal: controller.signal })
      .then((item) => {
        const defaultSecondaryId = item.secondary_options?.[0]?.id;
        setShowcaseCase(item);
        setInputValue(getShowcasePrompt(item, showcaseTaskId, defaultSecondaryId));
        setSelectedShowcaseSecondaryId(defaultSecondaryId);
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setShowcaseCase(null);
          setSelectedShowcaseSecondaryId(undefined);
          setInputValue("");
        }
      });

    return () => controller.abort();
  }, [locale, showcaseCaseId, showcaseTaskId]);

  const handleShowcaseSecondaryChange = (secondaryId: string) => {
    setSelectedShowcaseSecondaryId(secondaryId);
    if (showcaseCase) {
      setInputValue(getShowcasePrompt(showcaseCase, showcaseTaskId, secondaryId));
    }
  };

  const handleSetIsChatContent = (value: boolean) => {
    if (value && !chatLayoutMounted) {
      setChatLayoutMounted(true);
    }
    if (!value) {
      const nextRunInBackground = readRunInBackgroundMode();
      setRunInBackground(nextRunInBackground);
      setWelcomeKnowledgeRefreshKey((key) => key + 1);
      // Reset pending settings and KB config so a fresh new conversation starts clean.
      setPendingWorkflowSettings(getInitialWorkflowSettings(nextRunInBackground));
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
        const nextRunInBackground =
          detail?.runInBackground ?? readRunInBackgroundMode();
        setRunInBackground(nextRunInBackground);
        persistRunInBackgroundMode(nextRunInBackground);
        setWelcomeKnowledgeRefreshKey((key) => key + 1);
        setIsChatContent(false);
        setChatConfig({});
        setPendingWorkflowSettings(
          getInitialWorkflowSettings(nextRunInBackground),
        );
        return;
      }
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
      message.warning(chatDisabledReason);
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

  const showcaseSelection: ShowcaseSelection | undefined = showcaseCase
    ? {
        primaryValue: showcaseCase.primary_category || showcaseCase.id,
        primaryLabel: showcaseCase.primary_category || showcaseCase.title,
        primaryAriaLabel: t("showcase.primaryCategory"),
        secondaryValue: selectedShowcaseSecondaryId,
        secondaryOptions: showcaseCase.secondary_options?.map((option) => ({
          value: option.id,
          label: option.label,
          description: option.description,
          prompt: option.prompt,
        })),
        secondaryAriaLabel: t("showcase.secondaryCategory"),
        onSecondaryChange: handleShowcaseSecondaryChange,
      }
    : undefined;
  const shouldShowFeaturedCases = inputValue.trim().length === 0;

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
            initPendingWorkflowSettings={pendingWorkflowSettings}
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
                        onClick={() => navigate("/model-providers/default-services")}
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
                          setShowcaseCase(null);
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
                    onChange={setInputValue}
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
                    onWorkflowSettingsChange={(settings) => {
                      setPendingWorkflowSettings(settings);
                    }}
                    initialWorkflowSettings={pendingWorkflowSettings ?? undefined}
                    runInBackground={runInBackground}
                    showcaseSelection={showcaseSelection}
                  />
                  {shouldShowFeaturedCases ? <FeaturedCases /> : null}
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
