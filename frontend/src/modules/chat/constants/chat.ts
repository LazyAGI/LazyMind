import { AUTH_USER_STORAGE_KEY, principalFor } from "@/components/authPrincipal";
import { AGENTS } from "@/modules/agentIntegration/catalog";

export const CHAT_HOME_PATH = "/agent/chat/home";
// Kept as a deprecated export so Vite can hot-reload modules compiled before
// route-based conversation restoration replaced this storage key.
export const CHAT_RESUME_CONVERSATION_KEY = "chat_resume_conversation_id";
export const CHAT_NEW_RUN_IN_BACKGROUND_KEY = "chat_new_run_in_background";
export const CHAT_CONVERSATION_FILTER_KEY = "chat_conversation_filter";
export const CHAT_CONVERSATION_MODE_KEY = "chat_conversation_mode";
export const CHAT_CONVERSATION_SOURCES_KEY = "chat_conversation_sources";
const CHAT_KNOWN_CONVERSATION_SOURCES_KEY = "chat_known_conversation_sources";
export const CHAT_CONVERSATION_FILTER_EVENT = "lazymind:chat-conversation-filter";
export const CHAT_CAPTURE_MODE_DRAFT_EVENT = "lazymind:chat-capture-mode-draft";
export const CHAT_SELECT_CONVERSATION_EVENT = "lazymind:chat-select-conversation";
export const CHAT_AUTO_ADVANCE_EVENT = "lazymind:chat-auto-advance";
export const CHAT_KNOWLEDGE_CONFIRMATION_EVENT = "lazymind:chat-knowledge-confirmation";
export interface ChatKnowledgeConfirmationDetail {
  conversationId: string;
  historyId: string;
  confirmationId: string;
}
export const CHAT_WORKFLOW_STEP_FEEDBACK_EVENT =
  "lazymind:chat-workflow-step-feedback";
export const CHAT_OPEN_MODEL_SELECTOR_EVENT =
  "lazymind:chat-open-model-selector";
export const CHAT_MODEL_SWITCH_EVENT = "lazymind:chat-model-switch";
export const CHAT_MODEL_SELECTION_CHANGED_EVENT = "lazymind:chat-model-selection-changed";
export interface ChatModelSwitchNoticeDetail {
  conversationId: string;
  runId: string;
  record: import("@/api/generated/core-client").ChatModelSwitchRecord;
  live: boolean;
}
export const CHAT_FFMPEG_DEPENDENCY_MISSING_EVENT =
  "lazymind:chat-ffmpeg-dependency-missing";
export const CHAT_MEDIA_CAPABILITY_MISSING_EVENT =
  "lazymind:chat-media-capability-missing";
export const CHAT_CONVERSATION_ACTIVITY_EVENT =
  "lazymind:chat-conversation-activity";
export const CHAT_CONVERSATION_LIST_REFRESH_EVENT =
  "lazymind:chat-conversation-list-refresh";
export const CONVERSATION_TITLE_CHANGED_EVENT = "lazymind:conversation-title-changed";
export interface ConversationTitleChangedDetail {
  conversationId: string;
  displayName: string;
  titleRevision: number;
}
export const CHAT_SUBMIT_INPUT_EVENT = "lazymind:chat-submit-input";
export const CHAT_OPEN_ARTIFACT_PANEL_EVENT = "lazymind:chat-open-artifact-panel";

export interface ChatOpenArtifactPanelDetail {
  conversationId: string;
}

export function openConversationArtifactPanel(detail: ChatOpenArtifactPanelDetail) {
  window.dispatchEvent(
    new CustomEvent(CHAT_OPEN_ARTIFACT_PANEL_EVENT, { detail }),
  );
}
export const CHAT_PENDING_CONVERSATION_GROUP_KEY = "lazymind:pending-conversation-group";
export const CHAT_PENDING_CONVERSATION_PROMPT_KEY = "lazymind:pending-conversation-prompt";
export const WORKFLOW_PANEL_EXPANDED_EVENT = "lazymind:workflow-panel-expanded";
export const WORKFLOW_PANEL_EXPANDED_STORAGE_PREFIX =
  "lazymind:workflow-panel-expanded:";

export function getChatConversationPath(conversationId: string) {
  return `${CHAT_HOME_PATH}/${encodeURIComponent(conversationId)}`;
}

export type ChatConversationFilter = "normal" | "task";

export interface ChatConversationFilters {
  filter: ChatConversationFilter;
  // null means all sources, including newly connected assistants.
  sources: string[] | null;
}

const supportedConversationSources = new Set<string>([
  "lazymind", ...AGENTS.filter(agent => agent.conversationSourceLabel).map(agent => agent.id),
]);

function validConversationSources(values: unknown[]): string[] {
  return [...new Set(values.filter((value): value is string =>
    typeof value === "string" && supportedConversationSources.has(value),
  ))];
}

export function readKnownConversationSources(): string[] {
  try {
    const parsed: unknown = JSON.parse(sessionStorage.getItem(`${preferenceKey()}:${CHAT_KNOWN_CONVERSATION_SOURCES_KEY}`) || "[]");
    return Array.isArray(parsed) ? validConversationSources(parsed) : [];
  } catch {
    return [];
  }
}

// Keep discovered sources selectable even when their last visible row is
// filtered out or their connector is offline. This cache lasts for this tab.
export function rememberConversationSources(sources: string[]): string[] {
  const known = validConversationSources([...readKnownConversationSources(), ...sources]);
  try {
    sessionStorage.setItem(`${preferenceKey()}:${CHAT_KNOWN_CONVERSATION_SOURCES_KEY}`, JSON.stringify(known));
  } catch { /* The mounted list can still use the returned options. */ }
  return known;
}

