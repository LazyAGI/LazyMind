
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
export const CHAT_SELECT_CONVERSATION_EVENT = "lazymind:chat-select-conversation";
export const CHAT_AUTO_ADVANCE_EVENT = "lazymind:chat-auto-advance";
export const CHAT_WORKFLOW_STEP_FEEDBACK_EVENT =
  "lazymind:chat-workflow-step-feedback";
export const CHAT_OPEN_MODEL_SELECTOR_EVENT =
  "lazymind:chat-open-model-selector";
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

function validConversationSources(values: unknown[]): string[] {
  return [...new Set(values.filter((value): value is string =>
    typeof value === "string" && ["lazymind", "codex", "cursor", "workbuddy"].includes(value),
  ))];
}

export function readKnownConversationSources(): string[] {
  try {
    const parsed: unknown = JSON.parse(sessionStorage.getItem(CHAT_KNOWN_CONVERSATION_SOURCES_KEY) || "[]");
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
    sessionStorage.setItem(CHAT_KNOWN_CONVERSATION_SOURCES_KEY, JSON.stringify(known));
  } catch { /* The mounted list can still use the returned options. */ }
  return known;
}

export function readChatConversationFilters(): ChatConversationFilters {
  try {
    const legacy = sessionStorage.getItem(CHAT_CONVERSATION_FILTER_KEY);
    let legacyValues: unknown[] = [];
    if (legacy?.startsWith("[")) {
      try {
        const parsed: unknown = JSON.parse(legacy);
        if (Array.isArray(parsed)) legacyValues = parsed;
      } catch { /* Ignore invalid legacy storage. */ }
    } else if (legacy) {
      legacyValues = [legacy];
    }
    const storedMode = sessionStorage.getItem(CHAT_CONVERSATION_MODE_KEY);
    const legacyMode = legacyValues.includes("task") !== legacyValues.includes("normal")
      ? (legacyValues.includes("task") ? "task" : "normal")
      : sessionStorage.getItem(CHAT_NEW_RUN_IN_BACKGROUND_KEY) === "1" ? "task" : "normal";
    const filter = storedMode === "task" || storedMode === "normal" ? storedMode : legacyMode;
    const storedSources = sessionStorage.getItem(CHAT_CONVERSATION_SOURCES_KEY);
    if (storedSources !== null) {
      let parsed: unknown;
      try { parsed = JSON.parse(storedSources); } catch { /* Keep the independent mode. */ }
      const sources = Array.isArray(parsed) ? validConversationSources(parsed) : [];
      return { filter, sources: sources.length ? sources : null };
    }
    const sources = validConversationSources(legacyValues.flatMap((value) =>
      value === "normal" ? ["lazymind"]
        : typeof value === "string" && value.startsWith("agent:") ? [value.slice(6)] : [],
    ));
    return { filter, sources: sources.length ? sources : null };
  } catch {
    return { filter: "normal", sources: null };
  }
}

function persistChatConversationFilters(filters: ChatConversationFilters) {
  try {
    sessionStorage.setItem(CHAT_CONVERSATION_MODE_KEY, filters.filter);
    sessionStorage.setItem(CHAT_CONVERSATION_SOURCES_KEY, JSON.stringify(filters.sources));
  } catch {
    // Ignore storage errors; the live event still updates the current sidebar.
  }
  window.dispatchEvent(
    new CustomEvent(CHAT_CONVERSATION_FILTER_EVENT, { detail: filters }),
  );
}

export function selectChatConversationFilter(filter: ChatConversationFilter) {
  persistChatConversationFilters({ ...readChatConversationFilters(), filter });
}

export function selectChatConversationSources(sources: string[]) {
  const valid = validConversationSources(sources);
  if (valid.length) persistChatConversationFilters({ ...readChatConversationFilters(), sources: valid });
}

export function revealChatConversation(conversation: { conversation_id?: string; is_task_conv?: boolean; assistant?: string } | undefined) {
  if (!conversation) return;
  const current = readChatConversationFilters();
  const filter = conversation.is_task_conv ? "task" : "normal";
  const assistant = validConversationSources([conversation.assistant || "lazymind"])[0];
  const sources = current.sources && assistant && !current.sources.includes(assistant)
    ? [...current.sources, assistant] : current.sources;
  if (filter !== current.filter || sources !== current.sources) {
    persistChatConversationFilters({ filter, sources });
  }
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
  message: string;
  status?: string;
}

export interface ChatConversationActivityDetail {
  conversationId: string;
  /** When set on a conversation not yet in the sidebar list, insert it at the top. */
  displayName?: string;
}
