import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { ResumePresentation } from '../utils/resumePresentation';

export interface FeaturedSelection { caseId: string; taskId?: string; resumePresentation?: ResumePresentation }

export const useFeaturedSelectionStore = create<{
  selections: Record<string, FeaturedSelection>;
  setSelection: (key: string, selection?: FeaturedSelection) => void;
  moveSelection: (from: string, to: string) => void;
}>()(persist((set) => ({
  selections: {},
  setSelection: (key, selection) => set(state => {
    const selections = { ...state.selections };
    if (selection) {
      const previous = state.selections[key];
      selections[key] = selection.resumePresentation || previous?.caseId !== selection.caseId || !previous.resumePresentation
        ? selection : { ...selection, resumePresentation: previous.resumePresentation };
    } else delete selections[key];
    return { selections };
  }),
  moveSelection: (from, to) => set(state => {
    if (from === to || !state.selections[from]) return state;
    const selections = { ...state.selections, [to]: state.selections[from] };
    delete selections[from];
    return { selections };
  }),
}), { name: 'chat-featured-selections', partialize: state => ({ selections: state.selections }) }));