function preferenceKey() {
  let user = null;
  try { user = JSON.parse(localStorage.getItem(AUTH_USER_STORAGE_KEY) || "null"); } catch { /* No readable profile yet. */ }
  return `lazymind:chat-conversation-filters:${principalFor(user)}`;
}

let liveFilters: { key: string; value: ChatConversationFilters } | undefined;

function readSavedFilters(key: string): ChatConversationFilters {
  const fallback: ChatConversationFilters = { filter: "normal", sources: ["lazymind"] };
  try {
    const saved = localStorage.getItem(key);
    if (saved !== null) {
      const value = JSON.parse(saved);
      const sources = Array.isArray(value?.sources) ? validConversationSources(value.sources) : [];
      return { filter: value?.filter === "task" ? "task" : "normal", sources: sources.length ? sources : fallback.sources };
    }

    const legacy = sessionStorage.getItem(CHAT_CONVERSATION_FILTER_KEY);
    let values: unknown[] = [];
    if (legacy) {
      try {
        const parsed = legacy.startsWith("[") ? JSON.parse(legacy) : [legacy];
        if (Array.isArray(parsed)) values = parsed;
      } catch { /* The independent mode and source keys can still be recovered. */ }
    }
    const storedMode = sessionStorage.getItem(CHAT_CONVERSATION_MODE_KEY);
    const filter = storedMode === "normal" || storedMode === "task" ? storedMode
      : values.includes("task") !== values.includes("normal") ? (values.includes("task") ? "task" : "normal")
      : sessionStorage.getItem(CHAT_NEW_RUN_IN_BACKGROUND_KEY) === "1" ? "task" : "normal";
    const raw = sessionStorage.getItem(CHAT_CONVERSATION_SOURCES_KEY);
    let selected: unknown;
    try { selected = raw === null ? undefined : JSON.parse(raw); } catch { /* Preserve the saved mode. */ }
    const legacySources = values.flatMap(value => value === "normal" ? ["lazymind"]
      : typeof value === "string" && value.startsWith("agent:") ? [value.slice(6)] : []);
    const sources = validConversationSources(raw === null ? legacySources : Array.isArray(selected) ? selected : []);
    const filters = { filter, sources: sources.length ? sources : fallback.sources } as ChatConversationFilters;
    if (legacy || storedMode || raw) {
      localStorage.setItem(key, JSON.stringify(filters));
      for (const old of [CHAT_CONVERSATION_FILTER_KEY, CHAT_CONVERSATION_MODE_KEY, CHAT_CONVERSATION_SOURCES_KEY]) sessionStorage.removeItem(old);
    }
    return filters;
  } catch {
    return fallback;
  }
}

export function readChatConversationFilters(): ChatConversationFilters {
  const key = preferenceKey();
  return liveFilters?.key === key ? liveFilters.value : readSavedFilters(key);
}

function applyChatConversationFilters(filters: ChatConversationFilters, saved?: ChatConversationFilters) {
  const key = preferenceKey();
  liveFilters = { key, value: filters };
  if (saved) {
    try {
      localStorage.setItem(key, JSON.stringify(saved));
      if (filters.filter === saved.filter && JSON.stringify(filters.sources) === JSON.stringify(saved.sources)) {
        liveFilters = undefined;
      }
    } catch { /* The live selection still applies. */ }
  }
  window.dispatchEvent(new CustomEvent(CHAT_CONVERSATION_FILTER_EVENT, { detail: filters }));
}

export function selectChatConversationFilter(filter: ChatConversationFilter, { remember = true } = {}) {
  applyChatConversationFilters({ ...readChatConversationFilters(), filter },
    remember ? { ...readSavedFilters(preferenceKey()), filter } : undefined);
}

export function selectChatConversationSources(sources: string[]) {
  const valid = validConversationSources(sources);
  if (!valid.length) return;
  const filters = { ...readChatConversationFilters(), sources: valid };
  applyChatConversationFilters(filters, { ...readSavedFilters(preferenceKey()), sources: valid });
}

export function revealChatConversation(conversation: { conversation_id?: string; is_task_conv?: boolean; assistant?: string } | undefined) {
  if (!conversation) return;
  const current = readChatConversationFilters();
  const filter = conversation.is_task_conv ? "task" : "normal";
  const assistant = validConversationSources([conversation.assistant || "lazymind"])[0];
  const sources = current.sources && assistant && !current.sources.includes(assistant)
    ? [...current.sources, assistant] : current.sources;
  if (filter !== current.filter || sources !== current.sources) applyChatConversationFilters({ filter, sources });
}

export type ChatAutoAdvancePhase = "append" | "resume";

export interface ChatAutoAdvanceDetail {
  conversationId: string;
  driverMessage?: string;
  phase: ChatAutoAdvancePhase;
}

export interface ChatWorkflowStepFeedbackDetail {
  conversationId: string;
  feedbackId: string;
  historyId?: string;
  message?: string;
  status?: string;
}

export interface ChatConversationActivityDetail {
  conversationId: string;
  /** When set on a conversation not yet in the sidebar list, insert it at the top. */
  displayName?: string;
}

export const CHAT_ENTRY_DEFAULTS_CHANGED_EVENT = 'chat:entry-defaults-changed';
