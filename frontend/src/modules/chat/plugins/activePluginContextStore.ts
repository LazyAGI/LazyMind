import { create } from 'zustand';
import type { PluginContext } from './types';

interface ActivePluginContextStore {
  context: PluginContext | null;
  setContext: (ctx: PluginContext | null) => void;
  requestAdvance: () => void;
  clearAdvance: () => void;
  clearContext: () => void;
}

export const useActivePluginContextStore = create<ActivePluginContextStore>((set) => ({
  context: null,

  setContext: (ctx) => set({ context: ctx }),

  requestAdvance: () =>
    set((state) => ({
      context: state.context ? { ...state.context, advance: true } : null,
    })),

  clearAdvance: () =>
    set((state) => ({
      context: state.context ? { ...state.context, advance: false } : null,
    })),

  clearContext: () => set({ context: null }),
}));
