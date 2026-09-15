import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import VocabularyPage from "./VocabularyPage";
import { getActiveVocabularyReviewSession, startVocabularyReviewSession, submitVocabularySessionReview } from "./api";

vi.mock("@/runtime/mode", () => ({ isVocabularyEnabled: () => true }));
vi.mock("./api", () => ({
  listVocabulary: vi.fn(async () => [{ word: { id: "w1", term: "evidence", meaning: "证据", part_of_speech: "noun", provider: "local" }, state: "learning", tags: [], wordbooks: [], reps: 2, lapses: 1 }]),
  getAnkiStatus: vi.fn(async () => ({ connected: true, review_capability: "full" })),
  getVocabularyProvider: vi.fn(async () => ({ selected_provider: "local", anki_endpoint: "http://127.0.0.1:8765", anki_deck_name: "LazyMind Vocabulary" })),
  getActiveVocabularyReviewSession: vi.fn(async () => ({ active: false })),
  listAnkiDecks: vi.fn(async () => [{id:1,name:"Default",is_default:true},{id:2,name:"LazyMind Vocabulary",is_default:false}]),
  listReviewLogs: vi.fn(async () => []),
  createAnkiDeck: vi.fn(async () => undefined),
  addVocabularyWord: vi.fn(async () => ({})),
  syncAnki: vi.fn(async () => undefined),
  saveVocabularyProvider: vi.fn(async (setting) => setting),
  getVocabularyStats: vi.fn(async () => ({ total: 1, due: 1, new: 0, learning: 1, review: 0, relearning: 0, mastered: 0, weak: 0, reviewed_today: 0 })),
  listWordbooks: vi.fn(async () => [{ id: "default", name: "默认生词本" }, { id: "reading", name: "阅读" }]),
  deleteAnkiDeck: vi.fn(async () => undefined),
  startVocabularyReviewSession: vi.fn(async () => ({ session: { id: "s1" }, questions: [{ card_id: "c1", row_version: 1, previewed_at: new Date().toISOString(), remaining: 1, prompt: "evidence", answer: "证据", options: {}, word: { id: "w1", term: "evidence" }, state: "learning", tags: [], wordbooks: [], reps: 0, lapses: 0 }] })),
  submitVocabularySessionReview: vi.fn(async () => undefined),
  completeVocabularyReviewSession: vi.fn(async () => ({ session: { id: "s1" }, total: 1, correct: 1, incorrect: 0, accuracy: 1, rating_counts: { again: 0, hard: 0, good: 1, easy: 0 }, average_interval_before_days: 1, average_interval_after_days: 3, difficult_words: [], answers: [] })),
  masterVocabulary: vi.fn(), resumeVocabulary: vi.fn(), resetVocabulary: vi.fn(), resetVocabularyBatch: vi.fn(async () => ({ reset: 1 })), deleteVocabulary: vi.fn(), createWordbook: vi.fn(), deleteWordbook: vi.fn(), updateVocabularyWord: vi.fn(), reviewLogsExportUrl: () => "/logs",
}));

describe("VocabularyPage", () => {
  beforeEach(() => vi.clearAllMocks());
  it("shows words with answers and memory state", async () => {
    render(<VocabularyPage />);
    expect(await screen.findByText("evidence")).toBeTruthy();
    expect(screen.getByText("证据")).toBeTruthy();
    expect(screen.getByText("2 次 · 错 1")).toBeTruthy();
    expect(screen.getAllByText("默认生词本").length).toBeGreaterThan(0);
    expect(screen.queryByText("单词本管理")).toBeNull();
  });
  it("creates a review question and reveals its answer", async () => {
    vi.mocked(startVocabularyReviewSession).mockResolvedValueOnce({ session: { id: "s1" }, questions: [{ card_id: "c1", row_version: 1, previewed_at: new Date().toISOString(), remaining: 1, prompt: "evidence", answer: "证据", options: {}, word: { id: "w1", term: "evidence" }, state: "learning", tags: [], wordbooks: [], reps: 0, lapses: 0 }] }).mockResolvedValueOnce({session:{id:"s1"},questions:[]});
    render(<VocabularyPage />);
    fireEvent.click(await screen.findByRole("button", { name: "开始复习" }));
    expect(await screen.findByRole("heading", { name: "evidence" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "显示答案" }));
    await waitFor(() => expect(screen.getAllByText("证据").length).toBeGreaterThan(0));
    fireEvent.click(screen.getByRole("button", { name: /记得/ }));
    expect(await screen.findByText("本轮复习完成")).toBeTruthy();
    expect(screen.getByText("完成题目")).toBeTruthy();
  });
  it("separates learning history and backend settings", async () => {
    render(<VocabularyPage />);
    fireEvent.click(await screen.findByRole("tab", { name: "学习记录" }));
    expect(await screen.findByText("作答次数")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /设置/ }));
    expect(await screen.findByText("存储与学习方式")).toBeTruthy();
    expect(screen.getByText("LazyMind 本地")).toBeTruthy();
    expect(screen.getByText("Anki")).toBeTruthy();
    expect(screen.queryByText("当前单词本")).toBeNull();
  });
  it("submits a multiple-choice response for server-side grading", async () => {
    vi.mocked(startVocabularyReviewSession).mockResolvedValueOnce({session:{id:"objective-session"},questions:[{card_id:"c2",row_version:1,previewed_at:new Date().toISOString(),remaining:1,prompt:"context",answer:"",interaction:"multiple_choice",choices:[{value:"语境",label:"语境",part_of_speech:"n."},{value:"奔跑",label:"奔跑",part_of_speech:"v."}],options:{},word:{id:"w2",term:"context",meaning:"语境",part_of_speech:"n.",provider:"local"},state:"new",tags:[],wordbooks:[],reps:0,lapses:0}]}).mockResolvedValueOnce({session:{id:"objective-session"},questions:[]});
    render(<VocabularyPage/>);fireEvent.click(await screen.findByRole("button",{name:"开始复习"}));fireEvent.click(await screen.findByRole("button",{name:/语境/}));
    await waitFor(()=>expect(submitVocabularySessionReview).toHaveBeenCalledWith("objective-session",expect.objectContaining({response:"语境",rating:undefined})));
  });
  it("shows continue review when chat already owns the active session", async () => {
    vi.mocked(getActiveVocabularyReviewSession).mockResolvedValueOnce({active:true,session:{id:"shared",provider:"local",wordbook_id:"default",wordbook_name:"默认生词本",status:"active",expires_at:new Date(Date.now()+3600000).toISOString()}});
    render(<VocabularyPage/>);
    expect(await screen.findByRole("button",{name:"继续复习"})).toBeTruthy();
  });
});
