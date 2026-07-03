import {
  fetchUserUiPreferences,
  patchUserUiPreferences,
} from "@/modules/user/uiPreferencesApi";

export const DEVELOPER_ACTIVE_STORAGE_KEY = "lazymind:developer-active";
export const DEVELOPER_ACTIVE_EVENT = "lazymind:developer-active-change";

export function isDeveloperModeActive() {
  try {
    return localStorage.getItem(DEVELOPER_ACTIVE_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function setDeveloperModeActive(active: boolean) {
  try {
    if (active) {
      localStorage.setItem(DEVELOPER_ACTIVE_STORAGE_KEY, "1");
    } else {
      localStorage.removeItem(DEVELOPER_ACTIVE_STORAGE_KEY);
    }
  } catch {
    // Ignore storage errors.
  }

  window.dispatchEvent(
    new CustomEvent(DEVELOPER_ACTIVE_EVENT, { detail: { active } }),
  );
}

export async function syncDeveloperModeFromServer(): Promise<boolean> {
  try {
    const prefs = await fetchUserUiPreferences();
    setDeveloperModeActive(prefs.developer_mode_active);
    return prefs.developer_mode_active;
  } catch (error) {
    console.error("Failed to sync developer mode from server:", error);
    return isDeveloperModeActive();
  }
}

export async function persistDeveloperModeActive(active: boolean) {
  setDeveloperModeActive(active);
  try {
    await patchUserUiPreferences({ developer_mode_active: active });
  } catch (error) {
    console.error("Failed to persist developer mode:", error);
  }
}
