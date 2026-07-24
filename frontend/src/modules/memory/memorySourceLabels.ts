export type MemorySourceLabelKey =
  | "admin.memorySourceChatExplicit"
  | "admin.memorySourceMemoryReview";

export const getMemorySourceLabelKey = (
  sourceKind: string,
): MemorySourceLabelKey | null => {
  const normalized = sourceKind.trim().toLowerCase();
  if (normalized === "chat_explicit") {
    return "admin.memorySourceChatExplicit";
  }
  if (normalized === "memory_review") {
    return "admin.memorySourceMemoryReview";
  }
  return null;
};
