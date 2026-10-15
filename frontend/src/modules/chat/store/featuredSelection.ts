import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export interface FeaturedSelection { caseId: string; taskId?: string }

export const useFeaturedSelectionStore = create<{
  selections: Record<string, FeaturedSelection>;
  setSelection: (key: string, selection?: FeaturedSelection) => void;
  moveSelection: (from: string, to: string) => void;
}>()(persist((set) => ({
  selections: {},
  setSelection: (key, selection) => set(state => {
    const selections = { ...state.selections };
    if (selection) selections[key] = selection; else delete selections[key];
    return { selections };
  }),
  moveSelection: (from, to) => set(state => {
    if (from === to || !state.selections[from]) return state;
    const selections = { ...state.selections, [to]: state.selections[from] };
    delete selections[from];
    return { selections };
  }),
}), { name: 'chat-featured-selections', partialize: state => ({ selections: state.selections }) }));
