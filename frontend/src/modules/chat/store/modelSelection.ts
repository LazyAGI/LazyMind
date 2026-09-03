import { create } from "zustand";
import type {
  ChatModelListOpenAPIItem,
  ChatModelProviderOpenAPIItem,
  ChatModelSelectionOpenAPI,
  ChatModelsOpenAPIResponse,
  PatchConversationModelOpenAPIRequest,
} from "@/api/generated/core-client";

export type ChatModelSelectionRequest = Pick<
  PatchConversationModelOpenAPIRequest,
  "mode" | "model_id"
>;

export type ChatModelSelection = Pick<ChatModelSelectionOpenAPI, "mode" | "version"> &
  Partial<ChatModelSelectionOpenAPI>;

// Older catalogs use is_* badges; incomplete local selections are also allowed.
export type ChatModelOption = Pick<ChatModelListOpenAPIItem, "id" | "name"> &
  Partial<ChatModelListOpenAPIItem> & {
    is_default?: boolean;
    is_shared?: boolean;
    is_recommended?: boolean;
    is_low_cost?: boolean;
    available?: boolean;
  };

export type ChatModelProvider = Pick<ChatModelProviderOpenAPIItem, "id" | "name"> & {
  source?: ChatModelProviderOpenAPIItem["source"];
  models: ChatModelOption[];
};

export type ChatModelCatalog = Omit<
  ChatModelsOpenAPIResponse,
  "selection" | "default_selection" | "providers"
> & {
  selection: ChatModelSelection;
  default_selection?: ChatModelSelection;
  providers: ChatModelProvider[];
};

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
