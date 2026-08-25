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
import { Button, message, Select, Spin, Tooltip } from "antd";
import {
  AppstoreOutlined,
  BookOutlined,
  BulbOutlined,
  CloseOutlined,
  CommentOutlined,
  EditOutlined,
  PaperClipOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import { debounce } from "lodash";
import SendIcon from "../../assets/icons/send_icon.svg?react";
import AddIcon from "../../assets/icons/add.svg?react";

import ImageUpload, {
  allowedImageTypes,
  allowedFileTypes,
  allowedTextTypes,
  allowedUploadTypes,
  ImageUploadImperativeProps,
  OnBeforeAddFilesResult,
} from "../ImageUpload";
import { fileToBase64 } from "@/modules/chat/utils/upload";
import { useChatMessageStore } from "@/modules/chat/store/chatMessage";
import { useChatInputStore } from "@/modules/chat/store/chatInput";
import { resolveMarkdownImageUrlAsync } from "@/modules/knowledge/utils/imageUrl";

import "./index.scss";

import { ChatConfig } from "../ChatConfigs";
import ChatSelector, { type ChatSelectorImperativeProps } from "../ChatSelector";
import PromptModal, { PromptImperativeProps } from "../PromptModal";
import { appendPromptToDraft } from "../PromptModal/promptLibrary";
import ChatConfigModal from "./ChatConfigModal";
import type { ConversationRuntimeSettings } from "../../utils/request";
import { WorkflowSessionApi } from "../../utils/request";
import { useWorkflowStore } from "@/modules/chat/store/workflowPanel";
import BatchChatComponent, { BatchChatImperativeProps } from "../BatchChat";
import MentionEditor, {
  type ChatMention,
  type MentionEditorRef,
} from "./MentionEditor";
import ContextUsageButton from "./ContextUsageButton";
import { buildCitedMessageText } from "../newChatContainer/utils/citeMessage";

// Stable empty array reference — must NOT be inline `?? []` in a zustand selector
// because a new array on every call triggers useSyncExternalStore to fire React error #185.
const EMPTY_DISMISSED: Array<{ session_id: string; workflow_id: string }> = [];
const THINKING_DEPTH_LABEL_KEYS: Record<ThinkingDepth, string> = {
  low: "chat.thinkingDepthLow",
  medium: "chat.thinkingDepthMedium",
  high: "chat.thinkingDepthHigh",
  max: "chat.thinkingDepthMax",
};
import ShowChatFileList from "../ShowChatFileList";
import { formatFileSize } from "@/modules/chat/utils";
import {
  THINKING_DEPTH_VALUES,
  useChatThinkStore,
  type ThinkingDepth,
} from "@/modules/chat/store/chatThink";
import { useChatNewMessageStore } from "@/modules/chat/store/chatNewMessage";
import { useTranslation } from "react-i18next";
import { PromptServiceApi } from "@/modules/chat/utils/request";
import {
  listToolAssetsPage,
  TOOL_AVAILABILITY_CHANGED_EVENT,
  type ToolAvailabilityChange,
} from "@/modules/memory/toolApi";
import { Popover, Tag } from "antd";
import type {
  ChatFileList,
  ChatInputImperativeProps,
  SendMessageParams,
} from "./types";

export type { ChatFileList, ChatInputImperativeProps, SendMessageParams } from "./types";

/**
 * Shows a button in the toolbar when there are dismissed workflow sessions.
 * Clicking it opens a popover listing dismissed sessions with restore buttons.
 * Dismissed sessions are cached in workflowPanel store so the button survives component remounts.
 */
function DismissedWorkflowRestoreButton({
  conversationId,
}: {
  conversationId: string;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [restoring, setRestoring] = useState<string | null>(null);
  const bumpDismissedRefresh = useWorkflowStore((s) => s.bumpDismissedRefresh);
  const fetchDismissedSessions = useWorkflowStore(
    (s) => s.fetchDismissedSessions,
  );
  // Read the array reference from store; fall back to undefined and handle below.
  // IMPORTANT: do NOT use `?? []` inline — a fresh array on every selector call
  // causes useSyncExternalStore to detect a state change on every render, leading
  // to an infinite re-render loop (React error #185).
  const dismissedSessionsFromStore = useWorkflowStore(
    (s) => s.dismissedSessionsByConversation[conversationId],
  );
  const dismissedSessions = dismissedSessionsFromStore ?? EMPTY_DISMISSED;
  const dismissedRefreshTrigger = useWorkflowStore(
    (s) => s.dismissedRefreshTrigger[conversationId] ?? 0,
  );

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
      await WorkflowSessionApi().restoreSession(sessionId);
      bumpDismissedRefresh(conversationId);
      // Reload active session so WorkflowPanel re-appears immediately without needing a page refresh.
      useWorkflowStore.getState().loadActiveSession(conversationId);
      setOpen(false);
    } catch {
      // API errors are reported by the shared request interceptor.
    } finally {
      setRestoring(null);
    }
  };

  if (dismissedSessions.length === 0) return null;

  const content = (
    <div style={{ minWidth: 200 }}>
      <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
        {dismissedSessions.map((s) => (
          <li
            key={s.session_id}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              marginBottom: 6,
            }}
          >
            <Tag style={{ flex: 1 }}>{s.workflow_id}</Tag>
            <Button
              size="small"
              loading={restoring === s.session_id}
              onClick={() => handleRestore(s.session_id)}
            >
              {t("chat.workflowRestoreBtn")}
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
      trigger="click"
      content={content}
      title={t("chat.workflowDismissedTitle")}
    >
      <Tooltip title={t("chat.workflowDismissedTitle")}>
        <button
          type="button"
          className="input-bottom-actions-left-item input-bottom-actions-left-item--icon-only"
          aria-label={t("chat.workflowDismissedTitle")}
        >
          {/* Trash / recycle-bin icon */}
          <svg
            width="14"
            height="14"
            viewBox="0 0 14 14"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
            aria-hidden="true"
          >
            <path
              d="M1.75 3.5h10.5M5.25 3.5V2.333A.583.583 0 0 1 5.833 1.75h2.334a.583.583 0 0 1 .583.583V3.5M11.083 3.5l-.583 8.167A.583.583 0 0 1 9.917 12.25H4.083a.583.583 0 0 1-.583-.583L2.917 3.5"
              stroke="currentColor"
              strokeWidth="1.2"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
            <path
              d="M5.833 6.417v3.5M8.167 6.417v3.5"
              stroke="currentColor"
              strokeWidth="1.2"
              strokeLinecap="round"
            />
          </svg>
        </button>
      </Tooltip>
    </Popover>
  );
}

const MAX_UPLOAD_FILES = 3;
export const SKILL_DEPOSIT_MIN_USER_TURNS = 3;
export const SKILL_DEPOSIT_MIN_TOOL_CALL_TURNS = 8;

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

function getSuffix(f: { name?: string }) {
  const name = f.name ?? "";
  if (!name.includes(".")) {
    return "";
  }
  return name.substring(name.lastIndexOf(".")).toLowerCase();
}
function isImage(f: { name?: string }) {
  const suffix = getSuffix(f);
  return suffix !== "" && allowedImageTypes.includes(suffix);
}
function isDoc(f: { name?: string }) {
  const suffix = getSuffix(f);
  return suffix !== "" && (
    allowedFileTypes.includes(suffix) || allowedTextTypes.includes(suffix)
  );
}

const MARKDOWN_IMAGE_PATTERN =
  /!\[[^\]]*\]\(([^\s)]+)(?:\s+["'][^"']*["'])?\)/g;

