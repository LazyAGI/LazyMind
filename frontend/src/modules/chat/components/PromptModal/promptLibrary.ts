import type { PromptItem } from "@/api/generated/core-client";

export function appendPromptToDraft(draft: string, prompt: string): string {
  const normalizedPrompt = prompt.trim();
  if (!normalizedPrompt) return draft;
  if (!draft) return normalizedPrompt;
  return `${draft.trimEnd()}\n${normalizedPrompt}`;
}

export function canManagePrompt(prompt: PromptItem): boolean {
  return prompt.source === "custom";
}

export function setPromptFavorite(
  prompts: PromptItem[],
  promptID: string,
  isFavorite: boolean,
): PromptItem[] {
  return prompts.map((prompt) =>
    prompt.id === promptID ? { ...prompt, is_favorite: isFavorite } : prompt,
  );
}
