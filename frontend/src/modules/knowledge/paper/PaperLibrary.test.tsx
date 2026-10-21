import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider, Modal, message } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import PaperLibrary from "./PaperLibrary";
import { migratePaperLibrary, type PaperLibraryDataV3 } from "./libraryModel";
import type { PaperItem } from "./model";
import { loadPreviewPaperLibrary, savePreviewPaperLibrary, loadPersistedPaperLibrary, savePersistedPaperLibrary } from "./store";
import { paperFromPdf } from "./pdfMeta";
import { lookupIdentifier } from "./scholar";
import { importArxivPaper, listImportedArxivPapers, type ArxivImportResult } from "./arxivImport";
import * as paperModel from "./model";

vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get: vi.fn(), post: vi.fn() } }));
vi.mock("./store", async (importOriginal) => ({
  ...await importOriginal<typeof import("./store")>(),
  loadPersistedPaperLibrary: vi.fn(),
  savePersistedPaperLibrary: vi.fn(),
}));
vi.mock("./arxivImport", async (importOriginal) => ({
  ...await importOriginal<typeof import("./arxivImport")>(),
  importArxivPaper: vi.fn(),
  listImportedArxivPapers: vi.fn(),
}));
vi.mock("./pdfMeta", () => ({ paperFromPdf: vi.fn() }));
vi.mock("./scholar", () => ({
  lookupIdentifier: vi.fn(),
}));
vi.mock("./PaperDetailDrawer", () => ({ default: ({ paper }: { paper: PaperItem }) => <div role="dialog" aria-label="文献详情">{paper.title}</div> }));

const datasetId = "paper-navigation-test";
const makePaper = (id: string, title: string, folderId: string | null, important = false): PaperItem => ({
  id, title, folderId, important, authors: "Test Author", venue: "Research Journal",
  year: 2026, status: "unread", tags: [], source: "local", parse: "ready",
});

function seedLibrary() {
  const data = migratePaperLibrary({
    folders: [{ id: "first", name: "第一个文件夹" }, { id: "second", name: "第二个文件夹" }],
    papers: [
      makePaper("a", "Folder paper", "first", true),
      makePaper("b", "Other favorite", "second", true),
      makePaper("c", "Unfiled paper", null),
    ],
  });
  data.notes.push({ id: "note-a", paperId: "a", title: "Reading notes", content: "Keep these notes", kind: "note", createdAt: "2026-10-21", updatedAt: "2026-10-21" });
  data.papers[0].noteIds.push("note-a");
  data.papers[0].metadata.abstract = "Preserve the original abstract.";
  data.papers[0].metadata.confidence.title = 0.98;
  data.papers[0].annotationIds.push("annotation-a");
  data.annotations.push({ id: "annotation-a", paperId: "a", comment: "Keep this annotation", color: "yellow", createdAt: "2026-10-21" });
  data.attachments[0].contentRef = "stored-pdf-a";
  savePreviewPaperLibrary(datasetId, data);
  return data;
}

function browse() {
  return within(screen.getByRole("navigation", { name: "文献浏览" }));
}

async function renderLibrary(props: Partial<React.ComponentProps<typeof PaperLibrary>> = {}) {
  let result!: ReturnType<typeof render>;
  await act(async () => { result = render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <PaperLibrary datasetId={datasetId} {...props} />
    </ConfigProvider>,
  ); });
  return result;
}

async function openImportDialog(method: string, title: string) {
  fireEvent.click(browse().getByRole("button", { name: /导入/ }));
  fireEvent.click(await screen.findByRole("menuitem", { name: new RegExp(method) }));
  return screen.findByRole("dialog", { name: title });
}

function captureDownload() {
  const browserUrl = URL;
  const createObjectURL = vi.fn<(object: Blob | MediaSource) => string>(() => "blob:paper-export");
  vi.stubGlobal("URL", class extends browserUrl {
    static createObjectURL = createObjectURL;
    static revokeObjectURL = vi.fn();
  });
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  return async () => {
    expect(createObjectURL).toHaveBeenCalledOnce();
    let content = "";
    await act(async () => {
      content = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result));
        reader.onerror = () => reject(reader.error);
        reader.readAsText(createObjectURL.mock.calls[0][0] as Blob);
      });
    });
    return content;
  };
}

function libraryTools() {
  return within(screen.getByRole("toolbar", { name: "文献工具" }));
}

function dragTransfer() {
  const data = new Map<string, string>();
  return {
    get types() { return Array.from(data.keys()); },
    files: [],
    effectAllowed: "uninitialized",
    dropEffect: "none",
    setData: (type: string, value: string) => data.set(type, value),
    getData: (type: string) => data.get(type) || "",
    clearData: (type?: string) => type ? data.delete(type) : data.clear(),
    setDragImage: vi.fn(),
  };
}

async function dragPaperToFolder(title: string, folder: string, transfer = dragTransfer()) {
  const handle = screen.getByRole("button", { name: `拖动 ${title}` });
  await act(async () => {
  fireEvent.dragStart(handle, { dataTransfer: transfer });
  const target = browse().getByRole("button", { name: new RegExp(folder) });
  fireEvent.dragOver(target, { dataTransfer: transfer });
  fireEvent.drop(target, { dataTransfer: transfer });
  fireEvent.dragEnd(handle, { dataTransfer: transfer });
  });
}

async function closePopover(name: string) {
  fireEvent.mouseDown(document.body);
  fireEvent.click(document.body);
  await waitFor(() => expect(screen.queryByRole("dialog", { name })).not.toBeInTheDocument());
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
  vi.mocked(importArxivPaper).mockReset();
  vi.mocked(listImportedArxivPapers).mockReset().mockResolvedValue([]);
  ConfigProvider.config({ holderRender: (children: React.ReactNode) => <ConfigProvider theme={{ token: { motion: false } }}>{children}</ConfigProvider> });
  seedLibrary();
  vi.mocked(loadPersistedPaperLibrary).mockReset().mockImplementation(async (id) => ({ data: loadPreviewPaperLibrary(id), revision: 0, canWrite: true }));
  vi.mocked(savePersistedPaperLibrary).mockReset().mockImplementation(async (id, data, revision) => {
    savePreviewPaperLibrary(id, data);
    return { data, revision: revision + 1, canWrite: true };
  });
});