function extractMarkdownImageSources(text: string): string[] {
  return Array.from(
    text.matchAll(MARKDOWN_IMAGE_PATTERN),
    (match) => match[1] ?? "",
  ).filter(Boolean);
}

function removeMarkdownImages(text: string): string {
  return text.replace(MARKDOWN_IMAGE_PATTERN, "").trim();
}

async function markdownImageToFile(source: string): Promise<File> {
  const resolvedSource = await resolveMarkdownImageUrlAsync(source);
  const url = new URL(resolvedSource, window.location.origin);
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("Unsupported image URL protocol");
  }

  const response = await fetch(url, { credentials: "same-origin" });
  if (!response.ok) {
    throw new Error(`Failed to fetch pasted image: ${response.status}`);
  }

  const blob = await response.blob();
  if (!blob.type.startsWith("image/")) {
    throw new Error("Pasted URL did not return an image");
  }
  const rawName = decodeURIComponent(url.pathname.split("/").pop() || "");
  const suffix = getSuffix({ name: rawName });
  const mimeExtension = blob.type.split("/")[1]?.split("+")[0] || "png";
  const fileName = allowedImageTypes.includes(suffix)
    ? rawName
    : `pasted-image-${Date.now()}.${mimeExtension}`;

  return new File([blob], fileName, { type: blob.type || "image/png" });
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
  /** Called when workflow settings change (e.g. from the chat config popover). */
  onConversationSettingsChange?: (settings: ConversationRuntimeSettings) => void;
  /** Initial workflow settings to pre-populate the config popover. */
  initialConversationSettings?: ConversationRuntimeSettings;
  /** When true, the allow-workflow toggle in config is locked (workflow session is active). */
  hasWorkflowSession?: boolean;
  /** Optional case-driven category selectors shown in the welcome composer. */
  showcaseSelection?: ShowcaseSelection;
  /** Resources bound by a curated experience and included in every send. */
  boundMentions?: ChatMention[];
  multimodalEmbeddingReady?: boolean | null;
  rerankReady?: boolean | null;
  disabled?: boolean;
  disabledReason?: string;
  disabledDescription?: ReactNode;
  disabledAction?: ReactNode;
  citeMessage?: string;
  citeMessages?: string[];
  citeHistoryIds?: (string | undefined)[];
  onRemoveCiteMessage?: (index: number) => void;
  onClearCiteMessage?: () => void;
  skillDepositStats?: SkillDepositStats;
  skillDepositDisabledReason?: string;
  onSkillDeposit?: () => void;
  /** Send the next message as a background task. Used by the new-task entry point. */
  runInBackground?: boolean;
  showThinkingDepth?: boolean;
  showSkillDeposit?: boolean;
  showConversationConfig?: boolean;
  fixedThinkingDepth?: ThinkingDepth;
}

