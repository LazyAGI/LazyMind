import { create } from "zustand";

export type ChatModelSelectionMode = "fixed" | "auto";

export interface ChatModelSelectionRequest {
  mode: ChatModelSelectionMode;
  model_id?: string;
}

export interface ChatModelSelection extends ChatModelSelectionRequest {
  provider_name?: string;
  model_name?: string;
  group_name?: string;
  source?: string;
  availability?: string;
  version: number;
}

export interface ChatModelOption {
  id: string;
  name: string;
  group_id?: string;
  group_name?: string;
  source?: string;
  badges?: string[];
  availability?: string;
  current?: boolean;
  default?: boolean;
  shared?: boolean;
  is_default?: boolean;
  is_shared?: boolean;
  is_recommended?: boolean;
  is_low_cost?: boolean;
  capabilities?: string[];
  available?: boolean;
}

export interface ChatModelProvider {
  id: string;
  name: string;
  source?: string;
  models: ChatModelOption[];
}

export interface ChatModelCatalog {
  selection: ChatModelSelection;
  default_selection?: ChatModelSelection;
  providers: ChatModelProvider[];
  switch_allowed: boolean;
  switch_blocked_reason?: string;
  auto_available: boolean;
}

export const NEW_CHAT_MODEL_SELECTION_KEY = "__new_chat__";

export function chatModelSelectionKey(conversationId?: string): string {
  const normalized = conversationId?.trim();
  return normalized && !normalized.startsWith("temp_")
    ? normalized
    : NEW_CHAT_MODEL_SELECTION_KEY;
}

interface ModelSelectionStore {
  selections: Record<string, ChatModelSelection | undefined>;
  setSelection: (key: string, selection: ChatModelSelection) => void;
  clearSelection: (key: string) => void;
  resetForNewChat: () => void;
}

export const useModelSelectionStore = create<ModelSelectionStore>()((set) => ({
  selections: {},
  setSelection: (key, selection) =>
    set((state) => ({
      selections: { ...state.selections, [key]: selection },
    })),
  clearSelection: (key) =>
    set((state) => {
      if (!(key in state.selections)) return state;
      const selections = { ...state.selections };
      delete selections[key];
      return { selections };
    }),
  resetForNewChat: () =>
    set((state) => {
      if (!(NEW_CHAT_MODEL_SELECTION_KEY in state.selections)) return state;
      const selections = { ...state.selections };
      delete selections[NEW_CHAT_MODEL_SELECTION_KEY];
      return { selections };
    }),
}));

export function toChatModelSelectionRequest(
  selection?: ChatModelSelection,
): ChatModelSelectionRequest | undefined {
  if (!selection) return undefined;
  if (selection.mode === "auto") return { mode: "auto" };
  return selection.model_id
    ? { mode: "fixed", model_id: selection.model_id }
    : undefined;
}