afterEach(async () => {
  cleanup();
  await act(async () => {
    Modal.destroyAll();
    message.destroy();
  });
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("paper library navigation and import", () => {
  it("opens the primary readable PDF in the current tab from the paper title", async () => {
    const data = loadPreviewPaperLibrary(datasetId);
    const primary = { ...data.attachments[0], id: "primary-pdf", contentRef: "primary-doc", isPrimary: true };
    data.attachments[0].isPrimary = false;
    data.attachments.push(primary);
    data.papers[0].attachmentIds.push(primary.id);
    savePreviewPaperLibrary(datasetId, data);
    await renderLibrary();
    const link = screen.getByRole("link", { name: "Folder paper" });
    expect(link).toHaveAttribute("href", `/lib/knowledge/knowledge/${datasetId}/primary-doc`);
    expect(link).not.toHaveAttribute("target");
    expect(screen.queryByRole("dialog", { name: "文献详情" })).not.toBeInTheDocument();
  });

  it("keeps metadata accessible separately and uses it when there is no readable attachment", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    await renderLibrary();
    fireEvent.click(screen.getByRole("button", { name: "Folder paper 的文献详情" }));
    expect(screen.getByRole("dialog", { name: "文献详情" })).toHaveTextContent("Folder paper");
    expect(open).not.toHaveBeenCalled();
  });

  it("opens metadata for papers without a readable PDF", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    await renderLibrary();
    fireEvent.click(screen.getByRole("link", { name: "Unfiled paper" }));
    expect(screen.getByRole("dialog", { name: "文献详情" })).toHaveTextContent("Unfiled paper");
    expect(open).not.toHaveBeenCalled();
  });

  it("places the star beside the title and updates favorites without opening the reader", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    await renderLibrary();
    const title = screen.getByRole("heading", { name: "Folder paper" });
    await act(async () => fireEvent.click(within(title.parentElement!).getByRole("button", { name: "取消收藏" })));
    expect(loadPreviewPaperLibrary(datasetId).papers.find((paper) => paper.id === "a")?.important).toBe(false);
    expect(open).not.toHaveBeenCalled();
  });

  it("places selection actions above the table and can clear the selection", async () => {
    await renderLibrary();
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Folder paper" }));
    const actions = screen.getByRole("region", { name: "已选论文" });
    expect(actions.compareDocumentPosition(screen.getByRole("table", { name: "论文列表" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(within(actions).getByRole("button", { name: "取消选择" }));
    expect(screen.queryByRole("region", { name: "已选论文" })).not.toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "选择 Folder paper" })).not.toBeChecked();
  });

  it("provides a back action and shows favorites across folders", async () => {
    const onBack = vi.fn();
    await renderLibrary({ title: "我的论文库", onBack });
    fireEvent.click(screen.getByRole("button", { name: "返回知识库" }));
    expect(onBack).toHaveBeenCalledOnce();

    fireEvent.click(browse().getByRole("button", { name: /第一个文件夹/ }));
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Other favorite" })).not.toBeInTheDocument();
    fireEvent.click(browse().getByRole("button", { name: /收藏/ }));
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Other favorite" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Unfiled paper" })).not.toBeInTheDocument();
    expect(browse().getByRole("button", { name: "删除文件夹" })).toBeDisabled();
  });

  it.each(["bib", "ris", "csv"] as const)("exports only selected papers as %s from the bulk menu", async (format) => {
    const readDownload = captureDownload();
    await renderLibrary();
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Folder paper" }));
    const bulkActions = within(screen.getByRole("region", { name: "已选论文" }));
    fireEvent.mouseEnter(bulkActions.getByRole("button", { name: /更\s*多/ }));
    fireEvent.mouseEnter(await screen.findByRole("menuitem", { name: /导出已选/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: format === "bib" ? "BibTeX" : format.toUpperCase() }));

    const content = await readDownload();
    expect(content).toContain("Folder paper");
    expect(content).not.toContain("Other favorite");
    expect(content).not.toContain("Unfiled paper");
    if (format === "csv") expect(content).toContain("第一个文件夹");
  });

  it("keeps the document toolbar limited to search, filter and export", async () => {
    await renderLibrary();
    const tools = libraryTools();
    expect(tools.getAllByRole("button")).toHaveLength(3);
    for (const name of ["搜索", "筛选", "导出"]) expect(tools.getByRole("button", { name })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "搜索文献" })).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText("搜索题名、作者、DOI 或 arXiv")).not.toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "阅读状态" })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "排序" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /添加筛选|添加条件|加上这条/ })).not.toBeInTheDocument();
  });

  it("replaces the search button inline and keeps the query active after it closes", async () => {
    await renderLibrary();
    fireEvent.click(libraryTools().getByRole("button", { name: "搜索" }));
    const dialog = await screen.findByRole("dialog", { name: "搜索文献" });
    expect(libraryTools().queryByRole("button", { name: "搜索" })).not.toBeInTheDocument();
    expect(screen.getByRole("toolbar", { name: "文献工具" })).toContainElement(dialog);
    fireEvent.change(within(dialog).getByRole("textbox", { name: "搜索文献" }), { target: { value: "Folder" } });
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Other favorite" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Unfiled paper" })).not.toBeInTheDocument();
    await closePopover("搜索文献");
    expect(screen.queryByRole("textbox", { name: "搜索文献" })).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Other favorite" })).not.toBeInTheDocument();
  });

  it("switches directly between the three tools without closing the newly opened panel", async () => {
    await renderLibrary();
    for (const [tool, panel] of [["搜索", "搜索文献"], ["筛选", "筛选文献"], ["搜索", "搜索文献"]] as const) {
      const button = libraryTools().getByRole("button", { name: tool });
      fireEvent.mouseDown(button);
      fireEvent.mouseUp(button);
      fireEvent.click(button);
      await waitFor(() => {
        expect(screen.getAllByRole("dialog")).toHaveLength(1);
        expect(screen.getByRole("dialog", { name: panel })).toBeInTheDocument();
      });
    }
    const exportButton = libraryTools().getByRole("button", { name: "导出" });
    fireEvent.mouseDown(exportButton);
    fireEvent.mouseUp(exportButton);
    fireEvent.click(exportButton);
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(screen.getAllByRole("menu")).toHaveLength(1);
      expect(screen.getByRole("menuitem", { name: "BibTeX" })).toBeInTheDocument();
    });
  });

  it("filters reading state and real PDF availability from the grouped panel", async () => {
    const data = loadPreviewPaperLibrary(datasetId);
    data.papers[1].status = "read";
    savePreviewPaperLibrary(datasetId, data);
    await renderLibrary();
    fireEvent.click(libraryTools().getByRole("button", { name: "筛选" }));
    const filter = within(await screen.findByRole("dialog", { name: "筛选文献" }));
    fireEvent.click(within(filter.getByRole("group", { name: "阅读状态" })).getByRole("button", { name: "已读" }));
    expect(screen.getByRole("heading", { name: "Other favorite" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument();
    fireEvent.click(filter.getByRole("button", { name: "清除筛选" }));
    fireEvent.click(within(filter.getByRole("group", { name: "PDF" })).getByRole("button", { name: "有 PDF" }));
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Other favorite" })).not.toBeInTheDocument();
  });

  it("clears filters without clearing the search query", async () => {
    await renderLibrary();
    fireEvent.click(libraryTools().getByRole("button", { name: "搜索" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "搜索文献" }), { target: { value: "Folder" } });
    await closePopover("搜索文献");
    fireEvent.click(libraryTools().getByRole("button", { name: "筛选" }));
    const filter = within(await screen.findByRole("dialog", { name: "筛选文献" }));
    fireEvent.click(within(filter.getByRole("group", { name: "PDF" })).getByRole("button", { name: "无 PDF" }));
    expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument();
    fireEvent.click(filter.getByRole("button", { name: "清除筛选" }));
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Other favorite" })).not.toBeInTheDocument();
  });

  it("does not filter the library when a paper tag is clicked", async () => {
    const data = loadPreviewPaperLibrary(datasetId);
    data.papers[0].tags = ["RAG"];
    savePreviewPaperLibrary(datasetId, data);
    await renderLibrary();
    fireEvent.click(screen.getByText("RAG", { exact: true }));
    for (const title of ["Folder paper", "Other favorite", "Unfiled paper"]) {
      expect(screen.getByRole("heading", { name: title })).toBeInTheDocument();
    }
    expect(screen.queryByRole("button", { name: /^移除条件/ })).not.toBeInTheDocument();
  });

  it.each([
    ["bib", false], ["ris", false], ["csv", false],
    ["bib", true], ["ris", true], ["csv", true],
  ] as const)("exports the current result or selected subset from the top menu (%s, selected=%s)", async (format, selectOnly) => {
    const readDownload = captureDownload();
    await renderLibrary();
    fireEvent.click(libraryTools().getByRole("button", { name: "搜索" }));
    const search = await screen.findByRole("dialog", { name: "搜索文献" });
    fireEvent.change(within(search).getByRole("textbox", { name: "搜索文献" }), { target: { value: "paper" } });
    await closePopover("搜索文献");
    if (selectOnly) fireEvent.click(screen.getByRole("checkbox", { name: "选择 Folder paper" }));
    fireEvent.click(libraryTools().getByRole("button", { name: "导出" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: format === "bib" ? "BibTeX" : format.toUpperCase() }));
    const content = await readDownload();
    expect(content).toContain("Folder paper");
    expect(content).not.toContain("Other favorite");
    if (selectOnly) expect(content).not.toContain("Unfiled paper");
    else expect(content).toContain("Unfiled paper");
    if (format === "csv") expect(content).toContain("第一个文件夹");
  });

  it.each([
    ["直接上传", "上传文献"],
    ["arXiv / DOI 链接", "通过链接导入"],
    ["BibTeX / RIS / CSV", "导入文献题录"],
  ])("opens a dedicated %s dialog only after selecting the import method", async (method, title) => {
    await renderLibrary();
    fireEvent.click(browse().getByRole("button", { name: /导入/ }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    fireEvent.click(await screen.findByRole("menuitem", { name: new RegExp(method) }));
    expect(await screen.findByRole("dialog", { name: title })).toBeInTheDocument();
    for (const other of ["上传文献", "通过链接导入", "导入文献题录"].filter((value) => value !== title)) {
      expect(screen.queryByRole("dialog", { name: other })).not.toBeInTheDocument();
    }
  });

  it("creates and selects a folder from the browse toolbar", async () => {
    await renderLibrary();
    expect(browse().getByRole("button", { name: "删除文件夹" })).toBeDisabled();
    fireEvent.click(browse().getByRole("button", { name: "新建文件夹" }));
    const dialog = await screen.findByRole("dialog", { name: "新建文件夹" });
    fireEvent.change(within(dialog).getByRole("textbox", { name: "文件夹名称" }), { target: { value: "新主题" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /创\s*建/ }));
    await waitFor(() => expect(browse().getByRole("button", { name: /新主题/ })).toHaveAttribute("aria-current", "true"));
    expect(loadPreviewPaperLibrary(datasetId).folders.some((folder) => folder.name === "新主题")).toBe(true);
    expect(browse().getByRole("button", { name: "删除文件夹" })).toBeEnabled();
  });

  it("deletes a selected folder while preserving its papers, notes and attachments", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    fireEvent.click(browse().getByRole("button", { name: /第一个文件夹/ }));
    fireEvent.click(browse().getByRole("button", { name: "删除文件夹" }));
    const dialog = await screen.findByRole("dialog", { name: "删除文件夹「第一个文件夹」？" });
    fireEvent.click(within(dialog).getByRole("button", { name: "删除文件夹" }));
    await waitFor(() => expect(browse().queryByRole("button", { name: /第一个文件夹/ })).not.toBeInTheDocument());
    const after = loadPreviewPaperLibrary(datasetId);
    expect(after.papers).toHaveLength(3);
    expect(after.papers.find((paper) => paper.id === "a")?.folderId).toBeNull();
    expect(after.notes).toEqual(before.notes);
    expect(after.attachments).toEqual(before.attachments);
    expect(after.annotations).toEqual(before.annotations);
    expect(after.papers.find((paper) => paper.id === "a")?.noteIds).toEqual(["note-a"]);
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(browse().getByRole("button", { name: "删除文件夹" })).toBeDisabled();
  });

  it("preserves rich paper data when adding a folder through the ordinary update path", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    fireEvent.click(browse().getByRole("button", { name: "新建文件夹" }));
    const dialog = await screen.findByRole("dialog", { name: "新建文件夹" });
    fireEvent.change(within(dialog).getByRole("textbox", { name: "文件夹名称" }), { target: { value: "文献整理" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /创\s*建/ }));
    await waitFor(() => expect(loadPreviewPaperLibrary(datasetId).folders).toHaveLength(3));
    const after = loadPreviewPaperLibrary(datasetId);
    expect(after.papers).toEqual(before.papers);
    expect(after.notes).toEqual(before.notes);
    expect(after.attachments).toEqual(before.attachments);
    expect(after.annotations).toEqual(before.annotations);
  });

  it("keeps the folder and its papers when deletion is cancelled", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    fireEvent.click(browse().getByRole("button", { name: /第一个文件夹/ }));
    fireEvent.click(browse().getByRole("button", { name: "删除文件夹" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /取\s*消/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
    expect(browse().getByRole("button", { name: /第一个文件夹/ })).toHaveAttribute("aria-current", "true");
  });

  it("waits for one direct PDF upload and records its attachment when confirmed", async () => {
    const file = new File(["%PDF-test-content"], "original-study.pdf", { type: "application/pdf" });
    const paper = makePaper("uploaded-paper", "A different original study", null);
    vi.mocked(paperFromPdf).mockResolvedValueOnce(paper);
    let finishUpload!: (documentId: string) => void;
    const onUploadPdf = vi.fn(() => new Promise<string>((resolve) => { finishUpload = resolve; }));
    await renderLibrary({ onUploadPdf });
    fireEvent.click(browse().getByRole("button", { name: /导入/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /直接上传/ }));
    const dialog = await screen.findByRole("dialog", { name: "上传文献" });
    const fileInput = dialog.querySelector<HTMLInputElement>('input[type="file"]');
    expect(fileInput).not.toBeNull();
    fireEvent.change(fileInput!, { target: { files: [file] } });

    const addButton = await within(dialog).findByRole("button", { name: "加入论文库" });
    expect(onUploadPdf).not.toHaveBeenCalled();
    fireEvent.click(addButton);
    await waitFor(() => expect(onUploadPdf).toHaveBeenCalledOnce());
    expect(paperFromPdf).toHaveBeenCalledOnce();
    expect(onUploadPdf.mock.calls[0]).toEqual([vi.mocked(paperFromPdf).mock.calls[0][0]]);
    expect(screen.getAllByRole("dialog")).toHaveLength(1);
    expect(addButton).toBeDisabled();
    expect(loadPreviewPaperLibrary(datasetId).papers.some((item) => item.id === paper.id)).toBe(false);

    await act(async () => finishUpload("document-uploaded-pdf"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const saved = loadPreviewPaperLibrary(datasetId);
    const attachment = saved.attachments.find((item) => item.paperId === paper.id);
    expect(attachment).toMatchObject({
      name: file.name, size: file.size, mimeType: "application/pdf",
      kind: "pdf", contentRef: "document-uploaded-pdf", isPrimary: true,
    });
    expect(saved.papers.find((item) => item.id === paper.id)?.attachmentIds).toEqual([attachment!.id]);
    expect(onUploadPdf).toHaveBeenCalledOnce();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("preserves notes and metadata when loading a library without attachments", async () => {
    const data = loadPreviewPaperLibrary(datasetId);
    data.attachments = [];
    data.papers.forEach((paper) => { paper.attachmentIds = []; });
    savePreviewPaperLibrary(datasetId, data);

    const loaded = loadPreviewPaperLibrary(datasetId);
    expect(loaded).toEqual(data);
    expect(loaded.notes).toHaveLength(1);
    expect(loaded.attachments).toEqual([]);
    expect(loaded.papers[0].noteIds).toEqual(["note-a"]);
    expect(loaded.papers[0].metadata.abstract).toBe("Preserve the original abstract.");
  });

  it("shows identifier failures persistently and removes the previous import candidate", async () => {
    vi.mocked(lookupIdentifier)
      .mockResolvedValueOnce(makePaper("new", "New candidate", null))
      .mockRejectedValueOnce(new Error("arXiv 服务暂时不可用"));
    await renderLibrary();
    const dialog = await openImportDialog("arXiv / DOI 链接", "通过链接导入");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "2404.06654" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    expect(await within(dialog).findByRole("button", { name: "加入论文库" })).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("arXiv 服务暂时不可用");
    await act(async () => message.destroy());
    expect(within(dialog).getByRole("alert")).toHaveTextContent("arXiv 服务暂时不可用");
    expect(within(dialog).queryByRole("button", { name: "加入论文库" })).not.toBeInTheDocument();
    expect(loadPreviewPaperLibrary(datasetId).papers).toHaveLength(3);
  });

  it("only exposes file, identifier and bibliography imports", async () => {
    await renderLibrary();
    fireEvent.click(browse().getByRole("button", { name: /导入/ }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getAllByRole("menuitem")).toHaveLength(3);
    expect(within(menu).queryByText(/Semantic Scholar|Zotero/)).not.toBeInTheDocument();
  });

  it("shows synchronous bibliography parse errors without leaving a candidate to save", async () => {
    vi.spyOn(paperModel, "parseBibTeX").mockImplementation(() => { throw new Error("BibTeX 缺少闭合括号"); });
    await renderLibrary();
    const dialog = await openImportDialog("BibTeX / RIS / CSV", "导入文献题录");
    fireEvent.change(within(dialog).getByRole("textbox", { name: "文献题录内容" }), { target: { value: "@article{broken" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "识别题录" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("BibTeX 缺少闭合括号");
    expect(within(dialog).queryByRole("button", { name: "加入论文库" })).not.toBeInTheDocument();
  });

  it.each([
    ["missing upload callback", undefined, "无法上传 PDF"],
    ["empty upload result", async (): Promise<string | void> => undefined, "未返回有效文档 ID"],
    ["failed upload", async (): Promise<string | void> => { throw new Error("PDF 上传失败：服务器断开连接"); }, "服务器断开连接"],
  ] as const)("rejects real PDF import with %s", async (_case, onUploadPdf, errorText) => {
    vi.mocked(paperFromPdf).mockResolvedValueOnce(makePaper("new", "Unsaved PDF", null));
    await renderLibrary({ onUploadPdf });
    const dialog = await openImportDialog("直接上传", "上传文献");
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["%PDF-test"], "test.pdf", { type: "application/pdf" })] } });
    fireEvent.click(await within(dialog).findByRole("button", { name: "加入论文库" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(errorText);
    expect(within(dialog).getByRole("button", { name: "加入论文库" })).toBeEnabled();
    expect(loadPreviewPaperLibrary(datasetId).papers).toHaveLength(3);
  });

  it("discards an earlier unconfirmed candidate when the next file cannot be read", async () => {
    vi.mocked(paperFromPdf)
      .mockResolvedValueOnce(makePaper("new", "Previous upload", null))
      .mockRejectedValueOnce(new Error("PDF 文件已损坏"));
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary({ onUploadPdf: async () => "uploaded-document" });
    const dialog = await openImportDialog("直接上传", "上传文献");
    const input = dialog.querySelector<HTMLInputElement>('input[type="file"]')!;
    fireEvent.change(input, { target: { files: [new File(["%PDF-first"], "first.pdf", { type: "application/pdf" })] } });
    expect(await within(dialog).findByRole("button", { name: "加入论文库" })).toBeInTheDocument();
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["broken"], "broken.pdf", { type: "application/pdf" })] } });
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("PDF 文件已损坏");
    expect(within(dialog).queryByRole("button", { name: "加入论文库" })).not.toBeInTheDocument();
    expect(within(dialog).queryByText("Previous upload")).not.toBeInTheDocument();
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
  });

  it("cancels PDF confirmation without creating an upload, document or library record", async () => {
    const upload = vi.fn().mockResolvedValue("should-not-upload");
    const before = loadPreviewPaperLibrary(datasetId);
    vi.mocked(paperFromPdf).mockResolvedValueOnce(makePaper("new-pdf", "Unconfirmed PDF", null));
    await renderLibrary({ onUploadPdf: upload });
    const dialog = await openImportDialog("直接上传", "上传文献");
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["pdf"], "cancel.pdf")] } });
    await within(dialog).findByRole("button", { name: "加入论文库" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(upload).not.toHaveBeenCalled();
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
  });

  it("reuses a confirmed upload if saving its paper snapshot fails and is retried", async () => {
    const upload = vi.fn().mockResolvedValue("one-uploaded-document");
    vi.mocked(paperFromPdf).mockResolvedValueOnce(makePaper("new-pdf", "Retry PDF", null));
    await renderLibrary({ onUploadPdf: upload });
    const dialog = await openImportDialog("直接上传", "上传文献");
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["pdf"], "retry.pdf")] } });
    const confirm = await within(dialog).findByRole("button", { name: "加入论文库" });
    vi.mocked(savePersistedPaperLibrary).mockRejectedValueOnce(new Error("数据库保存失败"));
    fireEvent.click(confirm);
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("数据库保存失败");
    expect(upload).toHaveBeenCalledOnce();
    await waitFor(() => expect(confirm).toBeEnabled());
    fireEvent.click(confirm);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(upload).toHaveBeenCalledOnce();
    expect(loadPreviewPaperLibrary(datasetId).attachments.find((item) => item.paperId === "new-pdf")?.contentRef).toBe("one-uploaded-document");
  });

  it("keeps the import dialog and original library when saving fails, then permits retry", async () => {
    vi.mocked(lookupIdentifier).mockResolvedValueOnce(makePaper("new", "Retry candidate", null));
    await renderLibrary();
    const dialog = await openImportDialog("arXiv / DOI 链接", "通过链接导入");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "2404.06654" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    const confirm = await within(dialog).findByRole("button", { name: "加入论文库" });
    vi.mocked(savePersistedPaperLibrary).mockRejectedValueOnce(new Error("后端暂时无法保存"));
    fireEvent.click(confirm);
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("后端暂时无法保存");
    expect(screen.queryByRole("heading", { name: "Retry candidate" })).not.toBeInTheDocument();
    expect(loadPreviewPaperLibrary(datasetId).papers).toHaveLength(3);
    expect(within(dialog).getByRole("button", { name: "加入论文库" })).toBeInTheDocument();
    await act(async () => fireEvent.click(confirm));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Retry candidate" })).toBeInTheDocument();
    expect(loadPreviewPaperLibrary(datasetId).papers).toHaveLength(4);
  });
});