export interface ShowcaseSelection {
  value: string;
  label: string;
  ariaLabel: string;
}

export interface SkillDepositStats {
  userTurns: number;
  toolCallTurns: number;
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
      {isStopMode ? (
        <span className="stop-icon" aria-hidden="true" />
      ) : (
        <SendIcon />
      )}
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
      citeHistoryIds,
      onRemoveCiteMessage,
      onClearCiteMessage,
      skillDepositStats,
      skillDepositDisabledReason,
      onSkillDeposit,
      onConversationSettingsChange,
      initialConversationSettings,
      hasWorkflowSession,
      showcaseSelection,
      boundMentions = [],
      runInBackground = false,
      showThinkingDepth = true,
      showSkillDeposit = true,
      showConversationConfig = true,
      fixedThinkingDepth,
    } = props;
    const fileListRef = useRef<ImageUploadImperativeProps | null>(null);
    const knowledgeSelectorRef = useRef<ChatSelectorImperativeProps | null>(null);
    const promptRef = useRef<PromptImperativeProps>(null);
    const batchChatRef = useRef<BatchChatImperativeProps | null>(null);
    const innerRef = useRef<HTMLDivElement>(null);
    const textAreaRef = useRef<MentionEditorRef>(null);
    const isComposingRef = useRef(false);
    const [isUploading, setIsUploading] = useState(false);
    const [polishingSuggestionKey, setPolishingSuggestionKey] = useState<
      string | null
    >(null);
    const { thinkingDepth, setThinkingDepth } = useChatThinkStore();
    const effectiveThinkingDepth = fixedThinkingDepth ?? thinkingDepth;
    const { setNewMessage } = useChatNewMessageStore();
    const { t } = useTranslation();
    const [text, setText] = useState("");
    const [mentions, setMentions] = useState<ChatMention[]>([]);
    const effectiveMentions = useMemo(() => {
      const merged = new Map<string, ChatMention>();
      for (const mention of [...boundMentions, ...mentions]) {
        merged.set(`${mention.type}:${mention.resource_id}`, mention);
      }
      return [...merged.values()];
    }, [boundMentions, mentions]);
    const [contextRuntimeSettings, setContextRuntimeSettings] = useState(initialConversationSettings);
    const [contextUsageReset, setContextUsageReset] = useState(0);
    const [addMenuOpen, setAddMenuOpen] = useState(false);
    const [knowledgeToolsEnabled, setKnowledgeToolsEnabled] = useState<{
      kb: boolean | null;
    }>({ kb: null });
    const disabledNoticeId = useId();
    const previousSessionIdRef = useRef<string | undefined>(undefined);
    const hasSentMessageRef = useRef(false);

    useEffect(() => {
      setContextRuntimeSettings(initialConversationSettings);
    }, [initialConversationSettings]);

    const [fileList, setFileList] = useState<ChatFileList[]>([]);
    const { setPendingMessage, clearPendingMessage } = useChatMessageStore();
    const { saveInputContent, getInputContent, clearInputContent } =
      useChatInputStore();

    const refreshKnowledgeToolAvailability = useCallback(async () => {
      try {
        const response = await listToolAssetsPage({ silentError: true });
        const toolsByID = new Map(
          response.records.map((tool) => [tool.id, tool.isEnabled]),
        );
        setKnowledgeToolsEnabled({
          kb: toolsByID.get("kb") ?? null,
        });
      } catch {
        // Keep entries usable until the authoritative state can be read.
      }
    }, []);

    const handleToolAvailabilityChanged = useCallback((event: Event) => {
      const change = (event as CustomEvent<ToolAvailabilityChange>).detail;
      if (change?.id === "kb") {
        setKnowledgeToolsEnabled((current) => ({
          ...current,
          [change.id]: change.enabled,
        }));
      }
      void refreshKnowledgeToolAvailability();
    }, [refreshKnowledgeToolAvailability]);

    useEffect(() => {
      void refreshKnowledgeToolAvailability();
      window.addEventListener(
        TOOL_AVAILABILITY_CHANGED_EVENT,
        handleToolAvailabilityChanged,
      );
      return () => window.removeEventListener(
        TOOL_AVAILABILITY_CHANGED_EVENT,
        handleToolAvailabilityChanged,
      );
    }, [handleToolAvailabilityChanged, refreshKnowledgeToolAvailability]);

    const knowledgeBaseEnabled = knowledgeToolsEnabled.kb !== false;
    const knowledgeBaseDisabledReason = "知识库检索已在设置中停用";
    const uploadTypes = allowedUploadTypes;

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
      setFileList((prev) => {
        prev.forEach((item) => {
          if (item.previewUrl?.startsWith("blob:")) {
            URL.revokeObjectURL(item.previewUrl);
          }
        });
        return [];
      });
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
          if (files.length > 0) {
            fileListRef.current?.uploadFiles(files);
          }
        },
      }),
      [
        clearPendingMessage,
        clearMultiData,
        disabled,
        disabledReason,
      ],
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
        const obj: ChatFileList = {
          name: list[i].name,
          uid: list[i].uid,
          suffix,
          size: formatFileSize(list[i].size),
          base64: "",
          previewUrl: "",
        };
        if (tempImgData) {
          const res = await fileToBase64(list[i]);
          obj.base64 = res as string;
          obj.previewUrl = obj.base64;
        } else {
          obj.base64 = "";
          // Object URL lets users open/preview non-image attachments on click.
          obj.previewUrl = URL.createObjectURL(list[i]);
        }
        data.push(obj);
      }
      setFileList((prev) => {
        prev.forEach((item) => {
          if (
            item.previewUrl &&
            item.previewUrl.startsWith("blob:") &&
            !data.some((next) => next.previewUrl === item.previewUrl)
          ) {
            URL.revokeObjectURL(item.previewUrl);
          }
        });
        return data;
      });
      setTimeout(() => onHeightChange?.(), 0);
    };

    const removeImage = (uid: string) => {
      fileListRef.current?.removeFile(uid);
      setFileList((prev) => {
        const target = prev.find((item) => item.uid === uid);
        if (target?.previewUrl?.startsWith("blob:")) {
          URL.revokeObjectURL(target.previewUrl);
        }
        return prev.filter((item) => item.uid !== uid);
      });
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
    const isSendDisabled =
      disabled || isPromptPolishing || !value?.trim() || isUploading;
    const shouldShowPromptSuggestions =
      showPromptSuggestions &&
      !disabled &&
      !isStreaming &&
      value.trim().length > 0;
    const skillDepositUserTurns = skillDepositStats?.userTurns ?? 0;
    const skillDepositToolCallTurns = skillDepositStats?.toolCallTurns ?? 0;
    const missingSkillDepositUserTurns = Math.max(
      0,
      SKILL_DEPOSIT_MIN_USER_TURNS - skillDepositUserTurns,
    );
    const missingSkillDepositToolTurns = Math.max(
      0,
      SKILL_DEPOSIT_MIN_TOOL_CALL_TURNS - skillDepositToolCallTurns,
    );
    const isSkillDepositBlocked = Boolean(skillDepositDisabledReason);
    const isSkillDepositReady =
      missingSkillDepositUserTurns === 0 &&
      missingSkillDepositToolTurns === 0 &&
      !isSkillDepositBlocked;
    const isSkillDepositDisabled =
      !isSkillDepositReady ||
      disabled ||
      isPromptPolishing ||
      isStreaming ||
      !onSkillDeposit;
    const skillDepositTooltip = useMemo(() => {
      if (skillDepositDisabledReason) {
        return skillDepositDisabledReason;
      }
      if (isSkillDepositReady) {
        return t("chat.skillDepositReadyTooltip");
      }
      const missingParts: string[] = [];
      if (missingSkillDepositUserTurns > 0) {
        missingParts.push(
          t("chat.skillDepositMissingUserTurns", {
            count: missingSkillDepositUserTurns,
          }),
        );
      }
      if (missingSkillDepositToolTurns > 0) {
        missingParts.push(
          t("chat.skillDepositMissingToolTurns", {
            count: missingSkillDepositToolTurns,
          }),
        );
      }
      return t("chat.skillDepositDisabledTooltip", {
        missing: missingParts.join(t("chat.skillDepositMissingSeparator")),
      });
    }, [
      isSkillDepositReady,
      missingSkillDepositToolTurns,
      missingSkillDepositUserTurns,
      t,
    ]);

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
      const sendParams: SendMessageParams = {
        text: normalizedText,
        chatConfigSnapshot: {
          knowledgeBaseId: [...(chatConfig?.knowledgeBaseId ?? [])],
          creators: [...(chatConfig?.creators ?? [])],
          tags: [...(chatConfig?.tags ?? [])],
          databaseBaseId: chatConfig?.databaseBaseId,
        },
        thinking_depth: effectiveThinkingDepth,
        mentions: effectiveMentions,
        citeMessage: normalizedCiteMessages.join("\n\n"),
        citeMessages: normalizedCiteMessages,
        citeHistoryIds: citeHistoryIds?.filter(
          (historyId): historyId is string => Boolean(historyId?.trim()),
        ),
        fileList,
        fileListRef,
        files: fileListRef.current?.getFiles(),
        create_time: new Date().toISOString(),
        ...(runInBackground ? { run_in_background: true } : {}),
      };

      if (!isChatContent) {
        setPendingMessage(sendParams);
        setIsChatContent?.(true);
      } else {
        onSend?.(sendParams);
        clearMultiData();
      }

      hasSentMessageRef.current = true;
      setContextUsageReset((current) => current + 1);

      if (sessionId !== undefined) {
        debouncedSaveInput.cancel();
        clearInputContent(sessionId);
      }
      onChange("");
      setMentions([]);
      setText("");
      onClearCiteMessage?.();
    };

    const handleSkillDeposit = () => {
      if (isSkillDepositDisabled) {
        return;
      }
      onSkillDeposit?.();
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
          promptPolishOpenAPIRequest: {
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
      } catch {
        // API errors are reported by the shared request interceptor.
      } finally {
        setPolishingSuggestionKey(null);
      }
    };

    const handlePaste = useCallback(
      (e: React.ClipboardEvent<HTMLDivElement>) => {
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
              if (uploadTypes.includes(finalSuffix)) {
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
            message.warning(t("chat.unsupportedFileType", {
              types: t("chat.supportedUploadTypeSummary"),
            }));
          }

          if (files.length > 0) {
            fileListRef.current?.uploadFiles(files);
          }
        } else {
          const plainText = clipboardData.getData("text/plain");
          const imageSources = extractMarkdownImageSources(plainText);

          // Keep the structured editor plain-text only; pasted HTML must not be
          // able to manufacture trusted mention nodes.
          e.preventDefault();
          if (imageSources.length > 0) {
            e.stopPropagation();
            void Promise.all(imageSources.map(markdownImageToFile))
              .then((imageFiles) => {
                fileListRef.current?.uploadFiles(imageFiles);
                const remainingText = removeMarkdownImages(plainText);
                if (remainingText) {
                  document.execCommand("insertText", false, remainingText);
                }
              })
              .catch(() => {
                message.error(t("chat.fileUploadFailedRetry"));
                document.execCommand("insertText", false, plainText);
              });
            return;
          }

          document.execCommand("insertText", false, plainText);
        }
      },
      [
        disabled,
        disabledReason,
        fileList.length,
        t,
        uploadTypes,
      ],
    );

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
              <MentionEditor
                ref={textAreaRef}
                placeholder={placeholder || t("chat.inputPlaceholder")}
                value={value}
                onChange={handleInputChange}
                onMentionsChange={setMentions}
                disabledMentionReasons={knowledgeBaseEnabled ? undefined : {
                  knowledge_base: knowledgeBaseDisabledReason,
                }}
                onPaste={handlePaste}
                onCompositionChange={(composing) => {
                  isComposingRef.current = composing;
                }}
                onSend={() => {
                  if (
                    isComposingRef.current ||
                    isUploading ||
                    disabled ||
                    isPromptPolishing ||
                    isStreaming
                  ) return;
                  handleSend();
                  setNewMessage(false);
                }}
                disabled={disabled || isPromptPolishing}
              />

              <div className="input-bottom-actions">
                <div className="input-bottom-actions-left">
                  <div className="chat-add-resource">
                    <Popover
                      trigger="click"
                      open={addMenuOpen}
                      onOpenChange={(open) => {
                        if (open && (disabled || isPromptPolishing)) {
                          if (disabledReason) {
                            message.warning(disabledReason);
                          }
                          return;
                        }
                        setAddMenuOpen(open);
                      }}
                      placement="topLeft"
                      classNames={{ root: "chat-add-resource-popover" }}
                      content={
                        <div className="chat-add-resource-menu">
                            <button
                              type="button"
                              onClick={() => {
                                if (fileList.length >= MAX_UPLOAD_FILES) {
                                  message.warning(t("chat.maxFilesWarning"));
                                  setAddMenuOpen(false);
                                  return;
                                }
                                // Open the file picker while still inside the
                                // user gesture, then close the popover.
                                fileListRef.current?.openFileDialog();
                                setAddMenuOpen(false);
                              }}
                            >
                              <PaperClipOutlined />
                              {t("chat.addAttachment")}
                            </button>
                          <Tooltip title={knowledgeBaseEnabled ? undefined : knowledgeBaseDisabledReason}>
                            <span className="chat-add-resource-menu-tooltip-anchor">
                              <button
                                type="button"
                                disabled={!knowledgeBaseEnabled}
                                onClick={() => {
                                  setAddMenuOpen(false);
                                  knowledgeSelectorRef.current?.open(document.body);
                                }}
                              >
                                <BookOutlined />
                                {t("chat.knowledgeBase")}
                              </button>
                            </span>
                          </Tooltip>
                          <button
                            type="button"
                            onClick={() => {
                              setAddMenuOpen(false);
                              promptRef.current?.onOpen();
                            }}
                          >
                            <CommentOutlined />
                            {t("chat.promptTemplate")}
                          </button>
                        </div>
                      }
                    >
                      <Tooltip title={t("chat.addResourceTooltip")}>
                        <div
                          className={`input-bottom-actions-left-item${addMenuOpen ? " selected" : ""}${disabled || isPromptPolishing ? " is-disabled" : ""}`}
                          role="button"
                          tabIndex={disabled || isPromptPolishing ? -1 : 0}
                          aria-disabled={disabled || isPromptPolishing}
                        >
                          <AddIcon />
                          {t("chat.addResource")}
                        </div>
                      </Tooltip>
                    </Popover>
                    <div className="chat-add-resource-hidden-selector">
                      <ChatSelector
                        ref={knowledgeSelectorRef}
                        chatConfig={chatConfig ?? {}}
                        refreshKey={knowledgeRefreshKey}
                        embeddingReady={embeddingReady}
                        multimodalEmbeddingReady={multimodalEmbeddingReady}
                        rerankReady={rerankReady}
                        disabled={!knowledgeBaseEnabled}
                        disabledReason={knowledgeBaseDisabledReason}
                        onChange={onKnowledgeBaseChange}
                      />
                    </div>
                    <div className="chat-add-resource-hidden-upload">
                      <ImageUpload
                        updateFiles={updateImageList}
                        listNum={fileList.length}
                        ref={fileListRef}
                        types={uploadTypes}
                        max={MAX_UPLOAD_FILES}
                        onBeforeAddFiles={onBeforeAddFiles}
                        disabled={disabled || isPromptPolishing}
                        disabledReason={isPromptPolishing ? t("chat.promptPolishing") : disabledReason}
                        icon={<span />}
                      />
                    </div>
                  </div>
                  {showcaseSelection ? (
                    <div className="chat-showcase-selection" data-testid="showcase-selection">
                      <span className="chat-showcase-control chat-showcase-primary-control">
                        <AppstoreOutlined
                          className="chat-showcase-control-icon"
                          aria-hidden="true"
                        />
                        <Select
                          aria-label={showcaseSelection.ariaLabel}
                          className="chat-showcase-category-select"
                          size="small"
                          value={showcaseSelection.value}
                          disabled={disabled || isStreaming}
                          options={[
                            {
                              value: showcaseSelection.value,
                              label: showcaseSelection.label,
                            },
                          ]}
                        />
                      </span>
                    </div>
                  ) : null}
                  {showThinkingDepth && (
                    <Select
                      aria-label={t("chat.thinkingDepth")}
                      className="chat-thinking-depth-select"
                      size="small"
                      variant="borderless"
                      value={effectiveThinkingDepth}
                      disabled={disabled || isStreaming || Boolean(fixedThinkingDepth)}
                      onChange={setThinkingDepth}
                      options={THINKING_DEPTH_VALUES.map((value) => ({
                        value,
                        label: t(THINKING_DEPTH_LABEL_KEYS[value]),
                      }))}
                    />
                  )}
                  {/* <ModelSelector sessionId={sessionId} disabled={isStreaming} /> */}
                  {showHistoryButton && openHistory && (
                    <div
                      className={`input-bottom-actions-left-item ${showHistoryList ? "selected" : ""}`}
                      onClick={openHistory}
                    >
                      {t("chat.chatHistory")}
                    </div>
                  )}
                  {showSkillDeposit && isChatContent && (
                    <Tooltip title={skillDepositTooltip}>
                      <div
                        className={`input-bottom-actions-left-item skill-deposit-action${
                          isSkillDepositDisabled ? " is-disabled" : ""
                        }`}
                        aria-disabled={isSkillDepositDisabled}
                        role="button"
                        tabIndex={isSkillDepositDisabled ? -1 : 0}
                        onClick={handleSkillDeposit}
                        onKeyDown={(event) => {
                          if (
                            isSkillDepositDisabled ||
                            (event.key !== "Enter" && event.key !== " ")
                          ) {
                            return;
                          }
                          event.preventDefault();
                          handleSkillDeposit();
                        }}
                      >
                        <BulbOutlined />
                        {t("chat.skillDeposit")}
                      </div>
                    </Tooltip>
                  )}
                  {showConversationConfig && <ChatConfigModal
                    key={configResetKey != null ? `config-reset-${configResetKey}` : undefined}
                    conversationId={sessionId && !sessionId.startsWith("temp_") ? sessionId : undefined}
                    initialSettings={initialConversationSettings}
                    disabled={disabled || isStreaming}
                    hasWorkflowSession={hasWorkflowSession}
                    onSave={(settings) => {
                      setContextRuntimeSettings(settings);
                      onConversationSettingsChange?.(settings);
                    }}
                  />}
                  {showConversationConfig && sessionId && !sessionId.startsWith("temp_") && (
                    <DismissedWorkflowRestoreButton conversationId={sessionId} />
                  )}
                </div>

                <div className="input-bottom-actions-right">
                  {}
                  <div className="input-bottom-actions-right-item">
                    <ContextUsageButton
                      disabled={disabled || isUploading || isStreaming}
                      resetKey={`${sessionId ?? "new"}:${contextUsageReset}`}
                      staleKey={JSON.stringify({
                        text: value,
                        mentions: effectiveMentions.map((item) => [item.type, item.resource_id]),
                        files: fileList.map((item) => item.uid),
                        cites: normalizedCiteMessages,
                        knowledge: {
                          ids: chatConfig?.knowledgeBaseId ?? [],
                          creators: chatConfig?.creators ?? [],
                          tags: chatConfig?.tags ?? [],
                        },
                        runtime: contextRuntimeSettings,
                        thinkingDepth: effectiveThinkingDepth,
                      })}
                      buildRequest={() => {
                        const files = fileListRef.current?.getFiles() ?? [];
                        return {
                          ...(sessionId && !sessionId.startsWith("temp_")
                            ? { conversation_id: sessionId }
                            : {}),
                          input: [
                            {
                              input_type: "text",
                              text: buildCitedMessageText(value.trim(), normalizedCiteMessages),
                            },
                            ...files.map((file) => ({
                              input_type: allowedImageTypes.includes(
                                file.name.substring(file.name.lastIndexOf(".")).toLowerCase(),
                              )
                                ? "image"
                                : "file",
                              uri: file.uri,
                            })),
                          ],
                          mentions: effectiveMentions,
                          cite_messages: normalizedCiteMessages,
                          filters: {
                            kb_id: chatConfig?.knowledgeBaseId ?? [],
                            creator: chatConfig?.creators ?? [],
                            tags: chatConfig?.tags ?? [],
                          },
                          thinking_depth: effectiveThinkingDepth,
                          ...contextRuntimeSettings,
                        };
                      }}
                    />
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
                    polishingSuggestionKey === suggestion.key
                      ? " is-loading"
                      : ""
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
          onSelectPrompt={(prompt) => onChange(appendPromptToDraft(text, prompt))}
        />
        <BatchChatComponent ref={batchChatRef} cancelFn={() => {}} />
      </div>
    );
  },
);

ChatInput.displayName = "ChatInput";

export default ChatInput;
