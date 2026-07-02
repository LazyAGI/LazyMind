const DRAFT_LS_PREFIX = 'userMsgEditDraft:';

export interface UserMessageEditDraft {
  text: string;
  cites: string[];
}

export const userMessageEditDraftStore = {
  getDraft(conversationId: string): UserMessageEditDraft | null {
    if (!conversationId || conversationId.startsWith('temp_')) {
      return null;
    }
    try {
      const raw = localStorage.getItem(DRAFT_LS_PREFIX + conversationId);
      if (!raw) {
        return null;
      }
      const parsed = JSON.parse(raw) as Partial<UserMessageEditDraft>;
      if (typeof parsed.text !== 'string') {
        return null;
      }
      return {
        text: parsed.text,
        cites: Array.isArray(parsed.cites)
          ? parsed.cites.filter((item): item is string => typeof item === 'string')
          : [],
      };
    } catch {
      return null;
    }
  },

  setDraft(conversationId: string, draft: UserMessageEditDraft): void {
    if (!conversationId || conversationId.startsWith('temp_')) {
      return;
    }
    try {
      localStorage.setItem(DRAFT_LS_PREFIX + conversationId, JSON.stringify(draft));
    } catch {
      /* storage full — ignore */
    }
  },

  clearDraft(conversationId: string): void {
    if (!conversationId) {
      return;
    }
    try {
      localStorage.removeItem(DRAFT_LS_PREFIX + conversationId);
    } catch {
      /* ignore */
    }
  },
};
