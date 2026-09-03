import { isDeveloperModeActive } from "./developerMode";

export const SENSITIVE_WORD_FILTER_STORAGE_KEY = "lazymind:sensitive-word-filter-enabled";
export const SENSITIVE_WORD_FILTER_EVENT = "lazymind:sensitive-word-filter-change";

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

export function skipSensitiveFilterChatField(): { skip_sensitive_filter?: true } {
  return isDeveloperModeActive() && isSensitiveWordFilterEnabled()
    ? {}
    : { skip_sensitive_filter: true };
}
