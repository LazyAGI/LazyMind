import {
  useState,
  useRef,
  forwardRef,
  useEffect,
  useCallback,
  useImperativeHandle,
  useId,
  useMemo,
  type ReactNode,
} from "react";
import { RcFile } from "antd/es/upload";
import { Badge, Button, Input, message, Spin, Tooltip } from "antd";
import {
  CloseOutlined,
  CommentOutlined,
  EditOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import { debounce } from "lodash";
import AttachmentIcon from "../../assets/icons/attachment_icon.svg?react";
import SendIcon from "../../assets/icons/send_icon.svg?react";
import AddIcon from "../../assets/icons/add.svg?react";

import ImageUpload, {
  allowedImageTypes,
  allowedFileTypes,
  allowedUploadTypes,
  ImageUploadImperativeProps,
  OnBeforeAddFilesResult,
} from "../ImageUpload";
import { fileToBase64 } from "@/modules/chat/utils/upload";
import { useChatMessageStore } from "@/modules/chat/store/chatMessage";
import { useChatInputStore } from "@/modules/chat/store/chatInput";

import "./index.scss";

import { ChatConfig } from "../ChatConfigs";
import ChatSelector from "../ChatSelector";
import PromptModal, { PromptImperativeProps } from "../PromptModal";
import ChatConfigModal from "./ChatConfigModal";
import type { ConversationPluginSettings } from "../../utils/request";
import { PluginSessionApi } from "../../utils/request";
import { usePluginStore } from "@/modules/chat/store/pluginPanel";
import BatchChatComponent, { BatchChatImperativeProps } from "../BatchChat";

// Stable empty array reference — must NOT be inline `?? []` in a zustand selector
// because a new array on every call triggers useSyncExternalStore to fire React error #185.
const EMPTY_DISMISSED: Array<{ session_id: string; plugin_id: string }> = [];
import ShowChatFileList from "../ShowChatFileList";
import { formatFileSize } from "@/modules/chat/utils";
import { useChatThinkStore } from "@/modules/chat/store/chatThink";
import { useChatNewMessageStore } from "@/modules/chat/store/chatNewMessage";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { PromptServiceApi } from "@/modules/chat/utils/request";
import { Popover, Tag } from "antd";

const { TextArea } = Input;

/**
 * Shows a button in the toolbar when there are dismissed plugin sessions.
 * Clicking it opens a popover listing dismissed sessions with restore buttons.
 * Dismissed sessions are cached in pluginPanel store so the button survives component remounts.
 */
function DismissedPluginRestoreButton({ conversationId }: { conversationId: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [restoring, setRestoring] = useState<string | null>(null);
  const bumpDismissedRefresh = usePluginStore((s) => s.bumpDismissedRefresh);
  const fetchDismissedSessions = usePluginStore((s) => s.fetchDismissedSessions);
  // Read the array reference from store; fall back to undefined and handle below.
  // IMPORTANT: do NOT use `?? []` inline — a fresh array on every selector call
  // causes useSyncExternalStore to detect a state change on every render, leading
  // to an infinite re-render loop (React error #185).
  const dismissedSessionsFromStore = usePluginStore((s) => s.dismissedSessionsByConversation[conversationId]);
  const dismissedSessions = dismissedSessionsFromStore ?? EMPTY_DISMISSED;
  const dismissedRefreshTrigger = usePluginStore((s) => s.dismissedRefreshTrigger[conversationId] ?? 0);

  // Fetch on mount and whenever a dismiss/restore event fires.
  useEffect(() => {
    fetchDismissedSessions(conversationId);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conversationId, dismissedRefreshTrigger]);

  const handleOpenChange = (v: boolean) => {
    setOpen(v);
    if (v) fetchDismissedSessions(conversationId);
  };

  const handleRestore = async (sessionId: string) => {
    setRestoring(sessionId);
    try {
      await PluginSessionApi().restoreSession(sessionId);
      bumpDismissedRefresh(conversationId);
      // Reload active session so PluginPanel re-appears immediately without needing a page refresh.
      usePluginStore.getState().loadActiveSession(conversationId);
      setOpen(false);
    } catch (e: unknown) {
      const err = e as { response?: { data?: { error?: string } } };
      message.error(err?.response?.data?.error ?? t('chat.pluginRestoreFailed'));
    } finally {
      setRestoring(null);
    }
  };

  if (dismissedSessions.length === 0) return null;

  const content = (
    <div style={{ minWidth: 200 }}>
      <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
        {dismissedSessions.map((s) => (
          <li key={s.session_id} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
            <Tag style={{ flex: 1 }}>{s.plugin_id}</Tag>
            <Button
              size='small'
              loading={restoring === s.session_id}
              onClick={() => handleRestore(s.session_id)}
            >
              {t('chat.pluginRestoreBtn')}
            </Button>
          </li>
        ))}
      </ul>
    </div>
  );

  return (
    <Popover
      open={open}
      onOpenChange={handleOpenChange}
      trigger='click'
      content={content}
      title={t('chat.pluginDismissedTitle')}
    >
      <Tooltip title={t('chat.pluginDismissedTitle')}>
        <button
          type='button'
          className='input-bottom-actions-left-item input-bottom-actions-left-item--icon-only'
          aria-label={t('chat.pluginDismissedTitle')}
        >
          {/* Trash / recycle-bin icon */}
          <svg width='14' height='14' viewBox='0 0 14 14' fill='none' xmlns='http://www.w3.org/2000/svg' aria-hidden='true'>
            <path d='M1.75 3.5h10.5M5.25 3.5V2.333A.583.583 0 0 1 5.833 1.75h2.334a.583.583 0 0 1 .583.583V3.5M11.083 3.5l-.583 8.167A.583.583 0 0 1 9.917 12.25H4.083a.583.583 0 0 1-.583-.583L2.917 3.5' stroke='currentColor' strokeWidth='1.2' strokeLinecap='round' strokeLinejoin='round'/>
            <path d='M5.833 6.417v3.5M8.167 6.417v3.5' stroke='currentColor' strokeWidth='1.2' strokeLinecap='round'/>
          </svg>
        </button>
      </Tooltip>
    </Popover>
  );
}

const MAX_UPLOAD_FILES = 3;

const PROMPT_SUGGESTIONS = [
  {
    key: "persuasive",
    labelKey: "chat.promptSuggestionPersuasive",
    descriptionKey: "chat.promptSuggestionPersuasiveDesc",
    templateKey: "chat.promptSuggestionPersuasiveTemplate",
  },
  {
    key: "structure",
    labelKey: "chat.promptSuggestionStructure",
    descriptionKey: "chat.promptSuggestionStructureDesc",
    templateKey: "chat.promptSuggestionStructureTemplate",
  },
  {
    key: "tone",
    labelKey: "chat.promptSuggestionTone",
    descriptionKey: "chat.promptSuggestionToneDesc",
    templateKey: "chat.promptSuggestionToneTemplate",
  },
  {
    key: "polish",
    labelKey: "chat.promptSuggestionPolish",
    descriptionKey: "chat.promptSuggestionPolishDesc",
    templateKey: "chat.promptSuggestionPolishTemplate",
  },
];

function getSuffix(f: { name: string }) {
  return f.name.substring(f.name.lastIndexOf(".")).toLowerCase();
}
function isImage(f: { name: string }) {
  return allowedImageTypes.includes(getSuffix(f));
}
function isDoc(f: { name: string }) {
  return allowedFileTypes.includes(getSuffix(f));
}


function preprocessUpload(
  newFiles: File[],
  currentFiles: { name: string }[],
  hasKB: boolean,
  t: (key: string) => string,
): OnBeforeAddFilesResult {
  const hasImage = currentFiles.some(isImage);
  const hasDoc = currentFiles.some(isDoc);
  const newImages = newFiles.filter((f) => isImage(f));
  const newDocs = newFiles.filter((f) => isDoc(f));
  const newHasBoth = newImages.length > 0 && newDocs.length > 0;

  let filesToAdd: File[];
  let clearFirst: boolean;
  const toasts: string[] = [];

  if (newHasBoth) {
    filesToAdd = newDocs;
    clearFirst = currentFiles.length > 0;
    toasts.push(t("chat.docImageExclusive"));
    if (hasKB) {
      toasts.push(t("chat.priorityFile"));
    }
  } else if (hasDoc && newImages.length > 0) {
    clearFirst = false;
    filesToAdd = [];
    toasts.push(t("chat.docImageExclusive"));
    if (hasKB) {
      toasts.push(t("chat.priorityFile"));
    }
  } else if (hasImage && newDocs.length > 0) {
    clearFirst = false;
    filesToAdd = [];
    toasts.push(t("chat.docImageExclusive"));
    if (hasKB) {
      toasts.push(t("chat.priorityFile"));
    }
  } else {
    clearFirst = false;
    filesToAdd = newFiles;
    if (hasKB && newFiles.length > 0) {
      toasts.push(t("chat.priorityFile"));
    }
  }

  return { filesToAdd, clearFirst, toasts };
}

export interface SendMessageParams {
  text: string;
  citeMessage?: string;
  citeMessages?: string[];
  clearInput?: boolean;
  fileList?: ChatFileList[];
  fileListRef?: React.RefObject<ImageUploadImperativeProps | null>;
  files?: (RcFile & { uri: string })[];
  create_time?: string;
  /** When true, the payload will include run_in_background=true and the task center badge will increment. */
  run_in_background?: boolean;
  /** Structured answers from an AskCard submission; forwarded to the backend as ask_answers_structured. */
  ask_answers_structured?: import('@/modules/chat/components/AskCard').AskAnswersStructured;
}

interface ChatInputProps {
  value: string;
  onChange: (value: string) => void;
  onSend?: (params: SendMessageParams) => void;
  placeholder?: string;
  openHistory?: () => void;
  openNewChat?: () => void;
  isChatContent: boolean;
  showHistoryList?: boolean;
  showHistoryButton?: boolean;
  showPromptSuggestions?: boolean;
  setIsChatContent?: (isChatContent: boolean) => void;
  onHeightChange?: () => void;
  chatConfig?: ChatConfig;
  setChatConfig?: (chatConfig: ChatConfig) => void;
  setChatConfigFn?: (chatConfig: ChatConfig) => void;
  knowledgeRefreshKey?: number | string;
  /** Bump to remount the chat config popover (e.g. when starting a fresh welcome-screen chat). */
  configResetKey?: number | string;
  sessionId?: string;
  isStreaming?: boolean;
  onStopGeneration?: () => void;
  embeddingReady?: boolean | null;
  /** Called when plugin settings change (e.g. from the chat config popover). */
  onPluginSettingsChange?: (settings: ConversationPluginSettings) => void;
  /** Initial plugin settings to pre-populate the config popover. */
  initialPluginSettings?: ConversationPluginSettings;
  /** When true, the allow-plugin toggle in config is locked (plugin session is active). */
  hasPluginSession?: boolean;
  multimodalEmbeddingReady?: boolean | null;
  rerankReady?: boolean | null;
  disabled?: boolean;
  disabledReason?: string;
  disabledDescription?: ReactNode;
  disabledAction?: ReactNode;
  citeMessage?: string;
  citeMessages?: string[];
  onRemoveCiteMessage?: (index: number) => void;
  onClearCiteMessage?: () => void;
}

export interface ChatFileList {
  uid: string;
  name: string;
  base64: string;
  suffix: string;
  size: string;
}

export interface ChatInputImperativeProps {
  clearFiles: () => void;
  element: HTMLDivElement | null;
  focus: () => void;
  uploadFiles: (files: File[]) => void;
}

interface SendButtonProps {
  isStreaming: boolean;
  sendDisabled: boolean;
  disabled: boolean;
  sendLabel: string;
  stopLabel: string;
  onSend: () => void;
  onStop?: () => void;
}
const SendButton: React.FC<SendButtonProps> = ({
  isStreaming,
  sendDisabled,
  disabled,
  sendLabel,
  stopLabel,
  onSend,
  onStop,
}) => {
  const isStopMode = isStreaming && Boolean(onStop);
  const isDisabled = isStopMode ? disabled : sendDisabled || disabled;

  return (
    <button
      type="button"
      className={`send-button${isStopMode ? " stop-mode" : ""}${isDisabled ? " disabled" : ""}`}
      onClick={isDisabled ? undefined : isStopMode ? onStop : onSend}
      disabled={isDisabled}
      aria-label={isStopMode ? stopLabel : sendLabel}
    >
      {isStopMode ? <span className="stop-icon" aria-hidden="true" /> : <SendIcon />}
    </button>
  );
};

SendButton.displayName = "SendButton";

const ChatInput = forwardRef<ChatInputImperativeProps, ChatInputProps>(
  (props, ref) => {
    const {
      value,
      onChange,
      onSend,
      placeholder,
      openHistory,
      openNewChat,
      isChatContent,
      showHistoryList,
      showHistoryButton = true,
      showPromptSuggestions = true,
      onHeightChange,
      setIsChatContent,
      chatConfig,
      setChatConfig,
      setChatConfigFn,
      knowledgeRefreshKey,
      configResetKey,
      sessionId,
      isStreaming = false,
      onStopGeneration,
      embeddingReady,
      multimodalEmbeddingReady,
      rerankReady,
      disabled = false,
      disabledReason,
      disabledDescription,
      disabledAction,
      citeMessage,
      citeMessages,
      onRemoveCiteMessage,
      onClearCiteMessage,
      onPluginSettingsChange,
      initialPluginSettings,
      hasPluginSession,
    } = props;
    const fileListRef = useRef<ImageUploadImperativeProps | null>(null);
    const promptRef = useRef<PromptImperativeProps>(null);
    const batchChatRef = useRef<BatchChatImperativeProps | null>(null);
    const innerRef = useRef<HTMLDivElement>(null);
    const textAreaRef = useRef<any>(null);
    const isComposingRef = useRef(false);
    const [isUploading, setIsUploading] = useState(false);
    const [polishingSuggestionKey, setPolishingSuggestionKey] = useState<string | null>(null);
    const { setThink } = useChatThinkStore();
    const { setNewMessage } = useChatNewMessageStore();
    const { t } = useTranslation();
    const [text, setText] = useState("");
    const disabledNoticeId = useId();
    const previousSessionIdRef = useRef<string | undefined>(undefined);
    const hasSentMessageRef = useRef(false);

    const [fileList, setFileList] = useState<ChatFileList[]>([]);
    const { setPendingMessage, clearPendingMessage } = useChatMessageStore();
    const { saveInputContent, getInputContent, clearInputContent } =
      useChatInputStore();

    const debouncedSaveInput = useMemo(
      () =>
        debounce((conversationId: string, content: string) => {
          if (!content || content.trim() === "") {
            clearInputContent(conversationId);
          } else {
            saveInputContent(conversationId, content);
          }
        }, 500),
      [saveInputContent, clearInputContent],
    );

    const clearMultiData = useCallback(() => {
      setFileList([]);
      fileListRef.current?.clear();
      setTimeout(() => onHeightChange?.(), 0);
    }, [onHeightChange]);

    useImperativeHandle(
      ref,
      () => ({
        clearFiles: () => {
          clearMultiData();
          clearPendingMessage();
        },
        element: innerRef.current,
        focus: () => {
          textAreaRef.current?.focus?.();
        },
        uploadFiles: (files: File[]) => {
          if (disabled) {
            if (disabledReason) {
              message.warning(disabledReason);
            }
            return;
          }
          fileListRef.current?.uploadFiles(files);
        },
      }),
      [clearPendingMessage, clearMultiData, disabled, disabledReason],
    );

    useEffect(() => {
      if (
        sessionId !== undefined &&
        sessionId !== previousSessionIdRef.current
      ) {
        const previousId = previousSessionIdRef.current;

        debouncedSaveInput.cancel();

        if (previousId !== undefined) {
          const previousValue = value || "";
          if (!previousValue || previousValue.trim() === "") {
            clearInputContent(previousId);
          } else {
            saveInputContent(previousId, previousValue);
          }

          if (
            previousId.startsWith("temp_") &&
            !sessionId.startsWith("temp_")
          ) {
            const tempContent = getInputContent(previousId);
            if (tempContent) {
              saveInputContent(sessionId, tempContent);
              clearInputContent(previousId);
            }
          }
        }

        const savedContent = getInputContent(sessionId);
        if (savedContent !== value) {
          onChange(savedContent);
        }

        previousSessionIdRef.current = sessionId;
      }
    }, [
      sessionId,
      saveInputContent,
      getInputContent,
      clearInputContent,
      onChange,
      value,
      debouncedSaveInput,
    ]);

    useEffect(() => {
      return () => {
        debouncedSaveInput.cancel();

        if (hasSentMessageRef.current) {
          hasSentMessageRef.current = false;
          return;
        }

        if (sessionId !== undefined) {
          const currentValue = value || "";
          if (!currentValue || currentValue.trim() === "") {
            clearInputContent(sessionId);
          } else {
            saveInputContent(sessionId, currentValue);
          }
        }
      };
    }, [
      sessionId,
      value,
      saveInputContent,
      clearInputContent,
      debouncedSaveInput,
    ]);

    useEffect(() => {
      const checkUploadStatus = () => {
        const uploadingCount = fileListRef.current?.getUploadingCount() || 0;
        setIsUploading(uploadingCount > 0);
      };

      const interval = setInterval(checkUploadStatus, 500);

      return () => clearInterval(interval);
    }, []);
    const updateImageList = async (list: RcFile[]) => {
      const data: ChatFileList[] = [];
      for (let i = 0; i < list.length; i++) {
        const suffix = list[i].name
          .substring(list[i].name.lastIndexOf("."))
          .toLowerCase();

        const tempImgData = allowedImageTypes.includes(suffix);
        const obj = {
          name: list[i].name,
          uid: list[i].uid,
          suffix,
          size: formatFileSize(list[i].size),
          base64: "",
        };
        if (tempImgData) {
          const res = await fileToBase64(list[i]);
          obj["base64"] = res as string;
        } else {
          obj["base64"] = "";
        }
        data.push(obj);
      }
      setFileList(data);
      setTimeout(() => onHeightChange?.(), 0);
    };

    const removeImage = (uid: string) => {
      fileListRef.current?.removeFile(uid);
      const list = [...fileList].filter((item) => item.uid !== uid);
      setFileList(list);
      setTimeout(() => onHeightChange?.(), 0);
    };

    const onKnowledgeBaseChange = (
      knowledgeBaseId: string[],
      creators: string[],
      tags: string[],
    ) => {
      const tempData = { ...chatConfig, knowledgeBaseId, creators, tags };
      setChatConfig?.(tempData);
      setChatConfigFn?.(tempData);

      const hadNoKB = (chatConfig?.knowledgeBaseId?.length ?? 0) === 0;
      const nowHasKB = knowledgeBaseId.length > 0;
      const hasFiles = fileList.length > 0;
      if (hadNoKB && nowHasKB && hasFiles) {
        message.info(t("chat.priorityFile"));
      }
    };

    const hasKB = (chatConfig?.knowledgeBaseId?.length ?? 0) > 0;
    const onBeforeAddFiles = useCallback(
      (newFiles: File[], currentFiles: { name: string }[]) =>
        preprocessUpload(newFiles, currentFiles, hasKB, t),
      [hasKB, t],
    );
    const normalizedCiteMessages = useMemo(() => {
      if (citeMessages) {
        return citeMessages.map((item) => item.trim()).filter(Boolean);
      }

      const normalizedCiteMessage = citeMessage?.trim();
      return normalizedCiteMessage ? [normalizedCiteMessage] : [];
    }, [citeMessage, citeMessages]);
    const isPromptPolishing = Boolean(polishingSuggestionKey);
    const documentFileCount = fileList.filter(
      (item) => !allowedImageTypes.includes(item.suffix),
    ).length;
    const uploadBadgeCount = isUploading
      ? Math.max(documentFileCount, 1)
      : documentFileCount;
    const isSendDisabled =
      disabled || isPromptPolishing || !value?.trim() || isUploading;
    const shouldShowPromptSuggestions =
      showPromptSuggestions && !disabled && !isStreaming && value.trim().length > 0;

    useEffect(() => {
      setTimeout(() => onHeightChange?.(), 0);
    }, [onHeightChange, shouldShowPromptSuggestions]);

    const handleSend = () => {
      if (disabled) {
        if (disabledReason) {
          message.warning(disabledReason);
        }
        return;
      }
      if (isStreaming || isSendDisabled) {
        return;
      }
      const normalizedText = value.trim();
      setNewMessage(false);
      const sendParams = {
        text: normalizedText,
        citeMessage: normalizedCiteMessages.join("\n\n"),
        citeMessages: normalizedCiteMessages,
        fileList,
        fileListRef,
        files: fileListRef.current?.getFiles(),
        create_time: new Date().toISOString(),
      };

      if (!isChatContent) {
        setPendingMessage(sendParams);
        setIsChatContent?.(true);
      } else {
        onSend?.(sendParams);
        clearMultiData();
      }

      hasSentMessageRef.current = true;

      if (sessionId !== undefined) {
        debouncedSaveInput.cancel();
        clearInputContent(sessionId);
      }
      onChange("");
      setText("");
      onClearCiteMessage?.();
    };

    const handleInputChange = (text: string) => {
      onChange(text);
      setText(text);
      if (sessionId !== undefined) {
        debouncedSaveInput(sessionId, text);
      }
    };

    const handleApplyPromptSuggestion = async (
      suggestion: (typeof PROMPT_SUGGESTIONS)[number],
    ) => {
      const normalizedPrompt = value.trim();
      if (!normalizedPrompt || polishingSuggestionKey) {
        return;
      }

      setPolishingSuggestionKey(suggestion.key);
      try {
        const response = await PromptServiceApi().promptServicePolishPrompt({
          promptPolishRequest: {
            content: normalizedPrompt,
            user_instruct: t(suggestion.templateKey, { prompt: "" }).trim(),
          },
        });
        const nextPrompt = response.data.content?.trim();
        if (!nextPrompt) {
          return;
        }
        onChange(nextPrompt);
        setText(nextPrompt);
        if (sessionId !== undefined) {
          debouncedSaveInput(sessionId, nextPrompt);
        }
        setTimeout(() => onHeightChange?.(), 0);
      } catch (error) {
        message.error(
          getLocalizedErrorMessage(error, t("common.requestFailed")) ||
            t("common.requestFailed"),
        );
      } finally {
        setPolishingSuggestionKey(null);
      }
    };

    const handlePaste = useCallback(
      (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
        const clipboardData = e.clipboardData;
        if (disabled) {
          e.preventDefault();
          if (disabledReason) {
            message.warning(disabledReason);
          }
          return;
        }
        if (!clipboardData) {
          return;
        }

        const items = clipboardData.items;
        const files: File[] = [];
        const invalidFiles: File[] = [];
        let hasAnyFile = false;

        for (let i = 0; i < items.length; i++) {
          const item = items[i];

          if (item.kind === "file") {
            hasAnyFile = true;
            const file = item.getAsFile();
            if (file) {
              const fileName = file.name || `pasted-file-${Date.now()}`;
              const suffix = fileName.includes(".")
                ? fileName.substring(fileName.lastIndexOf(".")).toLowerCase()
                : "";

              let finalFile = file;
              if (!suffix && file.type.startsWith("image/")) {
                const ext = file.type.split("/")[1] || "png";
                const newFileName = `pasted-image-${Date.now()}.${ext}`;
                finalFile = new File([file], newFileName, { type: file.type });
              }

              const finalSuffix = finalFile.name
                .substring(finalFile.name.lastIndexOf("."))
                .toLowerCase();
              if (allowedUploadTypes.includes(finalSuffix)) {
                if (fileList.length + files.length < MAX_UPLOAD_FILES) {
                  files.push(finalFile);
                } else {
                  message.warning(t("chat.maxFilesWarning"));
                }
              } else {
                invalidFiles.push(finalFile);
              }
            }
          }
        }

        if (hasAnyFile) {
          e.preventDefault();
          e.stopPropagation();

          if (invalidFiles.length > 0) {
            message.warning(
              t("chat.unsupportedFileType", { types: allowedUploadTypes.join(",") }),
            );
          }

          if (files.length > 0) {
            fileListRef.current?.uploadFiles(files);
          }
        }
      },
      [disabled, disabledReason, fileList.length, t],
    );

    const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (
        e.key !== "Enter" ||
        e.shiftKey ||
        isUploading ||
        disabled ||
        isPromptPolishing ||
        isStreaming
      ) {
        return;
      }

      // IME candidate confirmation also uses Enter, and some browsers only
      // expose the composition state through the native event / keyCode 229.
      if (
        isComposingRef.current ||
        e.nativeEvent.isComposing ||
        e.nativeEvent.keyCode === 229
      ) {
        return;
      }

      e.preventDefault();
      handleSend();
      setNewMessage(false);
    };

    return (
      <div
        className={`input-wrapper${disabled ? " is-disabled" : ""}`}
        ref={innerRef}
      >
        {disabled && (disabledReason || disabledDescription) ? (
          <div
            className="chat-input-disabled-notice"
            id={disabledNoticeId}
            role="status"
            aria-live="polite"
          >
            <span className="chat-input-disabled-icon" aria-hidden="true">
              <SettingOutlined />
            </span>
            <div className="chat-input-disabled-copy">
              {disabledReason ? (
                <span className="chat-input-disabled-title">
                  {disabledReason}
                </span>
              ) : null}
              {disabledDescription ? (
                <span className="chat-input-disabled-description">
                  {disabledDescription}
                </span>
              ) : null}
            </div>
            {disabledAction ? (
              <div className="chat-input-disabled-action">{disabledAction}</div>
            ) : null}
          </div>
        ) : null}
        <div className="input-container">
          <div className="input-top">
            <div className="input-field">
              <ShowChatFileList fileList={fileList} onRemove={removeImage} />
              {normalizedCiteMessages.length > 0 && (
                <div className="cite-message-preview-list">
                  {normalizedCiteMessages.map((messageText, index) => (
                    <div
                      className="cite-message-preview"
                      key={`${index}-${messageText}`}
                    >
                      <CommentOutlined className="cite-message-preview-icon" />
                      <Tooltip
                        title={messageText}
                        placement="topLeft"
                        overlayClassName="cite-message-preview-tooltip"
                      >
                        <span
                          className="cite-message-preview-text"
                          tabIndex={0}
                          aria-label={messageText}
                        >
                          {messageText}
                        </span>
                      </Tooltip>
                      <Button
                        type="text"
                        size="small"
                        className="cite-message-preview-close"
                        icon={<CloseOutlined />}
                        onClick={() =>
                          onRemoveCiteMessage
                            ? onRemoveCiteMessage(index)
                            : onClearCiteMessage?.()
                        }
                        aria-label={t("chat.clearCitation")}
                      />
                    </div>
                  ))}
                  {normalizedCiteMessages.length > 1 && (
                    <Button
                      type="text"
                      size="small"
                      className="cite-message-preview-clear-all"
                      onClick={onClearCiteMessage}
                    >
                      {t("chat.clearCitation")}
                    </Button>
                  )}
                </div>
              )}
              <TextArea
                ref={textAreaRef}
                autoSize={{ minRows: 2, maxRows: 5 }}
                className="message-input"
                placeholder={
                  placeholder || t("chat.inputPlaceholder")
                }
                value={value}
                onChange={(e) => handleInputChange(e.target.value)}
                onPaste={handlePaste}
                onCompositionStart={() => {
                  isComposingRef.current = true;
                }}
                onCompositionEnd={() => {
                  isComposingRef.current = false;
                }}
                onKeyDown={handleKeyDown}
                disabled={disabled || isPromptPolishing}
                aria-describedby={
                  disabled && (disabledReason || disabledDescription)
                    ? disabledNoticeId
                    : undefined
                }
              />

              <div className="input-bottom-actions">
                <div className="input-bottom-actions-left">
                  {isChatContent && (
                    <div
                      className={`input-bottom-actions-left-item${isPromptPolishing ? " is-disabled" : ""}`}
                      aria-disabled={isPromptPolishing}
                      onClick={() => {
                        if (isPromptPolishing) {
                          return;
                        }
                        setThink(false);
                        clearMultiData();
                        clearPendingMessage();
                        openNewChat?.();
                        setNewMessage(true);
                      }}
                    >
                      <AddIcon />
                      {t("chat.newChat")}
                    </div>
                  )}
                  <ChatSelector
                    chatConfig={chatConfig ?? {}}
                    refreshKey={knowledgeRefreshKey}
                    embeddingReady={embeddingReady}
                    multimodalEmbeddingReady={multimodalEmbeddingReady}
                    rerankReady={rerankReady}
                    onChange={onKnowledgeBaseChange}
                  />
                  {/* <ModelSelector sessionId={sessionId} disabled={isStreaming} /> */}
                  {showHistoryButton && openHistory && (
                    <div
                      className={`input-bottom-actions-left-item ${showHistoryList ? "selected" : ""}`}
                      onClick={openHistory}
                    >
                      {t("chat.chatHistory")}
                    </div>
                  )}
                  <div
                    className={`input-bottom-actions-left-item${disabled || isPromptPolishing ? " is-disabled" : ""}`}
                    aria-disabled={disabled || isPromptPolishing}
                    onClick={() => {
                      if (isPromptPolishing) {
                        return;
                      }
                      if (disabled) {
                        if (disabledReason) {
                          message.warning(disabledReason);
                        }
                        return;
                      }
                      promptRef.current?.onOpen();
                    }}
                  >
                    {t("chat.promptTemplate")}
                  </div>
                  <ChatConfigModal
                    key={
                      configResetKey != null
                        ? `config-reset-${configResetKey}`
                        : undefined
                    }
                    conversationId={sessionId && !sessionId.startsWith("temp_") ? sessionId : undefined}
                    initialSettings={initialPluginSettings}
                    hasPluginSession={hasPluginSession}
                    onSave={onPluginSettingsChange}
                  />
                  {sessionId && !sessionId.startsWith("temp_") && (
                    <DismissedPluginRestoreButton conversationId={sessionId} />
                  )}
                </div>

                <div className="input-bottom-actions-right">
                  {}
                  <div className="input-bottom-actions-right-item">
                    <ImageUpload
                      updateFiles={updateImageList}
                      listNum={fileList.length}
                      ref={fileListRef}
                      types={allowedUploadTypes}
                      max={MAX_UPLOAD_FILES}
                      onBeforeAddFiles={onBeforeAddFiles}
                      disabled={disabled || isPromptPolishing}
                      disabledReason={
                        isPromptPolishing ? t("chat.promptPolishing") : disabledReason
                      }
                      icon={
                        <Badge
                          count={uploadBadgeCount}
                          dot={isUploading && !documentFileCount}
                          size="small"
                          className="chat-upload-document-badge"
                        >
                          <Button
                            aria-label={t("chat.upload")}
                            icon={<AttachmentIcon />}
                            type="text"
                            disabled={disabled || isPromptPolishing}
                          />
                        </Badge>
                      }
                    />
                  </div>
                  <div className="input-bottom-actions-right-item">
                    <Tooltip title="以异步任务方式执行，可在任务中心查看进度和结果">
                      <Button
                        size="small"
                        type="text"
                        style={{ fontSize: 12, color: '#888', padding: '0 4px' }}
                        disabled={isSendDisabled || isStreaming}
                        onClick={() => {
                          if (isSendDisabled || isStreaming) return;
                          const normalizedText = value.trim();
                          setNewMessage(false);
                          const sendParams: SendMessageParams = {
                            text: normalizedText,
                            citeMessage: normalizedCiteMessages.join("\n\n"),
                            citeMessages: normalizedCiteMessages,
                            fileList,
                            fileListRef,
                            files: fileListRef.current?.getFiles(),
                            create_time: new Date().toISOString(),
                            run_in_background: true,
                          };
                          if (!isChatContent) {
                            setPendingMessage(sendParams);
                            setIsChatContent?.(true);
                          } else {
                            onSend?.(sendParams);
                            clearMultiData();
                          }
                          onChange("");
                          setText("");
                          onClearCiteMessage?.();
                        }}
                        aria-label="后台运行"
                      >
                        后台运行
                      </Button>
                    </Tooltip>
                  </div>
                  <div className="input-bottom-actions-right-item">
                    <SendButton
                      isStreaming={isStreaming}
                      sendDisabled={isSendDisabled}
                      disabled={disabled}
                      sendLabel={t("chat.send")}
                      stopLabel={t("chat.stopGenerate")}
                      onSend={handleSend}
                      onStop={onStopGeneration}
                    />
                  </div>
                </div>
              </div>
            </div>
          </div>
          {shouldShowPromptSuggestions ? (
            <div
              className="prompt-suggestion-panel"
              aria-label={t("chat.promptSuggestionsAria")}
            >
              {PROMPT_SUGGESTIONS.map((suggestion) => (
                <button
                  type="button"
                  className={`prompt-suggestion-item${
                    polishingSuggestionKey === suggestion.key ? " is-loading" : ""
                  }`}
                  key={suggestion.key}
                  disabled={isPromptPolishing}
                  onClick={() => handleApplyPromptSuggestion(suggestion)}
                  aria-busy={polishingSuggestionKey === suggestion.key}
                >
                  <span className="prompt-suggestion-icon" aria-hidden="true">
                    {polishingSuggestionKey === suggestion.key ? (
                      <Spin size="small" />
                    ) : (
                      <EditOutlined />
                    )}
                  </span>
                  <span className="prompt-suggestion-copy">
                    <span className="prompt-suggestion-title">
                      {polishingSuggestionKey === suggestion.key
                        ? t("chat.promptPolishing")
                        : t(suggestion.labelKey)}
                    </span>
                    <span className="prompt-suggestion-description">
                      {t(suggestion.descriptionKey)}
                    </span>
                  </span>
                </button>
              ))}
            </div>
          ) : null}
        </div>
        <PromptModal
          ref={promptRef}
          onSelectPrompt={(prompt) => onChange(text + " " + prompt)}
        />
        <BatchChatComponent
          ref={batchChatRef}
          cancelFn={() => {

          }}
        />
      </div>
    );
  },
);

ChatInput.displayName = "ChatInput";

export default ChatInput;
