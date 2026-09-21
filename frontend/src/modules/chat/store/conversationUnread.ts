import { create } from "zustand";

interface ConversationUnreadState {
  counts: Record<string, number>;
  setCount: (conversationId: string, count: number) => void;
}

export const useConversationUnreadStore = create<ConversationUnreadState>((set) => ({
  counts: {},
  setCount: (conversationId, count) => set((state) => {
    if (!conversationId || (state.counts[conversationId] || 0) === count) return state;
    const counts = { ...state.counts };
    if (count > 0) counts[conversationId] = count;
    else delete counts[conversationId];
    return { counts };
  }),
}));
