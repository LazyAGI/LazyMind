import { create } from "zustand";
import { persist } from "zustand/middleware";

export interface ArtifactRef {
  artifact_key: string;
  slot_id: string;
  sort_order?: number;
  content_type: string;
  /** Short text preview or URL for display. */
  preview?: string;
}

interface ChatInputStore {
  inputContents: Record<string, string>;
  /** Pending artifact references to inject into the next message. Keyed by conversationId. */
  artifactRefs: Record<string, ArtifactRef[]>;
  saveInputContent: (conversationId: string, content: string) => void;
  getInputContent: (conversationId: string) => string;
  clearInputContent: (conversationId: string) => void;
  clearAllInputContents: () => void;
  addArtifactRef: (conversationId: string, ref: ArtifactRef) => void;
  removeArtifactRef: (conversationId: string, artifactKey: string, sortOrder?: number) => void;
  clearArtifactRefs: (conversationId: string) => void;
  getArtifactRefs: (conversationId: string) => ArtifactRef[];
}

export const useChatInputStore = create<ChatInputStore>()(
  persist(
    (set, get) => ({
      inputContents: {},
      artifactRefs: {},
      saveInputContent: (conversationId: string, content: string) => {
        set((state) => ({
          inputContents: {
            ...state.inputContents,
            [conversationId]: content,
          },
        }));
      },
      getInputContent: (conversationId: string) => {
        return get().inputContents[conversationId] || "";
      },
      clearInputContent: (conversationId: string) => {
        set((state) => {
          const newContents = { ...state.inputContents };
          delete newContents[conversationId];
          return { inputContents: newContents };
        });
      },
      clearAllInputContents: () => {
        set({ inputContents: {} });
      },
      addArtifactRef: (conversationId: string, ref: ArtifactRef) => {
        set((state) => {
          const existing = state.artifactRefs[conversationId] ?? [];
          const filtered = existing.filter(
            (r) => !(r.artifact_key === ref.artifact_key && r.sort_order === ref.sort_order),
          );
          return {
            artifactRefs: {
              ...state.artifactRefs,
              [conversationId]: [...filtered, ref],
            },
          };
        });
      },
      removeArtifactRef: (conversationId: string, artifactKey: string, sortOrder?: number) => {
        set((state) => {
          const existing = state.artifactRefs[conversationId] ?? [];
          return {
            artifactRefs: {
              ...state.artifactRefs,
              [conversationId]: existing.filter(
                (r) => !(r.artifact_key === artifactKey && r.sort_order === sortOrder),
              ),
            },
          };
        });
      },
      clearArtifactRefs: (conversationId: string) => {
        set((state) => {
          const refs = { ...state.artifactRefs };
          delete refs[conversationId];
          return { artifactRefs: refs };
        });
      },
      getArtifactRefs: (conversationId: string) => {
        return get().artifactRefs[conversationId] ?? [];
      },
    }),
    {
      name: "chat-input-contents",
      partialize: (state) => ({ inputContents: state.inputContents }),
    },
  ),
);
