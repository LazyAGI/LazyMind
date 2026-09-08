export const PERFORMANCE_STATS_STORAGE_KEY = "lazymind:performance-stats-enabled";
export const PERFORMANCE_STATS_EVENT = "lazymind:performance-stats-change";

export function isPerformanceStatsEnabled(): boolean {
  try {
    return localStorage.getItem(PERFORMANCE_STATS_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function setPerformanceStatsEnabled(enabled: boolean): void {
  try {
    if (enabled) localStorage.setItem(PERFORMANCE_STATS_STORAGE_KEY, "1");
    else localStorage.removeItem(PERFORMANCE_STATS_STORAGE_KEY);
  } catch {
    // Ignore storage errors.
  }
  window.dispatchEvent(new CustomEvent(PERFORMANCE_STATS_EVENT, {
    detail: { performance_stats_enabled: enabled },
  }));
}

export async function syncPerformanceStatsFromServer(): Promise<boolean> {
  try {
    const { fetchUserUiPreferences } = await import("@/modules/user/uiPreferencesApi");
    const enabled = Boolean((await fetchUserUiPreferences()).performance_stats_enabled);
    setPerformanceStatsEnabled(enabled);
    return enabled;
  } catch (error) {
    console.error("Failed to sync performance stats preference:", error);
    return isPerformanceStatsEnabled();
  }
}

export async function persistPerformanceStatsEnabled(enabled: boolean): Promise<void> {
  setPerformanceStatsEnabled(enabled);
  try {
    const { patchUserUiPreferences } = await import("@/modules/user/uiPreferencesApi");
    await patchUserUiPreferences({ performance_stats_enabled: enabled });
  } catch (error) {
    console.error("Failed to persist performance stats preference:", error);
  }
}
