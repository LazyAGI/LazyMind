import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { Modal } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DocumentVocabularyPanel from "./DocumentVocabularyPanel";
import { deleteDocumentVocabulary, listDocumentVocabulary, removeDocumentVocabulary, type DocumentVocabularyItem } from "./api";

vi.mock("./api", () => ({ listDocumentVocabulary: vi.fn(), removeDocumentVocabulary: vi.fn(), deleteDocumentVocabulary: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, options?: { page?: number; location?: string }) => options?.location || (options?.page ? `Page ${options.page}` : key) }) }));
const word = (extra: Partial<DocumentVocabularyItem> = {}): DocumentVocabularyItem => ({
  id: "word-1", term: "workload", phonetic: "/ˈwɜːkləʊd/", part_of_speech: "n.", meaning: "工作负载", definition: "", language: "en", user_note: "", provider: "local", origin_type: "document", source_name: "", source_version: "", license_id: "",
  example: { sentence: "A varied workload.", translation: "多样的工作负载。" }, source: { page: 7 }, source_count: 1, can_delete: true, ...extra,
});
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done; }); return { promise, resolve }; }
beforeEach(() => { vi.resetAllMocks(); vi.mocked(listDocumentVocabulary).mockResolvedValue([word()]); });

describe("DocumentVocabularyPanel", () => {
  it("renders readable word cards including phonetics, meaning and bilingual source", async () => {
    render(<DocumentVocabularyPanel documentId="doc-1" />);
    const card = await screen.findByRole("article");
    for (const text of ["workload", "/ˈwɜːkləʊd/", "工作负载", "A varied workload.", "多样的工作负载。"])
      expect(within(card).getByText(text)).toBeInTheDocument();
    expect(within(card).getByText("Page 7")).toBeInTheDocument();
    expect(card.querySelector("details")).not.toHaveAttribute("open");
    expect(within(card).getByText("A varied workload.")).not.toBeVisible();
    fireEvent.click(within(card).getByLabelText("vocabulary.document.exampleDetails"));
    expect(card.querySelector("details")).toHaveAttribute("open");
    expect(within(card).getByText("A varied workload.")).toBeVisible();
  });
  it("uses document context without attaching an unrelated example translation", async () => {
    vi.mocked(listDocumentVocabulary).mockResolvedValue([word({ source: { page: 2, context_sentence: "This paper studies workload diversity." } })]);
    render(<DocumentVocabularyPanel documentId="doc-1" />);
    expect(await screen.findByText("This paper studies workload diversity.")).toBeInTheDocument();
    expect(screen.queryByText("A varied workload.")).toBeNull();
    expect(screen.queryByText("多样的工作负载。")).toBeNull();
  });
  it("keeps removal and whole-word deletion separately confirmed and permission-aware", async () => {
    vi.mocked(listDocumentVocabulary).mockResolvedValue([word(), word({ id: "shared", term: "shared", can_delete: false })]);
    const confirm = vi.spyOn(Modal, "confirm").mockReturnValue({ destroy: vi.fn(), update: vi.fn() });
    render(<DocumentVocabularyPanel documentId="doc-1" />);
    const cards = await screen.findAllByRole("article");
    expect(within(cards[1]).queryByRole("button", { name: "vocabulary.document.deleteWord" })).toBeNull();
    fireEvent.click(within(cards[0]).getByRole("button", { name: "vocabulary.document.moreActions" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "vocabulary.document.removeSource" }));
    expect(removeDocumentVocabulary).not.toHaveBeenCalled();
    await act(async () => { await confirm.mock.calls[0][0].onOk?.(); });
    expect(removeDocumentVocabulary).toHaveBeenCalledWith("doc-1", "word-1");
    const card = await screen.findByText("workload");
    fireEvent.click(within(card.closest("article")!).getByRole("button", { name: "vocabulary.document.moreActions" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "vocabulary.document.deleteWord" }));
    expect(deleteDocumentVocabulary).not.toHaveBeenCalled();
    await act(async () => { await confirm.mock.calls[1][0].onOk?.(); });
    expect(deleteDocumentVocabulary).toHaveBeenCalledWith("doc-1", "word-1");
    confirm.mockRestore();
  });
  it("does not offer whole-word deletion for a shared word", async () => {
    vi.mocked(listDocumentVocabulary).mockResolvedValue([word({ can_delete: false })]);
    render(<DocumentVocabularyPanel documentId="doc-1" />);
    fireEvent.click(await screen.findByRole("button", { name: "vocabulary.document.moreActions" }));
    expect(await screen.findByRole("menuitem", { name: "vocabulary.document.removeSource" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "vocabulary.document.deleteWord" })).toBeNull();
  });
  it("shows load failure with retry instead of an empty list", async () => {
    vi.mocked(listDocumentVocabulary).mockRejectedValueOnce(new Error("offline"));
    render(<DocumentVocabularyPanel documentId="doc-1" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("vocabulary.document.loadFailed");
    expect(screen.queryByText("vocabulary.document.empty")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "vocabulary.document.retry" }));
    expect(await screen.findByText("workload")).toBeInTheDocument();
  });
  it("does not show results from an old document after a newer request completes", async () => {
    const old = deferred<DocumentVocabularyItem[]>();
    vi.mocked(listDocumentVocabulary).mockReturnValueOnce(old.promise).mockResolvedValueOnce([word({ term: "new-document" })]);
    const view = render(<DocumentVocabularyPanel documentId="old" />);
    view.rerender(<DocumentVocabularyPanel documentId="new" />);
    expect(await screen.findByText("new-document")).toBeInTheDocument();
    await act(async () => { old.resolve([word({ term: "old-document" })]); });
    expect(screen.queryByText("old-document")).toBeNull();
    expect(screen.getByText("new-document")).toBeInTheDocument();
  });
  it("clears the previous document while loading and shows a genuine empty state", async () => {
    const next = deferred<DocumentVocabularyItem[]>();
    const view = render(<DocumentVocabularyPanel documentId="old" />);
    await screen.findByText("workload");
    vi.mocked(listDocumentVocabulary).mockReturnValueOnce(next.promise);
    view.rerender(<DocumentVocabularyPanel documentId="new" />);
    expect(screen.queryByText("workload")).toBeNull();
    expect(screen.getByRole("status")).toBeInTheDocument();
    await act(async () => { next.resolve([]); });
    expect(screen.getByText("vocabulary.document.empty")).toBeInTheDocument();
  });
});