describe("paper library table and drag to folders", () => {
  it("shows title, publication year, venue and citation count in aligned columns", async () => {
    const data = loadPreviewPaperLibrary(datasetId);
    data.papers[0].citationCount = 1250;
    data.papers[1].citationCount = 0;
    savePreviewPaperLibrary(datasetId, data);
    await renderLibrary();

    for (const name of ["标题", "年份", "刊物", "引用量"]) {
      expect(screen.getByRole("columnheader", { name })).toBeInTheDocument();
    }
    const firstRow = within(screen.getByRole("heading", { name: "Folder paper" }).closest('[role="row"], tr') as HTMLElement);
    expect(firstRow.getByRole("cell", { name: "2026" })).toBeInTheDocument();
    expect(firstRow.getByRole("cell", { name: "Research Journal" })).toBeInTheDocument();
    expect(firstRow.getByRole("cell", { name: /^(1,250|1250)$/ })).toBeInTheDocument();
    const zeroRow = within(screen.getByRole("heading", { name: "Other favorite" }).closest('[role="row"], tr') as HTMLElement);
    expect(zeroRow.getByRole("cell", { name: "0" })).toBeInTheDocument();
    const unknownRow = within(screen.getByRole("heading", { name: "Unfiled paper" }).closest('[role="row"], tr') as HTMLElement);
    expect(unknownRow.getByRole("cell", { name: "—" })).toBeInTheDocument();
  });

  it("moves a dragged paper into a folder without changing its rich content", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    const handle = screen.getByRole("button", { name: "拖动 Folder paper" });
    expect(handle).toHaveAttribute("draggable", "true");
    await dragPaperToFolder("Folder paper", "第二个文件夹");

    const saved = loadPreviewPaperLibrary(datasetId);
    expect(saved.papers.find((paper) => paper.id === "a")?.folderId).toBe("second");
    expect(saved.papers.find((paper) => paper.id === "a")).toEqual({ ...before.papers[0], folderId: "second" });
    expect(saved.notes).toEqual(before.notes);
    expect(saved.annotations).toEqual(before.annotations);
    expect(saved.attachments).toEqual(before.attachments);
    fireEvent.click(browse().getByRole("button", { name: /第二个文件夹/ }));
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    fireEvent.click(browse().getByRole("button", { name: /第一个文件夹/ }));
    expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument();
  });

  it("moves all selected papers when dragging one selected paper", async () => {
    await renderLibrary();
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Folder paper" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Unfiled paper" }));
    await dragPaperToFolder("Folder paper", "第二个文件夹");

    const saved = loadPreviewPaperLibrary(datasetId);
    expect(saved.papers.find((paper) => paper.id === "a")?.folderId).toBe("second");
    expect(saved.papers.find((paper) => paper.id === "c")?.folderId).toBe("second");
    expect(saved.papers.find((paper) => paper.id === "b")?.folderId).toBe("second");
    expect(saved.papers).toHaveLength(3);
  });

  it("moves only the dragged paper when it is outside the current selection", async () => {
    await renderLibrary();
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Unfiled paper" }));
    await dragPaperToFolder("Folder paper", "第二个文件夹");
    const saved = loadPreviewPaperLibrary(datasetId);
    expect(saved.papers.find((paper) => paper.id === "a")?.folderId).toBe("second");
    expect(saved.papers.find((paper) => paper.id === "c")?.folderId).toBeNull();
  });

  it("ignores external files and text dropped on a folder", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    const transfer = dragTransfer();
    transfer.setData("text/plain", "a");
    transfer.setData("application/json", JSON.stringify({ datasetId, ids: ["a"] }));
    const target = browse().getByRole("button", { name: /第二个文件夹/ });
    fireEvent.dragOver(target, { dataTransfer: transfer });
    fireEvent.drop(target, { dataTransfer: transfer });
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
  });

  it("rejects a valid-looking paper payload without an active internal drag", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    const transfer = dragTransfer();
    transfer.setData("application/x-lazymind-papers", JSON.stringify({ datasetId, ids: ["a"] }));
    const target = browse().getByRole("button", { name: /第二个文件夹/ });
    fireEvent.dragOver(target, { dataTransfer: transfer });
    fireEvent.drop(target, { dataTransfer: transfer });
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
  });

  it("ignores a malformed paper drag payload", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    const transfer = dragTransfer();
    const handle = screen.getByRole("button", { name: "拖动 Folder paper" });
    fireEvent.dragStart(handle, { dataTransfer: transfer });
    expect(transfer.types.length).toBeGreaterThan(0);
    for (const type of transfer.types) transfer.setData(type, "{malformed");
    const target = browse().getByRole("button", { name: /第二个文件夹/ });
    fireEvent.dragOver(target, { dataTransfer: transfer });
    fireEvent.drop(target, { dataTransfer: transfer });
    fireEvent.dragEnd(handle, { dataTransfer: transfer });
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
  });

  it("rejects papers added to an active drag payload that were not dragged", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    const transfer = dragTransfer();
    const handle = screen.getByRole("button", { name: "拖动 Folder paper" });
    fireEvent.dragStart(handle, { dataTransfer: transfer });
    transfer.setData("application/x-lazymind-papers", JSON.stringify({ datasetId, ids: ["a", "c"] }));
    const target = browse().getByRole("button", { name: /第二个文件夹/ });
    fireEvent.dragOver(target, { dataTransfer: transfer });
    fireEvent.drop(target, { dataTransfer: transfer });
    fireEvent.dragEnd(handle, { dataTransfer: transfer });
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
  });

  it("keeps a paper in its original folder and reports persistence failure", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    fireEvent.click(browse().getByRole("button", { name: /第一个文件夹/ }));
    vi.mocked(savePersistedPaperLibrary).mockRejectedValueOnce(new Error("后端暂时无法保存"));
    await dragPaperToFolder("Folder paper", "第二个文件夹");

    expect(await screen.findByRole("alert")).toHaveTextContent("后端暂时无法保存");
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    fireEvent.click(browse().getByRole("button", { name: /第二个文件夹/ }));
    expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument();
  });
});

