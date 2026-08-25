import { create } from "zustand";
import { subscribeWithSelector } from "zustand/middleware";
import type { SendMessageParams } from "@/modules/chat/components/ChatInput/types";

interface ChatMessageStore {
  pendingMessage: SendMessageParams | null;
  setPendingMessage: (message: SendMessageParams | null) => void;
  clearPendingMessage: () => void;
}

export const useChatMessageStore = create<ChatMessageStore>()(
  subscribeWithSelector((set) => ({
    pendingMessage: null,
    setPendingMessage: (message) => set({ pendingMessage: message }),
    clearPendingMessage: () => set({ pendingMessage: null }),
  })),
);
