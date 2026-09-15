import type { PreferenceMemoryItem, PreferenceOrganizerTask } from "./currentMemoryApi";

export type PreferenceBudgetTone = "normal" | "warning" | "full" | "error";

export const getPreferenceBudgetTone = (used: number, max: number): PreferenceBudgetTone => {
  if (used > max) return "error";
  if (used === max) return "full";
  return used * 100 >= max * 80 ? "warning" : "normal";
};

export const preferenceOrganizerPresentation = (task: PreferenceOrganizerTask | null) => {
  if (!task) return null;
  if (task.status === "pending") return { key: task.waiting_reason === "memory_review"
    ? "admin.memoryPreferenceOrganizeWaitingReview" : "admin.memoryPreferenceOrganizeWaiting", tone: "info" as const };
  if (task.status === "running") return { key: "admin.memoryPreferenceOrganizeRunning", tone: "info" as const };
  if (task.result?.outcome === "partial") return { key: "admin.memoryPreferenceOrganizePartial", tone: "warning" as const };
  if (task.status === "failed") return { key: "admin.memoryPreferenceOrganizeFailed", tone: "error" as const };
  return { key: task.result?.outcome === "no_safe_changes"
    ? "admin.memoryPreferenceOrganizeNoChanges" : "admin.memoryPreferenceOrganizeDone", tone: "success" as const };
};

export const movePreferenceItem = <T extends PreferenceMemoryItem>(
  items: T[],
  activeName: string,
  overName: string,
): T[] => {
  const fromIndex = items.findIndex((item) => item.name === activeName);
  const toIndex = items.findIndex((item) => item.name === overName);
  if (fromIndex < 0 || toIndex < 0 || fromIndex === toIndex) {
    return items;
  }

  const next = [...items];
  const [moved] = next.splice(fromIndex, 1);
  next.splice(toIndex, 0, moved);
  return next;
};

export const mergePreferenceOrderWithLatest = <
  T extends PreferenceMemoryItem,
>(
  localItems: T[],
  latestItems: T[],
): T[] => {
  const latestByName = new Map(
    latestItems.map((item) => [item.name, item]),
  );
  const ordered: T[] = [];

  localItems.forEach((item) => {
    const latest = latestByName.get(item.name);
    if (latest) {
      ordered.push(latest);
      latestByName.delete(item.name);
    }
  });
  latestItems.forEach((item) => {
    if (latestByName.has(item.name)) {
      ordered.push(item);
      latestByName.delete(item.name);
    }
  });
  return ordered;
};

export const isCurrentMemoryConflict = (error: unknown): boolean => {
  if (!error || typeof error !== "object") {
    return false;
  }
  const response = (error as { response?: unknown }).response;
  if (!response || typeof response !== "object") {
    return false;
  }
  return (response as { status?: unknown }).status === 409;
};

export const isCurrentMemoryResourceNotFound = (
  error: unknown,
): boolean => {
  if (!error || typeof error !== "object") {
    return false;
  }
  const response = (error as { response?: unknown }).response;
  if (!response || typeof response !== "object") {
    return false;
  }
  const { data, status } = response as {
    data?: unknown;
    status?: unknown;
  };
  if (status !== 404 || !data || typeof data !== "object") {
    return false;
  }
  return (
    (data as { message?: unknown }).message ===
    "current memory resource not found"
  );
};