describe("paper library pointer dragging", () => {
  beforeEach(() => {
    // jsdom has no PointerEvent or layout. Keep the real DndContext and sensors,
    // providing only the browser primitives needed for pointer hit testing.
    class TestPointerEvent extends MouseEvent {
      readonly isPrimary: boolean;
      readonly pointerId: number;
      readonly pointerType: string;
      constructor(type: string, init: PointerEventInit = {}) {
        super(type, init);
        this.isPrimary = init.isPrimary ?? true;
        this.pointerId = init.pointerId ?? 1;
        this.pointerType = init.pointerType ?? "mouse";
      }
    }
    vi.stubGlobal("PointerEvent", TestPointerEvent);
  });

  async function beginPointerDrag(title: string, folderName: string) {
    const handle = screen.getByRole("button", { name: `拖动 ${title}` });
    const folder = browse().getByRole("button", { name: new RegExp(folderName) });
    vi.spyOn(handle, "getBoundingClientRect").mockReturnValue(new DOMRect(400, 100, 24, 32));
    vi.spyOn(folder, "getBoundingClientRect").mockReturnValue(new DOMRect(20, 200, 180, 40));
    fireEvent.pointerDown(handle, { pointerId: 1, isPrimary: true, button: 0, buttons: 1, clientX: 412, clientY: 116 });
    await act(async () => {
      fireEvent.pointerMove(document, { pointerId: 1, isPrimary: true, buttons: 1, clientX: 430, clientY: 116 });
    });
    expect(await screen.findByText(/^移动 \d+ 篇文献$/)).toBeInTheDocument();
    await act(async () => {
      fireEvent.pointerMove(document, { pointerId: 1, isPrimary: true, buttons: 1, clientX: 100, clientY: 220 });
    });
    return handle;
  }

  async function releasePointer() {
    await act(async () => {
      fireEvent.pointerUp(document, { pointerId: 1, isPrimary: true, button: 0, buttons: 0, clientX: 100, clientY: 220 });
      // The real sensor releases its document-level click suppression after
      // 50 ms. Let it detach before the next test starts another interaction.
      await new Promise((resolve) => setTimeout(resolve, 60));
    });
  }

  it("moves a paper using pointer movement without native drag events", async () => {
    await renderLibrary();
    fireEvent.click(browse().getByRole("button", { name: /第一个文件夹/ }));
    await beginPointerDrag("Folder paper", "第二个文件夹");
    await releasePointer();

    await waitFor(() => expect(loadPreviewPaperLibrary(datasetId).papers.find((paper) => paper.id === "a")?.folderId).toBe("second"));
    expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument();
  });

  it("moves the selection when a selected paper is dragged with the pointer", async () => {
    await renderLibrary();
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Folder paper" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Unfiled paper" }));
    await beginPointerDrag("Folder paper", "第二个文件夹");
    await releasePointer();

    await waitFor(() => {
      const saved = loadPreviewPaperLibrary(datasetId);
      expect(saved.papers.find((paper) => paper.id === "a")?.folderId).toBe("second");
      expect(saved.papers.find((paper) => paper.id === "c")?.folderId).toBe("second");
    });
    expect(screen.getByRole("checkbox", { name: "选择 Folder paper" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "选择 Unfiled paper" })).not.toBeChecked();
  });

  it("does not save when dropping into the paper's existing folder", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    const save = vi.spyOn(localStorage, "setItem");
    await beginPointerDrag("Folder paper", "第一个文件夹");
    await releasePointer();

    expect(save).not.toHaveBeenCalled();
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
  });

  it("cancels a pointer drag without saving or moving the paper", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    const save = vi.spyOn(localStorage, "setItem");
    await beginPointerDrag("Folder paper", "第二个文件夹");
    await act(async () => {
      fireEvent.keyDown(document, { key: "Escape", code: "Escape" });
    });
    await releasePointer();

    expect(save).not.toHaveBeenCalled();
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
  });
});

