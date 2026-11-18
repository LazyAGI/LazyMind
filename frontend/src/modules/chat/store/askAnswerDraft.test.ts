import { beforeEach, describe, expect, it, vi } from "vitest";
import { AskDraftStore, askDraftStore } from "./askAnswerDraft";

beforeEach(() => { sessionStorage.clear(); askDraftStore.clear(); });
const answers = { 0: { type: "text" as const, value: "最新答案" } };

describe("ask draft page lifecycle", () => {
  it("saves the latest answer and position on pagehide without writing per keystroke", () => {
    const write = vi.spyOn(sessionStorage, "setItem");
    try {
      askDraftStore.setAnswers("a", { 0: { type: "text", value: "最" } });
      askDraftStore.setQuestionIndex("a", 2);
      askDraftStore.setAnswers("a", answers);
      askDraftStore.setAnswers("b", { 0: { type: "text", value: "B" } });
      askDraftStore.setQuestionIndex("b", 1);
      expect(write).not.toHaveBeenCalled();
      window.dispatchEvent(new Event("pagehide"));
      const restored = new AskDraftStore();
      expect(restored.getAnswers("a")).toEqual(answers);
      expect(restored.getQuestionIndex("a")).toBe(2);
      expect(restored.getAnswers("b")).toEqual({ 0: { type: "text", value: "B" } });
      expect(restored.getQuestionIndex("b")).toBe(1);
      expect(restored.getAnswers("new-card")).toBeUndefined();
      expect(write).toHaveBeenCalledTimes(2);
      window.dispatchEvent(new Event("pagehide"));
      expect(write).toHaveBeenCalledTimes(2);
    } finally { write.mockRestore(); }
  });

  it("removes submitted answers from the browser snapshot while retaining the question position", () => {
    askDraftStore.setAnswers("submitted", answers);
    askDraftStore.setQuestionIndex("submitted", 2);
    askDraftStore.flush();
    askDraftStore.clearAnswers("submitted");
    const restored = new AskDraftStore();
    expect(restored.getAnswers("submitted")).toBeUndefined();
    expect(restored.getQuestionIndex("submitted")).toBe(2);
  });

  it.each(["getItem", "setItem"] as const)("keeps drafts usable when storage.%s fails", method => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const storage = vi.spyOn(sessionStorage, method).mockImplementation(() => { throw new DOMException("Unavailable", "SecurityError"); });
    try {
      expect(() => {
        askDraftStore.setAnswers("a", answers);
        askDraftStore.setQuestionIndex("a", 2);
        askDraftStore.flush();
      }).not.toThrow();
      expect(askDraftStore.getAnswers("a")).toEqual(answers);
      expect(askDraftStore.getQuestionIndex("a")).toBe(2);
      expect(warn).toHaveBeenCalled();
    } finally { storage.mockRestore(); warn.mockRestore(); }
    askDraftStore.flush();
    const restored = new AskDraftStore();
    expect(restored.getAnswers("a")).toEqual(answers);
  });

  it("ignores invalid JSON without breaking editing", () => {
    sessionStorage.setItem("askDraft:broken", "{");
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    try {
      expect(askDraftStore.getAnswers("broken")).toBeUndefined();
      askDraftStore.setAnswers("broken", answers);
      askDraftStore.flush();
      expect(new AskDraftStore().getAnswers("broken")).toEqual(answers);
    } finally { warn.mockRestore(); }
  });
});
