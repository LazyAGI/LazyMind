import { create } from "zustand";

// There is only one model mode now (LazyMind). This store is kept as a
// thin wrapper so that callsites that reference getModelSelection /
// setModelSelection don't need to be touched, but the "deepseek" and
// "both" concepts are completely removed.

export type ModelSelectionType = "value_engineering";

interface ModelSelectionStore {
  getModelSelection: (conversationId: string) => ModelSelectionType;
  setModelSelection: (
    conversationId: string,
    selection: ModelSelectionType,
  ) => void;
  resetForNewChat: () => void;
  clearModelSelection: (conversationId: string) => void;
}

export const useModelSelectionStore = create<ModelSelectionStore>()(() => ({
  getModelSelection: (_conversationId: string): ModelSelectionType =>
    "value_engineering",
  setModelSelection: (
    _conversationId: string,
    _selection: ModelSelectionType,
  ) => {},
  resetForNewChat: () => {},
  clearModelSelection: (_conversationId: string) => {},
}));

export function parseModelSelectionFromModels(
  _models?: string[],
): ModelSelectionType {
  return "value_engineering";
}