describe("paper library backend arXiv import", () => {
  const result: ArxivImportResult = {
    paper: {
      title: "New arXiv paper", authors: ["A. Author", "B. Author"], year: 2024,
      abstract: "An abstract from the backend.", venue: "arXiv", base_id: "2404.06654", version: "v3",
      abs_url: "https://arxiv.org/abs/2404.06654v3", pdf_url: "https://arxiv.org/pdf/2404.06654v3",
    },
    work_id: "arxiv-work", document_id: "arxiv-pdf-document", document_task_id: "arxiv-task",
    filename: "2404.06654v3.pdf", size: 12345, source_url: "https://arxiv.org/pdf/2404.06654v3", source_version: "v3",
    content_sha256: "b".repeat(64), status: "imported", processing_status: "pending",
  };

  function candidate(version = "v3"): PaperItem {
    return {
      ...makePaper("arxiv-2404.06654", result.paper.title, null), source: "arxiv", arxiv: "2404.06654",
      arxivVersion: version, sourceUrl: `https://arxiv.org/pdf/2404.06654${version}`, abstract: result.paper.abstract,
    };
  }

  async function lookupArxivCandidate(version = "v3") {
    vi.mocked(lookupIdentifier).mockResolvedValueOnce(candidate(version));
    const dialog = await openImportDialog("arXiv / DOI 链接", "通过链接导入");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: `https://arxiv.org/pdf/2404.06654${version}` } });
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    await within(dialog).findByText(result.paper.title);
    return dialog;
  }

  it("previews arXiv metadata without saving a placeholder or using legacy import confirmation", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    const success = vi.spyOn(message, "success");
    await renderLibrary();
    const dialog = await lookupArxivCandidate();
    expect(within(dialog).getByRole("button", { name: "导入文献与 PDF" })).toBeEnabled();
    expect(within(dialog).queryByRole("button", { name: "加入论文库" })).not.toBeInTheDocument();
    expect(importArxivPaper).not.toHaveBeenCalled();
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
    expect(success).not.toHaveBeenCalled();
  });

  it("waits for a real backend PDF result, then stores its attachment identity", async () => {
    let finish!: (value: ArxivImportResult) => void;
    vi.mocked(importArxivPaper).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const success = vi.spyOn(message, "success");
    await renderLibrary();
    const dialog = await lookupArxivCandidate();
    fireEvent.click(within(dialog).getByRole("button", { name: "导入文献与 PDF" }));
    expect(importArxivPaper).toHaveBeenCalledWith("arXiv:2404.06654v3", datasetId);
    expect(loadPreviewPaperLibrary(datasetId).papers).toHaveLength(3);
    expect(success).not.toHaveBeenCalled();
    await act(async () => finish(result));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const saved = loadPreviewPaperLibrary(datasetId);
    const paper = saved.papers.find((item) => item.arxiv === "2404.06654");
    expect(paper).toMatchObject({ title: result.paper.title, arxivVersion: "v3", academicWorkId: result.work_id });
    expect(paper?.parse).not.toBe("ready");
    const attachment = saved.attachments.find((item) => item.contentRef === result.document_id);
    expect(attachment).toMatchObject({ paperId: paper!.id, name: result.filename, size: result.size, source: "arxiv" });
    expect(paper?.attachmentIds).toContain(attachment!.id);
    expect(success).toHaveBeenCalledOnce();
  });

  it("shows backend import failure without saving or showing success and permits retry", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    vi.mocked(importArxivPaper).mockRejectedValueOnce(new Error("arXiv PDF 下载失败，请重试")).mockResolvedValueOnce(result);
    const success = vi.spyOn(message, "success");
    await renderLibrary();
    const dialog = await lookupArxivCandidate();
    fireEvent.click(within(dialog).getByRole("button", { name: "导入文献与 PDF" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("arXiv PDF 下载失败，请重试");
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
    expect(success).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "导入文献与 PDF" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(importArxivPaper).toHaveBeenCalledTimes(2);
    expect(loadPreviewPaperLibrary(datasetId).attachments.some((attachment) => attachment.contentRef === result.document_id)).toBe(true);
  });

  it("imports a newer arXiv version into the existing parent without discarding notes or the old PDF", async () => {
    const data = loadPreviewPaperLibrary(datasetId);
    data.papers[0] = { ...data.papers[0], source: "arxiv", arxiv: "2404.06654", arxivVersion: "v1" };
    savePreviewPaperLibrary(datasetId, data);
    vi.mocked(importArxivPaper).mockResolvedValueOnce(result);
    await renderLibrary();
    const dialog = await lookupArxivCandidate();
    const confirm = await within(dialog).findByRole("button", { name: "导入文献与 PDF" });
    fireEvent.click(confirm);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const saved = loadPreviewPaperLibrary(datasetId);
    const matches = saved.papers.filter((paper) => paper.arxiv === "2404.06654");
    expect(matches).toHaveLength(1);
    expect(matches[0]).toMatchObject({ id: "a", arxivVersion: "v3", folderId: "first", important: true, noteIds: ["note-a"] });
    expect(saved.notes).toEqual(data.notes);
    expect(saved.annotations).toEqual(data.annotations);
    expect(saved.attachments.filter((attachment) => attachment.paperId === "a").map((attachment) => attachment.contentRef)).toEqual(expect.arrayContaining(["stored-pdf-a", result.document_id]));
    expect(saved.papers).toHaveLength(3);
  });

  it("restores backend-persisted papers and failed processing state on mount", async () => {
    vi.mocked(listImportedArxivPapers).mockResolvedValueOnce([{ ...result, processing_status: "failed", processing_error: "文档解析启动失败" }]);
    await renderLibrary();
    expect(await screen.findByRole("heading", { name: result.paper.title })).toBeInTheDocument();
    expect(listImportedArxivPapers).toHaveBeenCalledWith(datasetId);
    const saved = loadPreviewPaperLibrary(datasetId);
    expect(saved.papers.find((paper) => paper.arxiv === "2404.06654")).toMatchObject({ parse: "failed", processingError: "文档解析启动失败" });
    expect(saved.attachments.some((attachment) => attachment.contentRef === result.document_id)).toBe(true);
    expect(importArxivPaper).not.toHaveBeenCalled();
  });

  it("shows a sync error and restores the backend library when synchronization is retried", async () => {
    vi.mocked(listImportedArxivPapers).mockRejectedValueOnce(new Error("无法读取已导入文献")).mockResolvedValueOnce([result]);
    await renderLibrary();
    expect(await screen.findByRole("alert")).toHaveTextContent("无法读取已导入文献");
    fireEvent.click(screen.getByRole("button", { name: "重新同步" }));
    expect(await screen.findByRole("heading", { name: result.paper.title })).toBeInTheDocument();
    expect(listImportedArxivPapers).toHaveBeenCalledTimes(2);
  });

  it("allows metadata preview but prevents actual PDF import in the demo library", async () => {
    await renderLibrary({ datasetId: "paper-preview", preview: true });
    const dialog = await lookupArxivCandidate();
    const confirm = within(dialog).queryByRole("button", { name: "导入文献与 PDF" });
    if (confirm) {
      expect(confirm).toBeDisabled();
      fireEvent.click(confirm);
    }
    expect(importArxivPaper).not.toHaveBeenCalled();
    expect(listImportedArxivPapers).not.toHaveBeenCalled();
  });

  it("keeps removed arXiv papers hidden after reload and restores their notes only on explicit import", async () => {
    const original = loadPreviewPaperLibrary(datasetId);
    original.papers[0] = { ...original.papers[0], source: "arxiv", arxiv: result.paper.base_id, arxivVersion: "v3", academicWorkId: result.work_id };
    savePreviewPaperLibrary(datasetId, original);
    vi.mocked(listImportedArxivPapers).mockResolvedValue([{ ...result, processing_status: "ready" }]);
    const view = await renderLibrary();
    await waitFor(() => expect(loadPreviewPaperLibrary(datasetId).attachments.some((item) => item.contentRef === result.document_id)).toBe(true));
    fireEvent.mouseEnter(screen.getByRole("button", { name: "Folder paper 的更多操作" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "移除" }));
    const removeDialog = await screen.findByRole("dialog", { name: "从论文集移除这篇题录？" });
    fireEvent.click(within(removeDialog).getByRole("button", { name: /移\s*除/ }));
    await waitFor(() => expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument());
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    view.unmount();

    await renderLibrary();
    await waitFor(() => expect(listImportedArxivPapers).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: result.paper.title })).not.toBeInTheDocument();
    const removed = loadPreviewPaperLibrary(datasetId);
    expect(removed.hiddenArxivWorkIds).toContain(result.work_id);
    expect(removed.notes).toEqual(original.notes);

    vi.mocked(importArxivPaper).mockResolvedValueOnce({ ...result, status: "already_present", processing_status: "ready" });
    const importDialog = await lookupArxivCandidate();
    fireEvent.click(within(importDialog).getByRole("button", { name: "导入文献与 PDF" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const restored = loadPreviewPaperLibrary(datasetId);
    expect(restored.papers.find((item) => item.academicWorkId === result.work_id)).toMatchObject({ id: "a", title: "Folder paper", folderId: "first", important: true, noteIds: ["note-a"], annotationIds: ["annotation-a"] });
    expect(restored.hiddenArxivWorkIds).not.toContain(result.work_id);
    expect(restored.notes).toEqual(original.notes);
    expect(restored.attachments.filter((item) => item.paperId === "a").map((item) => item.contentRef)).toEqual(expect.arrayContaining(["stored-pdf-a", result.document_id]));
  });

  it("preserves papers synchronized while folder deletion is awaiting confirmation", async () => {
    let finishSync!: (value: ArxivImportResult[]) => void;
    vi.mocked(listImportedArxivPapers).mockImplementationOnce(() => new Promise((resolve) => { finishSync = resolve; }));
    const original = loadPreviewPaperLibrary(datasetId);
    await renderLibrary();
    fireEvent.click(browse().getByRole("button", { name: /第一个文件夹/ }));
    fireEvent.click(browse().getByRole("button", { name: "删除文件夹" }));
    const dialog = await screen.findByRole("dialog", { name: "删除文件夹「第一个文件夹」？" });
    await act(async () => finishSync([{ ...result, processing_status: "ready" }]));
    expect(loadPreviewPaperLibrary(datasetId).papers).toHaveLength(4);
    fireEvent.click(within(dialog).getByRole("button", { name: "删除文件夹" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const saved = loadPreviewPaperLibrary(datasetId);
    expect(saved.papers).toHaveLength(4);
    expect(saved.papers.find((item) => item.academicWorkId === result.work_id)).toBeDefined();
    expect(saved.attachments.some((item) => item.contentRef === result.document_id)).toBe(true);
    expect(saved.folders.some((item) => item.id === "first")).toBe(false);
    expect(saved.papers.find((item) => item.id === "a")?.folderId).toBeNull();
    expect(saved.notes).toEqual(original.notes);
  });
  it.each([false, true])("never writes a stale arXiv import into a different library session (return to original: %s)", async (returnToOriginal) => {
    let finish!: (item: ArxivImportResult) => void;
    vi.mocked(importArxivPaper).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const other = migratePaperLibrary({ folders: [], papers: [makePaper("other-library", "Other library paper", null)] });
    savePreviewPaperLibrary("other-dataset", other);
    const view = await renderLibrary();
    const dialog = await lookupArxivCandidate();
    fireEvent.click(within(dialog).getByRole("button", { name: "导入文献与 PDF" }));
    await act(async () => view.rerender(<ConfigProvider theme={{ token: { motion: false } }}><PaperLibrary datasetId="other-dataset" /></ConfigProvider>));
    expect(screen.getByRole("heading", { name: "Other library paper" })).toBeInTheDocument();
    if (returnToOriginal) await act(async () => view.rerender(<ConfigProvider theme={{ token: { motion: false } }}><PaperLibrary datasetId={datasetId} /></ConfigProvider>));
    await act(async () => finish(result));
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
    expect(loadPreviewPaperLibrary("other-dataset")).toEqual(other);
    expect(loadPreviewPaperLibrary(datasetId).papers.map((paper) => paper.id)).toEqual(["a", "b", "c"]);
    expect(screen.queryByRole("heading", { name: result.paper.title })).not.toBeInTheDocument();
  });

  it("does not issue a late snapshot write after the library is unmounted", async () => {
    let finish!: (item: ArxivImportResult) => void;
    vi.mocked(importArxivPaper).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const before = loadPreviewPaperLibrary(datasetId);
    const view = await renderLibrary();
    const dialog = await lookupArxivCandidate();
    fireEvent.click(within(dialog).getByRole("button", { name: "导入文献与 PDF" }));
    view.unmount();
    await act(async () => finish(result));
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
  });

  it("hydrates backend arXiv papers for readers without attempting to save", async () => {
    const data = migratePaperLibrary({ folders: [], papers: [] });
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValueOnce({ data, revision: 0, canWrite: false });
    vi.mocked(listImportedArxivPapers).mockResolvedValueOnce([result]);
    await renderLibrary();
    expect(await screen.findByRole("heading", { name: result.paper.title })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: result.paper.title })).toHaveAttribute("href", `/lib/knowledge/knowledge/${datasetId}/${result.document_id}`);
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
    expect(browse().getByRole("button", { name: /导入/ })).toBeDisabled();
  });

  it("does not let a delayed arXiv poll overwrite an in-flight user save", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    let finishSync!: (items: ArxivImportResult[]) => void;
    let finishSave!: () => void;
    vi.mocked(listImportedArxivPapers).mockImplementationOnce(() => new Promise((resolve) => { finishSync = resolve; })).mockResolvedValue([result]);
    vi.mocked(savePersistedPaperLibrary).mockImplementationOnce((id, data, revision) => new Promise((resolve) => {
      finishSave = () => { savePreviewPaperLibrary(id, data); resolve({ data, revision: revision + 1, canWrite: true }); };
    }));
    await renderLibrary();
    const star = within(screen.getByRole("heading", { name: "Folder paper" }).parentElement!).getByRole("button", { name: "取消收藏" });
    fireEvent.click(star);
    await act(async () => finishSync([result]));
    expect(screen.queryByRole("heading", { name: result.paper.title })).not.toBeInTheDocument();
    expect(star).toBeDisabled();
    await act(async () => finishSave());
    expect(star).toHaveAttribute("aria-pressed", "false");
    await act(async () => fireEvent.focus(window));
    expect(await screen.findByRole("heading", { name: result.paper.title })).toBeInTheDocument();
    const saved = loadPreviewPaperLibrary(datasetId);
    expect(saved.papers.find((paper) => paper.id === "a")?.important).toBe(false);
    expect(saved.notes).toEqual(before.notes);
    expect(saved.papers.find((paper) => paper.id === "a")?.metadata).toEqual(before.papers[0].metadata);
  });
});


describe("backend durable paper library", () => {
  it("retains a stale edit draft without overwriting another member's changes after refresh", async () => {
    await renderLibrary();
    fireEvent.mouseEnter(screen.getByRole("button", { name: "Folder paper 的更多操作" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "编辑" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑题录" });
    fireEvent.change(within(dialog).getByLabelText("题名"), { target: { value: "My unsaved title" } });
    const shared = loadPreviewPaperLibrary(datasetId);
    shared.papers[0].tags = ["Shared tag"];
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValueOnce({ data: shared, revision: 8, canWrite: true });
    await act(async () => fireEvent.focus(window));
    await act(async () => fireEvent.click(within(dialog).getByRole("button", { name: "保存题录" })));
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
    expect(within(dialog).getByRole("alert")).toHaveTextContent("重新打开编辑");
    expect(within(dialog).getByLabelText("题名")).toHaveValue("My unsaved title");
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(screen.getByText("Shared tag")).toBeInTheDocument();
  });

  it("honors reader access and never writes an arXiv synchronization snapshot", async () => {
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValueOnce({ data: loadPreviewPaperLibrary(datasetId), revision: 3, canWrite: false });
    await renderLibrary();
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(browse().getByRole("button", { name: /导入/ })).toBeDisabled();
    expect(browse().getByRole("button", { name: "新建文件夹" })).toBeDisabled();
    expect(within(screen.getByRole("heading", { name: "Folder paper" }).parentElement!).getByRole("button", { name: "取消收藏" })).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent("只读");
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
  });

  it("retains folders added by another member while a bibliography import is being confirmed", async () => {
    vi.mocked(lookupIdentifier).mockResolvedValueOnce(makePaper("new", "New import", null));
    await renderLibrary();
    const dialog = await openImportDialog("arXiv / DOI 链接", "通过链接导入");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "10.1000/example" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    await within(dialog).findByRole("button", { name: "加入论文库" });
    const shared = loadPreviewPaperLibrary(datasetId);
    shared.folders.push({ id: "remote-folder", name: "Shared folder" });
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValueOnce({ data: shared, revision: 9, canWrite: true });
    await act(async () => fireEvent.focus(window));
    await act(async () => fireEvent.click(within(dialog).getByRole("button", { name: "加入论文库" })));
    expect(loadPreviewPaperLibrary(datasetId).folders).toContainEqual({ id: "remote-folder", name: "Shared folder" });
    expect(screen.getByRole("heading", { name: "New import" })).toBeInTheDocument();
  });

  it("blocks edits until backend loading succeeds and never displays cached papers after a failed load", async () => {
    let fail!: (error: Error) => void;
    vi.mocked(loadPersistedPaperLibrary).mockImplementationOnce(() => new Promise((_, reject) => { fail = reject; }));
    await renderLibrary();
    expect(browse().getByRole("button", { name: /导入/ })).toBeDisabled();
    expect(browse().getByRole("button", { name: "新建文件夹" })).toBeDisabled();
    expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument();
    await act(async () => fail(new Error("服务器不可用")));
    expect(screen.getByRole("alert")).toHaveTextContent("服务器不可用");
    expect(browse().getByRole("button", { name: /导入/ })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
    expect(await screen.findByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(browse().getByRole("button", { name: /导入/ })).toBeEnabled();
  });

  it("keeps the import pending until its complete backend snapshot is acknowledged", async () => {
    let finish!: () => void;
    const success = vi.spyOn(message, "success");
    vi.mocked(savePersistedPaperLibrary).mockImplementationOnce((id, data, revision) => new Promise((resolve) => {
      finish = () => { savePreviewPaperLibrary(id, data); resolve({ data, revision: revision + 1, canWrite: true }); };
    }));
    vi.mocked(lookupIdentifier).mockResolvedValueOnce(makePaper("new", "Awaiting backend", null));
    await renderLibrary();
    const dialog = await openImportDialog("arXiv / DOI 链接", "通过链接导入");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "10.1000/example" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    const addButton = await within(dialog).findByRole("button", { name: "加入论文库" });
    await act(async () => fireEvent.click(addButton));
    expect(within(dialog).getByRole("button", { name: /加入论文库/ })).toBeDisabled();
    expect(screen.queryByRole("heading", { name: "Awaiting backend" })).not.toBeInTheDocument();
    expect(loadPreviewPaperLibrary(datasetId).papers).toHaveLength(3);
    expect(success).not.toHaveBeenCalled();
    await act(async () => finish());
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Awaiting backend" })).toBeInTheDocument();
    expect(success).toHaveBeenCalledOnce();
    expect(loadPreviewPaperLibrary(datasetId).papers.find((item) => item.id === "new")?.attachmentIds).toEqual([]);
  });

  it("rejects a failed star save visibly without changing metadata and refreshes shared changes on focus", async () => {
    const before = loadPreviewPaperLibrary(datasetId);
    before.attachments = [];
    before.papers.forEach((paper) => { paper.attachmentIds = []; });
    savePreviewPaperLibrary(datasetId, before);
    let fail!: (error: Error) => void;
    vi.mocked(savePersistedPaperLibrary).mockImplementationOnce(() => new Promise((_, reject) => { fail = reject; }));
    await renderLibrary();
    const star = within(screen.getByRole("heading", { name: "Folder paper" }).parentElement!).getByRole("button", { name: "取消收藏" });
    fireEvent.click(star);
    expect(star).toBeDisabled();
    expect(star).toHaveAttribute("aria-pressed", "true");
    await act(async () => fail(new Error("文献已由其他成员修改，请重新加载")));
    expect(screen.getByRole("alert")).toHaveTextContent("其他成员修改");
    expect(loadPreviewPaperLibrary(datasetId)).toEqual(before);
    const shared: PaperLibraryDataV3 = { ...before, papers: before.papers.map((paper) => ({ ...paper, title: paper.id === "a" ? "Shared update" : paper.title })) };
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValueOnce({ data: shared, revision: 12, canWrite: true });
    await act(async () => fireEvent.focus(window));
    expect(screen.getByRole("heading", { name: "Shared update" })).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole("heading", { name: "Shared update" }).parentElement!).getByRole("button", { name: "取消收藏" }));
    await waitFor(() => expect(loadPreviewPaperLibrary(datasetId).papers[0].important).toBe(false));
    const saved = loadPreviewPaperLibrary(datasetId);
    expect(saved.notes).toEqual(before.notes);
    expect(saved.papers[0].metadata).toEqual(before.papers[0].metadata);
    expect(saved.attachments).toEqual([]);
    expect(savePersistedPaperLibrary).toHaveBeenLastCalledWith(datasetId, expect.any(Object), 12);
  });
});
