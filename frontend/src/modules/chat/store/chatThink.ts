import { create } from "zustand";
import { persist } from "zustand/middleware";

export type ThinkingDepth = "low" | "medium" | "high" | "max";

export const THINKING_DEPTH_VALUES: ThinkingDepth[] = [
  "low",
  "medium",
  "high",
  "max",
];

interface ChatThinkStore {
  think: boolean;
  thinkingDepth: ThinkingDepth;
  setThink: (think: boolean) => void;
  setThinkingDepth: (depth: ThinkingDepth) => void;
}

export const useChatThinkStore = create<ChatThinkStore>()(
  persist(
    (set) => ({
      think: false,
      thinkingDepth: "medium",
      setThink: (think: boolean) => set({ think }),
      setThinkingDepth: (thinkingDepth: ThinkingDepth) =>
        set({ thinkingDepth }),
    }),
    {
      name: "chat-thinking-depth",
      partialize: (state) => ({ thinkingDepth: state.thinkingDepth }),
    },
  ),
);
