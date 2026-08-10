import type { PreferenceMemoryItem } from "./currentMemoryApi";

export type PreferenceResidentUsageTone = "normal" | "warning" | "error";

export const getPreferenceResidentUsageTone = (
  usedItems: number,
  maxItems: number,
  overLimit: boolean,
): PreferenceResidentUsageTone => {
  const ratio = usedItems / maxItems;
  if (overLimit || ratio >= 1) {
    return "error";
  }
  return ratio >= 0.8 ? "warning" : "normal";
};

export const isPreferenceResident = (
  index: number,
  maxItems?: number,
): boolean => maxItems === undefined || index < maxItems;

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
