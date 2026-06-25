import { useState, useEffect, useRef } from "react";
import "./index.scss";
import DisclaimerIcon from "../../assets/icons/disclaimer_icon.svg?react";
import WarningIcon from "../../assets/icons/warning.svg?react";
import ChatInput, {
  ChatInputImperativeProps,
} from "@/modules/chat/components/ChatInput";
import ChatLayout from "../chatLayout";
import { ChatConfig } from "@/modules/chat/components/ChatConfigs";
import { Button, Tooltip, message } from "antd";
import {
  CHAT_RESUME_CONVERSATION_KEY,
  CHAT_SELECT_CONVERSATION_EVENT,
} from "@/modules/chat/constants/chat";
import { allowedUploadTypes } from "@/modules/chat/components/ImageUpload";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useChatModelProviderGuard } from "@/modules/chat/hooks/useChatModelProviderGuard";
import { AgentAppsAuth } from "@/components/auth";
import PreferenceConfigNotice from "@/modules/chat/components/PreferenceConfigNotice";

const NewChatPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
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
  const [welcomeKnowledgeRefreshKey, setWelcomeKnowledgeRefreshKey] =
    useState(0);
  const newChatInputRef = useRef<ChatInputImperativeProps>(null);

  const [isDragging, setIsDragging] = useState(false);
  const dragCounterRef = useRef(0);
  const isChatDisabled = !modelProviderGuard.canChat;
  const chatDisabledReason = modelProviderGuard.isChecking
    ? t("chat.modelProviderChecking")
    : modelProviderGuard.status === "error"
      ? t("chat.modelProviderCheckFailed")
      : t("chat.modelProviderRequiredTitle");
  const chatDisabledDescription = modelProviderGuard.isChecking
    ? t("chat.modelProviderCheckingDesc")
    : modelProviderGuard.status === "error"
      ? t("chat.modelProviderCheckFailedDesc")
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

  useEffect(() => {
    if (!isChatContent) {
      newChatInputRef.current?.clearFiles();
      setInputValue("");
    }
  }, [isChatContent]);

  const handleSetIsChatContent = (value: boolean) => {
    if (value && !chatLayoutMounted) {
      setChatLayoutMounted(true);
    }
    if (!value) {
      setWelcomeKnowledgeRefreshKey((key) => key + 1);
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
      const conversationId =
        (event as CustomEvent<{ conversationId?: string }>).detail
          ?.conversationId || "";
      if (!conversationId) {
        setWelcomeKnowledgeRefreshKey((key) => key + 1);
        setIsChatContent(false);
        return;
      }
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
    if (isChatDisabled) {
      return;
    }
    // Ignore internal DOM drag-and-drop (e.g. plugin panel card sorting).
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

    if (isChatDisabled) {
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

  return (
    <div>
      {}
      {chatLayoutMounted && (
        <div style={{ display: isChatContent ? "block" : "none" }}>
          <ChatLayout
            setIsChatContent={handleSetIsChatContent}
            setChatConfigFn={setChatConfig}
            initchatConfig={chatConfig}
            canChat={!isChatDisabled}
            embeddingReady={modelProviderGuard.embeddingReady}
            multimodalEmbeddingReady={modelProviderGuard.multimodalEmbeddingReady}
            rerankReady={modelProviderGuard.rerankReady}
            chatDisabledReason={chatDisabledReason}
            chatDisabledDescription={chatDisabledDescription}
            chatDisabledAction={chatDisabledAction}
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
                    {getGreeting()}{t("chat.greetingSuffix")}
                  </h1>
                </div>

                <div className="input-section">
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
                  <PreferenceConfigNotice hidden={isChatDisabled} />
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
                    setIsChatContent={(value) => {
                      if (value) {
                        setInputValue("");
                      }
                      handleSetIsChatContent(value);
                    }}
                    chatConfig={chatConfig}
                    setChatConfig={setChatConfig}
                    disabled={isChatDisabled}
                    embeddingReady={modelProviderGuard.embeddingReady}
                    multimodalEmbeddingReady={modelProviderGuard.multimodalEmbeddingReady}
                    rerankReady={modelProviderGuard.rerankReady}
                    disabledReason={chatDisabledReason}
                    disabledDescription={chatDisabledDescriptionContent}
                    disabledAction={chatDisabledAction}
                  />
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
