import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ChatMention } from "../components/ChatInput/MentionEditor";
import type { FeaturedSelection } from "./featuredSelection";
import type { ComposerFileCandidate } from '../components/ChatInput/composerReferences';

/** Draft bucket used by the composer before a conversation has been created. */
export const NEW_CHAT_INPUT_DRAFT_KEY = "__new_chat__";

export interface ChatModeDraft {
  text: string;
  mentions: ChatMention[];
  files: ComposerFileCandidate[];
  featuredSelection?: FeaturedSelection;
}

export interface ArtifactRef {
  slot: string;
  slot_id: string;
  sort_order?: number;
  content_type: string;
  /** Short text preview or URL for display. */
  preview?: string;
}

export interface WorkflowRepairDraft extends ChatModeDraft {
  runtimeSettings?: import('../utils/request').ConversationRuntimeSettings;
  runInBackground: boolean;
  workspaceId?: string;
  workspacePermissionMode?: import('../utils/localWorkspace').WorkspacePermissionMode;
}

interface ChatInputStore {
  workflowRepairDrafts: Record<string, WorkflowRepairDraft>;
  saveWorkflowRepairDraft: (key: string, draft: WorkflowRepairDraft) => void;
  updateWorkflowRepairFiles: (key: string, files: ComposerFileCandidate[]) => void;
  modeDraft?: ChatModeDraft;
  setModeDraft: (draft?: ChatModeDraft) => void;
  inputContents: Record<string, string>;
  inputMentions: Record<string, ChatMention[]>;
  /** Pending artifact references to inject into the next message. Keyed by conversationId. */
  artifactRefs: Record<string, ArtifactRef[]>;
  saveInputContent: (conversationId: string, content: string, mentions?: ChatMention[]) => void;
  getInputMentions: (conversationId: string) => ChatMention[];
  getInputContent: (conversationId: string) => string;
  clearInputContent: (conversationId: string) => void;
  clearAllInputContents: () => void;
  addArtifactRef: (conversationId: string, ref: ArtifactRef) => void;
  removeArtifactRef: (conversationId: string, slot: string, sortOrder?: number) => void;
  clearArtifactRefs: (conversationId: string) => void;
  getArtifactRefs: (conversationId: string) => ArtifactRef[];
}

export const useChatInputStore = create<ChatInputStore>()(
  persist(
    (set, get) => ({
      workflowRepairDrafts: {},
      saveWorkflowRepairDraft: (key, draft) => set(state => ({
        workflowRepairDrafts: { ...state.workflowRepairDrafts, [key]: draft },
        inputContents: { ...state.inputContents, [key]: draft.text },
        inputMentions: { ...state.inputMentions, [key]: draft.mentions },
      })),
      updateWorkflowRepairFiles: (key, files) => set(state => {
        const draft = state.workflowRepairDrafts[key];
        return draft ? { workflowRepairDrafts: { ...state.workflowRepairDrafts, [key]: { ...draft, files } } } : {};
      }),
      setModeDraft: modeDraft => set({ modeDraft }),
      inputContents: {},
      inputMentions: {},
      artifactRefs: {},
      saveInputContent: (conversationId: string, content: string, mentions?: ChatMention[]) => {
        set((state) => ({
          workflowRepairDrafts: state.workflowRepairDrafts[conversationId] ? {
            ...state.workflowRepairDrafts,
            [conversationId]: { ...state.workflowRepairDrafts[conversationId], text: content,
              mentions: mentions ?? state.workflowRepairDrafts[conversationId].mentions },
          } : state.workflowRepairDrafts,
          inputContents: {
            ...state.inputContents,
            [conversationId]: content,
          },
          inputMentions: {
            ...state.inputMentions,
            [conversationId]: mentions ?? (state.inputContents[conversationId] === content
              ? state.inputMentions[conversationId] || [] : []),
          },
        }));
      },
      getInputMentions: (conversationId) => get().inputMentions[conversationId] || [],
      getInputContent: (conversationId: string) => {
        return get().inputContents[conversationId] || "";
      },
      clearInputContent: (conversationId: string) => {
        set((state) => {
          const newContents = { ...state.inputContents };
          const inputMentions = { ...state.inputMentions };
          delete newContents[conversationId];
          delete inputMentions[conversationId];
          const workflowRepairDrafts = { ...state.workflowRepairDrafts };
          delete workflowRepairDrafts[conversationId];
          return { inputContents: newContents, inputMentions, workflowRepairDrafts };
        });
      },
      clearAllInputContents: () => {
        set({ inputContents: {}, inputMentions: {}, workflowRepairDrafts: {}, modeDraft: undefined });
      },
      addArtifactRef: (conversationId: string, ref: ArtifactRef) => {
        set((state) => {
          const existing = state.artifactRefs[conversationId] ?? [];
          const filtered = existing.filter(
            (r) => !(r.slot === ref.slot && r.sort_order === ref.sort_order),
          );
          return {
            artifactRefs: {
              ...state.artifactRefs,
              [conversationId]: [...filtered, ref],
            },
          };
        });
      },
      removeArtifactRef: (conversationId: string, slot: string, sortOrder?: number) => {
        set((state) => {
          const existing = state.artifactRefs[conversationId] ?? [];
          return {
            artifactRefs: {
              ...state.artifactRefs,
              [conversationId]: existing.filter(
                (r) => !(r.slot === slot && r.sort_order === sortOrder),
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
      partialize: (state) => ({ inputContents: state.inputContents, inputMentions: state.inputMentions, workflowRepairDrafts: state.workflowRepairDrafts }),
    },
  ),
);
