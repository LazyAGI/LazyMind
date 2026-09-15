export const SENSITIVE_WORD_FILTER_STORAGE_KEY = "lazymind:sensitive-word-filter-enabled";
export const SENSITIVE_WORD_FILTER_EVENT = "lazymind:sensitive-word-filter-change";

const CHAT_SSE_PATHS = ["conversations:chat", "conversations:resumeChat"];
const DEVELOPER_ACTIVE_STORAGE_KEY = "lazymind:developer-active";

export function isSensitiveWordFilterEnabled() {
  try {
    return localStorage.getItem(SENSITIVE_WORD_FILTER_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function setSensitiveWordFilterEnabled(enabled: boolean) {
  try {
    if (enabled) {
      localStorage.setItem(SENSITIVE_WORD_FILTER_STORAGE_KEY, "1");
    } else {
      localStorage.removeItem(SENSITIVE_WORD_FILTER_STORAGE_KEY);
    }
  } catch {
    // Ignore storage errors.
  }

  window.dispatchEvent(
    new CustomEvent(SENSITIVE_WORD_FILTER_EVENT, { detail: { enabled } }),
  );
}

export async function syncSensitiveWordFilterFromServer(): Promise<boolean> {
  try {
    const { fetchUserUiPreferences } = await import("@/modules/user/uiPreferencesApi");
    const prefs = await fetchUserUiPreferences();
    const enabled = Boolean(prefs.sensitive_word_filter_enabled);
    setSensitiveWordFilterEnabled(enabled);
    return enabled;
  } catch (error) {
    console.error("Failed to sync sensitive-word filter from server:", error);
    return isSensitiveWordFilterEnabled();
  }
}

export async function persistSensitiveWordFilterEnabled(enabled: boolean) {
  setSensitiveWordFilterEnabled(enabled);
  try {
    const { patchUserUiPreferences } = await import("@/modules/user/uiPreferencesApi");
    await patchUserUiPreferences({ sensitive_word_filter_enabled: enabled });
  } catch (error) {
    console.error("Failed to persist sensitive-word filter:", error);
  }
}

function isDeveloperModeActiveCached() {
  try {
    return localStorage.getItem(DEVELOPER_ACTIVE_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function skipSensitiveFilterChatField(): { skip_sensitive_filter: boolean } {
  return {
    skip_sensitive_filter: !(isDeveloperModeActiveCached() && isSensitiveWordFilterEnabled()),
  };
}

export function applySkipSensitiveFilterToChatPayload(url: string, payload: string): string {
  if (!payload || !CHAT_SSE_PATHS.some((path) => url.includes(path))) {
    return payload;
  }
  try {
    const body = JSON.parse(payload) as Record<string, unknown>;
    if (!body || typeof body !== "object" || Array.isArray(body)) {
      return payload;
    }
    return JSON.stringify({ ...body, ...skipSensitiveFilterChatField() });
  } catch {
    return payload;
  }
}
