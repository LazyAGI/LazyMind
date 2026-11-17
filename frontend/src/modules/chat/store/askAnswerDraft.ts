import type { AnswerState } from "../components/AskCard";

interface AskDraft {
  answers?: Record<number, AnswerState>;
  questionIndex?: number;
}

const STORAGE_PREFIX = "askDraft:";

// Edits stay in memory while typing. Persist only on page exit, synchronously,
// so a reload cannot discard edits still waiting for the backend debounce.
export class AskDraftStore {
  private drafts = new Map<string, AskDraft>();
  private dirty = new Set<string>();

  private read(key: string): AskDraft {
    const cached = this.drafts.get(key);
    if (cached) return cached;
    let draft: AskDraft = {};
    try {
      const raw = sessionStorage.getItem(STORAGE_PREFIX + key);
      if (raw) {
        const stored = JSON.parse(raw);
        if (stored && typeof stored === "object" && !Array.isArray(stored)) {
          draft = stored;
        }
      }
    } catch (error) {
      console.warn("[AskDraft] Unable to restore browser draft", error);
    }
    this.drafts.set(key, draft);
    return draft;
  }

  getAnswers(key: string) { return this.read(key).answers; }

  setAnswers(key: string, answers: Record<number, AnswerState>) {
    this.drafts.set(key, { ...this.read(key), answers });
    this.dirty.add(key);
  }

  getQuestionIndex(key: string) { return this.read(key).questionIndex; }

  setQuestionIndex(key: string, questionIndex: number) {
    const draft = this.read(key);
    if (draft.questionIndex === questionIndex) return;
    this.drafts.set(key, { ...draft, questionIndex });
    this.dirty.add(key);
  }

  clearAnswers(key: string) {
    const { answers, ...position } = this.read(key);
    if (!answers) return;
    this.drafts.set(key, position);
    this.dirty.add(key);
    // Do not let a previously persisted draft reappear after submission.
    this.persist(key);
  }

  private persist(key: string) {
    try {
      sessionStorage.setItem(STORAGE_PREFIX + key, JSON.stringify(this.drafts.get(key)));
      this.dirty.delete(key);
    } catch (error) {
      // Storage failure must not break the editor. The in-memory draft and
      // ordinary backend autosave remain available; report the failure for diagnosis.
      console.warn("[AskDraft] Unable to persist browser draft", error);
    }
  }

  flush() {
    for (const key of this.dirty) this.persist(key);
  }

  clear() {
    this.drafts.clear();
    this.dirty.clear();
  }
}

export const askDraftStore = new AskDraftStore();
if (typeof window !== "undefined") {
  window.addEventListener("pagehide", () => askDraftStore.flush());
}
