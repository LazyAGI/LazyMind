import { libraryFixture } from "./__tests__/fixtures";
const taskApi = vi.hoisted(() => ({ createTasks: vi.fn(), startTasks: vi.fn() }));
vi.mock('../utils/request', () => ({ TaskServiceApi: () => taskApi }));
vi.mock('@/runtime/readiness', () => ({ waitForRuntimeCapability: vi.fn().mockResolvedValue(undefined) }));
vi.mock('../components/PaperTasks', () => ({ default: () => null }));
vi.mock('./libraryLoader', () => ({ loadPaperLibrarySources: vi.fn() }));
vi.mock('../utils/fileHash', () => ({ computeFileSha256: vi.fn().mockResolvedValue('test-file-hash') }));
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider, Modal, message } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import PaperLibrary from "./PaperLibrary";
import { loadPaperLibrarySources } from './libraryLoader';
import { setPaperImportance, updatePaperIdentity } from '../api/paperWorkflows';
vi.mock('../api/paperWorkflows', async original => ({ ...await original<typeof import('../api/paperWorkflows')>(), setPaperImportance: vi.fn(), updatePaperIdentity: vi.fn().mockResolvedValue(undefined) }));
import { previewPaperLink, importPaperLink, listAcademicImportBatches } from '../api/academicReferences';
vi.mock('../api/academicReferences', async original => ({ ...await original<typeof import('../api/academicReferences')>(), previewPaperLink: vi.fn(), importPaperLink: vi.fn(), listAcademicImportBatches: vi.fn(), retryAcademicImport: vi.fn() }));
const personalMarks = new Map<string, boolean>();
import type { PaperItem } from "./model";
import { emptyLibraryV3, type PaperLibraryDataV3 } from "./libraryModel";
import { loadPersistedPaperLibrary, savePersistedPaperLibrary, setLibraryPaperImportance } from "./store";
import { paperFromPdf } from "./pdfMeta";
import * as paperModel from "./model";

vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get: vi.fn(), post: vi.fn() } }));
vi.mock("./store", async (importOriginal) => ({
  ...await importOriginal<typeof import("./store")>(),
  loadPersistedPaperLibrary: vi.fn(),
  savePersistedPaperLibrary: vi.fn(),
  setLibraryPaperImportance: vi.fn(),
}));
vi.mock("./pdfMeta", () => ({ paperFromPdf: vi.fn() }));

const libraries = new Map<string, PaperLibraryDataV3>();
const readLibrary = (id: string): PaperLibraryDataV3 => JSON.parse(JSON.stringify(libraries.get(id) || emptyLibraryV3()));
const writeLibrary = (id: string, data: PaperLibraryDataV3) => { libraries.set(id, JSON.parse(JSON.stringify(data))); };
const lookupFixture = vi.fn<(input: string) => Promise<PaperItem>>();
const datasetId = "paper-navigation-test";
const makePaper = (id: string, title: string, folderId: string | null, important = false): PaperItem => ({
  id, title, folderId, important, authors: "Test Author", venue: "Research Journal",
  year: 2026, status: "unread", tags: [], source: "local", parse: "ready",
});

function seedLibrary() {
  const data = libraryFixture({
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
  writeLibrary(datasetId, data);
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

function folderTitle(name: RegExp): HTMLElement {
  return browse().getByText(name, { selector: '.paper-directory-node-name' }).closest('.paper-directory-folder-title') as HTMLElement;
}

function mockReload(snapshot: Awaited<ReturnType<typeof loadPersistedPaperLibrary>>) {
  vi.mocked(loadPersistedPaperLibrary).mockResolvedValueOnce(snapshot);
  vi.mocked(loadPaperLibrarySources).mockResolvedValueOnce({ ...snapshot, paperImportance: snapshot.paperImportance || Object.fromEntries(personalMarks), active: false, states: snapshot.data.attachments.filter(item => item.contentRef).map(item => ({ state: { document_id: item.contentRef!, kind: 'paper' as const, paper_id: item.paperId, manual: true, status: 'succeeded' }, is_important: false })) });
}

async function confirmPdfOwnership(dialog: HTMLElement) {
  const checkbox = within(dialog).queryByRole('checkbox', { name: /我已核对/ });
  if (checkbox && !(checkbox as HTMLInputElement).checked) fireEvent.click(checkbox);
}

async function dragPaperToFolder(title: string, folder: string, transfer = dragTransfer()) {
  const handle = screen.getByRole("button", { name: `拖动 ${title}` });
  await act(async () => {
  fireEvent.dragStart(handle, { dataTransfer: transfer });
  const target = folderTitle(new RegExp(folder));
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
  libraries.clear();
  vi.clearAllMocks();
  lookupFixture.mockReset();
  vi.mocked(listAcademicImportBatches).mockReset().mockResolvedValue({ items: [], next_cursor: '', notification: { active: 0, needs_action: 0 } });
  vi.mocked(importPaperLink).mockReset().mockResolvedValue({ batch_id: 'link-batch' });
  vi.mocked(previewPaperLink).mockReset().mockImplementation(async input => {
    const item = await lookupFixture(input);
    return { work_id: item.id, title: item.title, doi: item.doi, arxiv_id: item.arxiv,
      paper: { paper_id: item.id, title: item.title, authors: item.authors.split('、'), venue: item.venue, year: item.year, abstract: item.abstract || null },
      disposition: item.arxiv ? 'downloadable' : 'metadata_only',
      presence: { status: 'absent', current_dataset: [], other_datasets: [], importing: false, coverage_complete: true },
      fulltext_candidates: item.arxiv ? [{ url: item.sourceUrl!, version: item.arxivVersion, source_provider: 'arxiv', source_type: 'official_pdf' }] : [] };
  });
  ConfigProvider.config({ holderRender: (children: React.ReactNode) => <ConfigProvider theme={{ token: { motion: false } }}>{children}</ConfigProvider> });
  seedLibrary();
  personalMarks.clear(); personalMarks.set('a', true); personalMarks.set('b', true);
  vi.mocked(setPaperImportance).mockImplementation(async (id, value) => { personalMarks.set(id, value); });
  vi.mocked(setLibraryPaperImportance).mockImplementation(async (id, paperId, revision, value) => { personalMarks.set(paperId, value); return { data: readLibrary(id), revision, canWrite: true, paperImportance: Object.fromEntries(personalMarks) }; });
  vi.mocked(loadPaperLibrarySources).mockImplementation(async id => {
    const data = readLibrary(id);
    return { data, revision: 0, canWrite: true, paperImportance: Object.fromEntries(personalMarks), active: false, states: data.attachments.filter(item => item.contentRef).map(item => ({
      state: { document_id: item.contentRef!, kind: 'paper' as const, paper_id: item.paperId, manual: true, status: 'succeeded' },
      is_important: !!personalMarks.get(item.paperId),
    })) };
  });
  vi.mocked(loadPersistedPaperLibrary).mockReset().mockImplementation(async (id) => ({ data: readLibrary(id), revision: 0, canWrite: true, paperImportance: Object.fromEntries(personalMarks) }));
  vi.mocked(savePersistedPaperLibrary).mockReset().mockImplementation(async (id, data, revision) => {
    writeLibrary(id, data);
    return { data, revision: revision + 1, canWrite: true, paperImportance: Object.fromEntries(personalMarks) };
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
  it('shows paper leaves under the named library and keeps global views independent from nested directories', async () => {
    const data = readLibrary(datasetId);
    data.folders.push({ id: 'nested', name: '子目录', parentId: 'first' });
    data.papers[0].folderId = 'nested';
    writeLibrary(datasetId, data);
    await renderLibrary({ title: '我的论文知识库' });
    const tree = browse().getByRole('tree', { name: '论文目录' });
    expect(within(tree).getByText('我的论文知识库')).toBeInTheDocument();
    expect(within(tree).getByText('Unfiled paper')).toBeInTheDocument();
    expect(within(tree).queryByText('待整理')).not.toBeInTheDocument();
    fireEvent.click(folderTitle(/第一个文件夹/));
    expect(screen.queryByRole('heading', { name: 'Folder paper' })).not.toBeInTheDocument();
    expect(screen.getByText(/本层 0 篇.*含子目录 1 篇/)).toBeInTheDocument();
    fireEvent.click(folderTitle(/子目录/));
    expect(screen.getByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
    fireEvent.click(browse().getByRole('button', { name: /重点论文/ }));
    expect(screen.getByRole('heading', { name: 'Other favorite' })).toBeInTheDocument();
    fireEvent.click(folderTitle(/我的论文知识库/));
    expect(screen.getByRole('heading', { name: 'Unfiled paper' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Folder paper' })).not.toBeInTheDocument();
  });

  it('adds a PDF from metadata-only paper details and blocks a reliably different paper before upload', async () => {
    const data = readLibrary(datasetId);
    data.papers[2].doi = '10.1000/target';
    writeLibrary(datasetId, data);
    vi.mocked(paperFromPdf).mockResolvedValueOnce({ ...makePaper('wrong', 'Another paper', null), doi: '10.1000/other', identityVerified: true });
    const upload = vi.fn();
    await renderLibrary({ onUploadPdf: upload });
    fireEvent.click(screen.getByRole('button', { name: 'Unfiled paper' }));
    const detail = within(screen.getByRole('dialog', { name: '文献详情' }));
    fireEvent.click(detail.getByRole('button', { name: '添加 PDF' }));
    const dialog = await screen.findByRole('dialog', { name: '上传文献' });
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(['pdf'], 'wrong.pdf')] } });
    expect(await within(dialog).findByText('PDF 与当前论文身份不一致')).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: '添加 PDF' })).toBeDisabled();
    expect(within(dialog).getByRole('combobox', { name: 'PDF 关联论文' })).toHaveAttribute('disabled');
    expect(upload).not.toHaveBeenCalled();
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
  });

  it("does not show an old missing-title prompt before current recognition metadata is loaded", async () => {
    const data = readLibrary(datasetId);
    data.papers[0].incomplete = true;
    writeLibrary(datasetId, data);
    let finish!: (source: Awaited<ReturnType<typeof loadPaperLibrarySources>>) => void;
    vi.mocked(loadPaperLibrarySources).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    await renderLibrary();
    expect(screen.getByText('正在加载文献…')).toBeVisible();
    expect(screen.queryByRole('heading', { name: 'Folder paper' })).not.toBeInTheDocument();
    expect(screen.queryByText(/没有符合条件的论文/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '待补全论文题名' })).not.toBeInTheDocument();
    await act(async () => finish({
      data: { ...data, papers: data.papers.map(paper => paper.id === 'a' ? { ...paper, title: 'Confirmed title', incomplete: false } : paper) }, revision: 0, canWrite: true, active: false, paperImportance: {},
      states: [{ state: { document_id: 'stored-pdf-a', kind: 'paper', status: 'succeeded', paper_id: 'a', manual: false },
        paper: { paper_id: 'a', title: 'Confirmed title', abstract: null }, is_important: false }],
    }));
    expect(screen.getByRole('heading', { name: 'Confirmed title' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '待补全论文题名' })).not.toBeInTheDocument();
  });

  it("opens the missing-title editor directly and clears the prompt only after a successful save", async () => {
    const data = readLibrary(datasetId);
    data.papers[0].incomplete = true;
    writeLibrary(datasetId, data);
    await renderLibrary();
    fireEvent.click(screen.getByRole('button', { name: '待补全论文题名' }));
    const dialog = within(screen.getByRole('dialog', { name: '文献详情' }));
    fireEvent.change(dialog.getByLabelText('题名'), { target: { value: 'Confirmed title' } });
    fireEvent.click(dialog.getByRole('button', { name: /保存题录/ }));
    await waitFor(() => expect(updatePaperIdentity).toHaveBeenCalledWith('stored-pdf-a', expect.objectContaining({ paper: expect.objectContaining({ title: 'Confirmed title' }) })));
    await waitFor(() => expect(readLibrary(datasetId).papers[0].incomplete).toBe(false));
    expect(screen.queryByRole('button', { name: '待补全论文题名' })).not.toBeInTheDocument();
  });

  it("opens paper details consistently and lets readers choose among PDF attachments", async () => {
    const data = readLibrary(datasetId);
    data.attachments.push({ ...data.attachments[0], id: "second-pdf", contentRef: "second-doc", isPrimary: false, sourceVersion: 'v1' });
    data.papers[0].attachmentIds.push('second-pdf');
    writeLibrary(datasetId, data);
    await renderLibrary();
    fireEvent.click(screen.getByRole('button', { name: 'Folder paper' }));
    const detail = within(screen.getByRole('dialog', { name: '文献详情' }));
    fireEvent.click(detail.getByRole('tab', { name: '附件 2' }));
    expect(detail.getAllByRole('button', { name: '打开 PDF' })).toHaveLength(2);
    expect(detail.getByText(/v1/)).toBeInTheDocument();
    fireEvent.click(detail.getByRole('button', { name: '关闭' }));
    const row = within(screen.getByRole('heading', { name: 'Folder paper' }).closest('tr')!);
    fireEvent.click(row.getByRole('button', { name: /阅读/ }));
    const menu = within(await screen.findByRole('menu'));
    expect(menu.getByRole('link', { name: /v1/ })).toHaveAttribute('href', `/lib/knowledge/knowledge/${datasetId}/second-doc`);
    expect(menu.getByRole('link', { name: /版本未知.*主附件/ })).toHaveAttribute('href', `/lib/knowledge/knowledge/${datasetId}/stored-pdf-a`);
  });

  it("keeps metadata accessible separately and uses it when there is no readable attachment", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    await renderLibrary();
    fireEvent.mouseEnter(screen.getByRole("button", { name: "Folder paper 的更多操作" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "查看详情" }));
    expect(screen.getByRole("dialog", { name: "文献详情" })).toHaveTextContent("Folder paper");
    expect(open).not.toHaveBeenCalled();
  });

  it("opens metadata for papers without a readable PDF", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    await renderLibrary();
    fireEvent.click(screen.getByRole("button", { name: "Unfiled paper" }));
    expect(screen.getByRole("dialog", { name: "文献详情" })).toHaveTextContent("Unfiled paper");
    expect(open).not.toHaveBeenCalled();
  });

  it('opens attachment and note counts directly on their detail tabs', async () => {
    await renderLibrary();
    const row = within(screen.getByRole('heading', { name: 'Folder paper' }).closest('tr')!);
    fireEvent.click(row.getByRole('button', { name: 'PDF · 1 份' }));
    expect(within(screen.getByRole('dialog', { name: '文献详情' })).getByRole('tab', { name: '附件 1' })).toHaveAttribute('aria-selected', 'true');
    fireEvent.click(within(screen.getByRole('dialog', { name: '文献详情' })).getByRole('button', { name: '关闭' }));
    fireEvent.click(row.getByRole('button', { name: '1 条笔记' }));
    expect(within(screen.getByRole('dialog', { name: '文献详情' })).getByRole('tab', { name: '笔记 1' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('Keep these notes')).toBeInTheDocument();
  });

  it.each(['menu', 'detail'])('reuses document reparse from %s with the primary PDF and actual processing level', async entry => {
    const data = readLibrary(datasetId);
    data.papers[0].parse = 'failed';
    writeLibrary(datasetId, data);
    taskApi.createTasks.mockResolvedValue({ data: { tasks: [{ task_id: 'reparse-task' }] } });
    taskApi.startTasks.mockResolvedValue({ data: { started_count: 1 } });
    await renderLibrary({ processingLevel: 'parsed' });
    if (entry === 'menu') {
      fireEvent.mouseEnter(screen.getByRole('button', { name: 'Folder paper 的更多操作' }));
      fireEvent.click(await screen.findByRole('menuitem', { name: '重新解析' }));
    } else {
      const row = within(screen.getByRole('heading', { name: 'Folder paper' }).closest('tr')!);
      fireEvent.click(row.getByRole('button', { name: 'PDF · 1 份' }));
      const detail = within(screen.getByRole('dialog', { name: '文献详情' }));
      expect(detail.getByRole('tab', { name: '附件 1' })).toHaveAttribute('aria-selected', 'true');
      expect(detail.getByRole('status', { name: '正文解析状态' })).toHaveTextContent('正文解析失败');
      fireEvent.click(detail.getByRole('button', { name: '重新解析' }));
    }
    const dialog = screen.getByRole('dialog', { name: '重新解析' });
    fireEvent.click(within(dialog).getByRole('button', { name: /OK|确\s*定/ }));
    await waitFor(() => expect(taskApi.createTasks).toHaveBeenCalled());
    expect(taskApi.createTasks.mock.calls[0]).toEqual([datasetId, { parent: `datasets/${datasetId}`, items: [{ upload_file_id: '', task: { task_type: 'TASK_TYPE_REPARSE', document_ids: ['stored-pdf-a'], display_name: expect.any(String), reparse_groups: [], reparse_mode: 'rebuild' } }] }]);
    await waitFor(() => expect(taskApi.startTasks).toHaveBeenCalledWith(datasetId, { task_ids: ['reparse-task'] }));
  });

  it.each(['read-only', 'parser-unavailable', 'stored'])('disables detail reparse when %s', async reason => {
    const data = readLibrary(datasetId);
    data.papers[0].parse = 'failed';
    writeLibrary(datasetId, data);
    if (reason === 'read-only') mockReload({ data, revision: 0, canWrite: false });
    await renderLibrary({ canParse: reason !== 'parser-unavailable', processingLevel: reason === 'stored' ? 'stored' : 'parsed' });
    const row = within(screen.getByRole('heading', { name: 'Folder paper' }).closest('tr')!);
    fireEvent.click(row.getByRole('button', { name: 'PDF · 1 份' }));
    expect(within(screen.getByRole('dialog', { name: '文献详情' })).getByRole('button', { name: '重新解析' })).toBeDisabled();
    expect(taskApi.createTasks).not.toHaveBeenCalled();
  });

  it('acknowledges a metadata retry and shows the pending state while refreshing', async () => {
    let status = 'failed';
    vi.mocked(loadPaperLibrarySources).mockImplementation(async () => ({ data: readLibrary(datasetId), revision: 0, canWrite: true, active: false, paperImportance: {}, states: [{ state: { document_id: 'stored-pdf-a', kind: 'paper', paper_id: 'a', status, manual: false, error: status === 'failed' ? 'Model unavailable' : undefined }, is_important: false }] }));
    vi.mocked(updatePaperIdentity).mockImplementationOnce(async () => { status = 'pending'; return { status }; });
    await renderLibrary();
    fireEvent.click(screen.getByRole('button', { name: '论文信息识别失败' }));
    fireEvent.click(within(screen.getByRole('dialog', { name: '文献详情' })).getByRole('button', { name: '重试自动识别' }));
    await waitFor(() => expect(updatePaperIdentity).toHaveBeenCalledWith('stored-pdf-a', { reidentify: true }));
    await waitFor(() => expect(screen.getByRole('status', { name: '论文信息自动识别' })).toHaveTextContent('待识别'));
    expect(screen.queryByRole('button', { name: '重试自动识别' })).not.toBeInTheDocument();
  });

  it("places the star beside the title and updates favorites without opening the reader", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    await renderLibrary();
    const title = screen.getByRole("heading", { name: "Folder paper" });
    await act(async () => fireEvent.click(within(title.parentElement!).getByRole("button", { name: /取消收藏|取消重点|paperTasks.unmarkImportant/ })));
    expect(personalMarks.get("a")).toBe(false);
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

    fireEvent.click(folderTitle(/第一个文件夹/));
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Other favorite" })).not.toBeInTheDocument();
    fireEvent.click(browse().getByRole("button", { name: /收藏|重点论文/ }));
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Other favorite" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Unfiled paper" })).not.toBeInTheDocument();
    expect(browse().getByRole("button", { name: "删除文件夹" })).toBeDisabled();
  });

  it.each(["bib", "ris", "csv"] as const)("exports only selected papers as %s from the document toolbar", async (format) => {
    const readDownload = captureDownload();
    await renderLibrary();
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Folder paper" }));
    const bulkActions = within(screen.getByRole("region", { name: "已选论文" }));
    expect(bulkActions.queryByRole("button", { name: /更\s*多/ })).not.toBeInTheDocument();
    fireEvent.click(libraryTools().getByRole("button", { name: "导出题录" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: new RegExp(format === "bib" ? "BibTeX" : format.toUpperCase()) }));

    const content = await readDownload();
    expect(content).toContain("Folder paper");
    expect(content).not.toContain("Other favorite");
    expect(content).not.toContain("Unfiled paper");
    if (format === "csv") expect(content).toContain("第一个文件夹");
  });

  it("groups search, filter, export and import notifications in the document toolbar", async () => {
    await renderLibrary();
    const tools = libraryTools();
    expect(tools.getAllByRole("button")).toHaveLength(4);
    for (const name of ["搜索", "筛选", "导出题录"]) expect(tools.getByRole("button", { name })).toBeInTheDocument();
    expect(tools.getByRole("button", { name: /论文导入通知/ })).toHaveAttribute("aria-expanded", "false");
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

  it("switches between tools and import notifications without overlapping panels", async () => {
    await renderLibrary();
    for (const [tool, panel] of [["搜索", "搜索文献"], ["筛选", "筛选文献"], ["论文导入通知：0 项进行中 · 0 项待处理", "论文导入通知"], ["搜索", "搜索文献"]] as const) {
      const button = libraryTools().getByRole("button", { name: tool });
      fireEvent.mouseDown(button);
      fireEvent.mouseUp(button);
      fireEvent.click(button);
      await waitFor(() => {
        expect(screen.getAllByRole("dialog")).toHaveLength(1);
        expect(screen.getByRole("dialog", { name: panel })).toBeInTheDocument();
      });
    }
    const exportButton = libraryTools().getByRole("button", { name: "导出题录" });
    fireEvent.mouseDown(exportButton);
    fireEvent.mouseUp(exportButton);
    fireEvent.click(exportButton);
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(screen.getAllByRole("menu")).toHaveLength(1);
      expect(screen.getByRole("menuitem", { name: /BibTeX/ })).toBeInTheDocument();
    });
  });

  it("filters reading state and real PDF availability from the grouped panel", async () => {
    const data = readLibrary(datasetId);
    data.papers[1].status = "read";
    writeLibrary(datasetId, data);
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
    const data = readLibrary(datasetId);
    data.papers[0].tags = ["RAG"];
    writeLibrary(datasetId, data);
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
    fireEvent.click(libraryTools().getByRole("button", { name: "导出题录" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: new RegExp(format === "bib" ? "BibTeX" : format.toUpperCase()) }));
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
    await waitFor(() => expect(folderTitle(/新主题/).closest(".ant-tree-treenode")).toHaveClass("ant-tree-treenode-selected"));
    expect(readLibrary(datasetId).folders.some((folder) => folder.name === "新主题")).toBe(true);
    expect(browse().getByRole("button", { name: "删除文件夹" })).toBeEnabled();
  });

  it("creates a folder when HTTP origins do not expose crypto.randomUUID", async () => {
    vi.stubGlobal("crypto", { getRandomValues: crypto.getRandomValues.bind(crypto) });
    await renderLibrary();
    fireEvent.click(browse().getByRole("button", { name: "新建文件夹" }));
    const dialog = await screen.findByRole("dialog", { name: "新建文件夹" });
    fireEvent.change(within(dialog).getByRole("textbox", { name: "文件夹名称" }), { target: { value: "HTTP folder" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /创\s*建/ }));
    await waitFor(() => expect(folderTitle(/HTTP folder/).closest(".ant-tree-treenode")).toHaveClass("ant-tree-treenode-selected"));
    expect(readLibrary(datasetId).folders.find((folder) => folder.name === "HTTP folder")?.id).toMatch(/^folder-[a-f0-9-]{36}$/);
  });

  it("deletes a selected folder while preserving its papers, notes and attachments", async () => {
    const before = readLibrary(datasetId);
    await renderLibrary();
    fireEvent.click(folderTitle(/第一个文件夹/));
    fireEvent.click(browse().getByRole("button", { name: "删除文件夹" }));
    const dialog = await screen.findByRole("dialog", { name: "删除文件夹「第一个文件夹」？" });
    fireEvent.click(within(dialog).getByRole("button", { name: "删除文件夹" }));
    await waitFor(() => expect(browse().queryByText("第一个文件夹", { exact: true })).not.toBeInTheDocument());
    const after = readLibrary(datasetId);
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
    const before = readLibrary(datasetId);
    await renderLibrary();
    fireEvent.click(browse().getByRole("button", { name: "新建文件夹" }));
    const dialog = await screen.findByRole("dialog", { name: "新建文件夹" });
    fireEvent.change(within(dialog).getByRole("textbox", { name: "文件夹名称" }), { target: { value: "文献整理" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /创\s*建/ }));
    await waitFor(() => expect(readLibrary(datasetId).folders).toHaveLength(3));
    const after = readLibrary(datasetId);
    expect(after.papers).toEqual(before.papers);
    expect(after.notes).toEqual(before.notes);
    expect(after.attachments).toEqual(before.attachments);
    expect(after.annotations).toEqual(before.annotations);
  });

  it("keeps the folder and its papers when deletion is cancelled", async () => {
    const before = readLibrary(datasetId);
    await renderLibrary();
    fireEvent.click(folderTitle(/第一个文件夹/));
    fireEvent.click(browse().getByRole("button", { name: "删除文件夹" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /取\s*消/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(readLibrary(datasetId)).toEqual(before);
    expect(folderTitle(/第一个文件夹/).closest(".ant-tree-treenode")).toHaveClass("ant-tree-treenode-selected");
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
    expect(onUploadPdf.mock.calls[0]).toEqual([vi.mocked(paperFromPdf).mock.calls[0][0], expect.objectContaining({ idempotency_key: expect.any(String), library_paper_id: "uploaded-paper", title: "A different original study" })]);
    expect(screen.getAllByRole("dialog")).toHaveLength(1);
    expect(addButton).toBeDisabled();
    expect(readLibrary(datasetId).papers.some((item) => item.id === paper.id)).toBe(false);

    await act(async () => finishUpload("document-uploaded-pdf"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const saved = readLibrary(datasetId);
    const attachment = saved.attachments.find((item) => item.paperId === paper.id);
    expect(attachment).toMatchObject({
      name: file.name, size: file.size, mimeType: "application/pdf",
      kind: "pdf", contentRef: "document-uploaded-pdf", isPrimary: true,
    });
    expect(saved.papers.find((item) => item.id === paper.id)?.attachmentIds).toEqual([attachment!.id]);
    expect(onUploadPdf).toHaveBeenCalledOnce();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it.each([false, true])("attaches a confirmed PDF to the existing paper and preserves its research data (retry: %s)", async (retry) => {
    const before = readLibrary(datasetId);
    before.attachments = before.attachments.filter((item) => item.paperId !== "a");
    before.papers[0].attachmentIds = [];
    before.papers[0].doi = "10.1234/existing";
    writeLibrary(datasetId, before);
    vi.mocked(paperFromPdf).mockResolvedValueOnce({ ...makePaper("extracted-pdf", "Different PDF title", null), doi: "10.1234/existing", identityVerified: true });
    const upload = vi.fn().mockResolvedValue("attached-document");
    await renderLibrary({ onUploadPdf: upload });
    const dialog = await openImportDialog("直接上传", "上传文献");
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["pdf"], "existing.pdf")] } });
    const confirm = await within(dialog).findByRole("button", { name: "添加 PDF" });
    await confirmPdfOwnership(dialog);
    expect(within(dialog).getAllByText("Folder paper").length).toBeGreaterThan(0);
    expect(upload).not.toHaveBeenCalled();
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
    if (retry) vi.mocked(savePersistedPaperLibrary).mockRejectedValueOnce(new Error("保存冲突，请重试"));
    fireEvent.click(confirm);
    if (retry) {
      expect(await within(dialog).findByText("保存冲突，请重试")).toBeInTheDocument();
      expect(readLibrary(datasetId)).toEqual(before);
      await waitFor(() => expect(confirm).toBeEnabled());
      fireEvent.click(confirm);
    }
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const saved = readLibrary(datasetId);
    const attachment = saved.attachments.find((item) => item.contentRef === "attached-document")!;
    expect(attachment).toMatchObject({ paperId: "a", name: "existing.pdf", isPrimary: true });
    expect(saved.papers).toHaveLength(3);
    expect(saved.papers[0]).toMatchObject({ ...before.papers[0], attachmentIds: [attachment.id] });
    expect(saved.notes).toEqual(before.notes);
    expect(saved.annotations).toEqual(before.annotations);
    expect(upload).toHaveBeenCalledOnce();
  });

  it("cancels a staged PDF attachment without uploading or changing its existing paper", async () => {
    const before = readLibrary(datasetId);
    vi.mocked(paperFromPdf).mockResolvedValueOnce({ ...makePaper("extracted-pdf", "Folder paper", null) });
    const upload = vi.fn().mockResolvedValue("should-not-upload");
    await renderLibrary({ onUploadPdf: upload });
    const dialog = await openImportDialog("直接上传", "上传文献");
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["pdf"], "cancel.pdf")] } });
    await within(dialog).findByRole("button", { name: "添加 PDF" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(upload).not.toHaveBeenCalled();
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
    expect(readLibrary(datasetId)).toEqual(before);
  });

  it.each([false, true])("reconciles a staged attachment with refreshed metadata without restoring removed papers (removed: %s)", async (removed) => {
    vi.mocked(paperFromPdf).mockResolvedValueOnce(makePaper("extracted-pdf", "Folder paper", null));
    const upload = vi.fn().mockResolvedValue("attached-after-refresh");
    await renderLibrary({ onUploadPdf: upload });
    const dialog = await openImportDialog("直接上传", "上传文献");
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["pdf"], "refresh.pdf")] } });
    const confirm = await within(dialog).findByRole("button", { name: "添加 PDF" });
    await confirmPdfOwnership(dialog);
    const shared = readLibrary(datasetId);
    shared.papers = removed ? shared.papers.filter((paper) => paper.id !== "a") : shared.papers.map((paper) => paper.id === "a" ? { ...paper, title: "Shared title", tags: ["shared-tag"] } : paper);
    mockReload({ data: shared, revision: 7, canWrite: true });
    await act(async () => fireEvent.focus(window));
    fireEvent.click(confirm);
    if (removed) {
      expect(await within(dialog).findByText(/目标文献已被移除/)).toBeInTheDocument();
      expect(upload).not.toHaveBeenCalled();
      expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
      expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument();
    } else {
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
      const saved = readLibrary(datasetId);
      expect(saved.papers[0]).toMatchObject({ id: "a", title: "Shared title", tags: ["shared-tag"] });
      expect(saved.attachments.find((item) => item.contentRef === "attached-after-refresh")).toMatchObject({ paperId: "a", isPrimary: false });
      expect(saved.attachments.find((item) => item.contentRef === "stored-pdf-a")?.isPrimary).toBe(true);
      expect(savePersistedPaperLibrary).toHaveBeenLastCalledWith(datasetId, expect.any(Object), 7, undefined, expect.objectContaining({ operation: "attach", confirmed: true }));
    }
  });

  it("recovers an acknowledged attachment after a lost save response without duplicating its graph entry", async () => {
    const before = readLibrary(datasetId);
    before.attachments = before.attachments.filter((item) => item.paperId !== "a");
    before.papers[0].attachmentIds = [];
    writeLibrary(datasetId, before);
    vi.mocked(paperFromPdf).mockResolvedValueOnce(makePaper("extracted-pdf", "Folder paper", null));
    const upload = vi.fn().mockResolvedValue("accepted-document");
    await renderLibrary({ onUploadPdf: upload });
    const dialog = await openImportDialog("直接上传", "上传文献");
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["pdf"], "accepted.pdf")] } });
    const confirm = await within(dialog).findByRole("button", { name: "添加 PDF" });
    await confirmPdfOwnership(dialog);
    vi.mocked(savePersistedPaperLibrary).mockImplementationOnce(async (id, data) => {
      writeLibrary(id, data);
      throw new Error("保存响应丢失");
    });
    fireEvent.click(confirm);
    expect(await within(dialog).findByText("保存响应丢失")).toBeInTheDocument();
    mockReload({ data: readLibrary(datasetId), revision: 1, canWrite: true });
    await act(async () => fireEvent.focus(window));
    fireEvent.click(confirm);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const saved = readLibrary(datasetId);
    expect(saved.attachments.filter((item) => item.contentRef === "accepted-document")).toHaveLength(1);
    expect(saved.attachments.find((item) => item.contentRef === "accepted-document")?.isPrimary).toBe(true);
    expect(saved.papers[0].attachmentIds).toHaveLength(1);
    expect(upload).toHaveBeenCalledOnce();
  });

  it("preserves notes and metadata when loading a library without attachments", async () => {
    const data = readLibrary(datasetId);
    data.attachments = [];
    data.papers.forEach((paper) => { paper.attachmentIds = []; });
    writeLibrary(datasetId, data);

    const loaded = readLibrary(datasetId);
    expect(loaded).toEqual(data);
    expect(loaded.notes).toHaveLength(1);
    expect(loaded.attachments).toEqual([]);
    expect(loaded.papers[0].noteIds).toEqual(["note-a"]);
    expect(loaded.papers[0].metadata.abstract).toBe("Preserve the original abstract.");
  });

  it("shows identifier failures persistently and removes the previous import candidate", async () => {
    lookupFixture
      .mockResolvedValueOnce(makePaper("new", "New candidate", null))
      .mockRejectedValueOnce(new Error("arXiv 服务暂时不可用"));
    await renderLibrary();
    const dialog = await openImportDialog("arXiv / DOI 链接", "通过链接导入");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "2404.06654" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    expect(await within(dialog).findByRole("button", { name: "保存题录" })).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("arXiv 服务暂时不可用");
    await act(async () => message.destroy());
    expect(within(dialog).getByRole("alert")).toHaveTextContent("arXiv 服务暂时不可用");
    expect(within(dialog).queryByRole("button", { name: "保存题录" })).not.toBeInTheDocument();
    expect(readLibrary(datasetId).papers).toHaveLength(3);
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
    expect(readLibrary(datasetId).papers).toHaveLength(3);
  });

  it("discards an earlier unconfirmed candidate when the next file cannot be read", async () => {
    vi.mocked(paperFromPdf)
      .mockResolvedValueOnce(makePaper("new", "Previous upload", null))
      .mockRejectedValueOnce(new Error("PDF 文件已损坏"));
    const before = readLibrary(datasetId);
    await renderLibrary({ onUploadPdf: async () => "uploaded-document" });
    const dialog = await openImportDialog("直接上传", "上传文献");
    const input = dialog.querySelector<HTMLInputElement>('input[type="file"]')!;
    fireEvent.change(input, { target: { files: [new File(["%PDF-first"], "first.pdf", { type: "application/pdf" })] } });
    expect(await within(dialog).findByRole("button", { name: "加入论文库" })).toBeInTheDocument();
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["broken"], "broken.pdf", { type: "application/pdf" })] } });
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("PDF 文件已损坏");
    expect(within(dialog).queryByRole("button", { name: "加入论文库" })).not.toBeInTheDocument();
    expect(within(dialog).queryByText("Previous upload")).not.toBeInTheDocument();
    expect(readLibrary(datasetId)).toEqual(before);
  });

  it("cancels PDF confirmation without creating an upload, document or library record", async () => {
    const upload = vi.fn().mockResolvedValue("should-not-upload");
    const before = readLibrary(datasetId);
    vi.mocked(paperFromPdf).mockResolvedValueOnce(makePaper("new-pdf", "Unconfirmed PDF", null));
    await renderLibrary({ onUploadPdf: upload });
    const dialog = await openImportDialog("直接上传", "上传文献");
    fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["pdf"], "cancel.pdf")] } });
    await within(dialog).findByRole("button", { name: "加入论文库" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(upload).not.toHaveBeenCalled();
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
    expect(readLibrary(datasetId)).toEqual(before);
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
    expect(readLibrary(datasetId).attachments.find((item) => item.paperId === "new-pdf")?.contentRef).toBe("one-uploaded-document");
  });

  it("keeps the import dialog and original library when saving fails, then permits retry", async () => {
    lookupFixture.mockResolvedValueOnce(makePaper("new", "Retry candidate", null));
    await renderLibrary();
    const dialog = await openImportDialog("arXiv / DOI 链接", "通过链接导入");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "2404.06654" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    const confirm = await within(dialog).findByRole("button", { name: "保存题录" });
    vi.mocked(savePersistedPaperLibrary).mockRejectedValueOnce(new Error("后端暂时无法保存"));
    fireEvent.click(confirm);
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("后端暂时无法保存");
    expect(readLibrary(datasetId).papers.some(paper => paper.title === "Retry candidate")).toBe(false);
    expect(readLibrary(datasetId).papers).toHaveLength(3);
    expect(within(dialog).getByRole("button", { name: "保存题录" })).toBeInTheDocument();
    await act(async () => fireEvent.click(confirm));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Retry candidate" })).toBeInTheDocument();
    expect(readLibrary(datasetId).papers).toHaveLength(4);
  });
});

describe("paper library table and drag to folders", () => {
  it("shows publication columns without an unavailable citation count", async () => {
    const data = readLibrary(datasetId);
    data.papers[0].citationCount = 1250;
    data.papers[1].citationCount = 0;
    writeLibrary(datasetId, data);
    await renderLibrary();

    for (const name of ["标题", "年份", "会议／期刊"]) {
      expect(screen.getByRole("columnheader", { name })).toBeInTheDocument();
    }
    const firstRow = within(screen.getByRole("heading", { name: "Folder paper" }).closest('[role="row"], tr') as HTMLElement);
    expect(firstRow.getByRole("cell", { name: "2026" })).toBeInTheDocument();
    expect(firstRow.getByRole("cell", { name: "Research Journal" })).toBeInTheDocument();
    expect(screen.queryByRole("columnheader", { name: "引用量" })).not.toBeInTheDocument();
  });

  it("moves a dragged paper into a folder without changing its rich content", async () => {
    const before = readLibrary(datasetId);
    await renderLibrary();
    const handle = screen.getByRole("button", { name: "拖动 Folder paper" });
    expect(handle).toHaveAttribute("draggable", "true");
    await dragPaperToFolder("Folder paper", "第二个文件夹");

    const saved = readLibrary(datasetId);
    expect(saved.papers.find((paper) => paper.id === "a")?.folderId).toBe("second");
    expect(saved.papers.find((paper) => paper.id === "a")).toEqual({ ...before.papers[0], folderId: "second" });
    expect(saved.notes).toEqual(before.notes);
    expect(saved.annotations).toEqual(before.annotations);
    expect(saved.attachments).toEqual(before.attachments);
    fireEvent.click(folderTitle(/第二个文件夹/));
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    fireEvent.click(folderTitle(/第一个文件夹/));
    expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument();
  });

  it("moves all selected papers when dragging one selected paper", async () => {
    await renderLibrary();
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Folder paper" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Unfiled paper" }));
    await dragPaperToFolder("Folder paper", "第二个文件夹");

    const saved = readLibrary(datasetId);
    expect(saved.papers.find((paper) => paper.id === "a")?.folderId).toBe("second");
    expect(saved.papers.find((paper) => paper.id === "c")?.folderId).toBe("second");
    expect(saved.papers.find((paper) => paper.id === "b")?.folderId).toBe("second");
    expect(saved.papers).toHaveLength(3);
  });

  it("moves only the dragged paper when it is outside the current selection", async () => {
    await renderLibrary();
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Unfiled paper" }));
    await dragPaperToFolder("Folder paper", "第二个文件夹");
    const saved = readLibrary(datasetId);
    expect(saved.papers.find((paper) => paper.id === "a")?.folderId).toBe("second");
    expect(saved.papers.find((paper) => paper.id === "c")?.folderId).toBeNull();
  });

  it("ignores external files and text dropped on a folder", async () => {
    const before = readLibrary(datasetId);
    await renderLibrary();
    const transfer = dragTransfer();
    transfer.setData("text/plain", "a");
    transfer.setData("application/json", JSON.stringify({ datasetId, ids: ["a"] }));
    const target = folderTitle(/第二个文件夹/);
    fireEvent.dragOver(target, { dataTransfer: transfer });
    fireEvent.drop(target, { dataTransfer: transfer });
    expect(readLibrary(datasetId)).toEqual(before);
  });

  it("rejects a valid-looking paper payload without an active internal drag", async () => {
    const before = readLibrary(datasetId);
    await renderLibrary();
    const transfer = dragTransfer();
    transfer.setData("application/x-lazymind-papers", JSON.stringify({ datasetId, ids: ["a"] }));
    const target = folderTitle(/第二个文件夹/);
    fireEvent.dragOver(target, { dataTransfer: transfer });
    fireEvent.drop(target, { dataTransfer: transfer });
    expect(readLibrary(datasetId)).toEqual(before);
  });

  it("ignores a malformed paper drag payload", async () => {
    const before = readLibrary(datasetId);
    await renderLibrary();
    const transfer = dragTransfer();
    const handle = screen.getByRole("button", { name: "拖动 Folder paper" });
    fireEvent.dragStart(handle, { dataTransfer: transfer });
    expect(transfer.types.length).toBeGreaterThan(0);
    for (const type of transfer.types) transfer.setData(type, "{malformed");
    const target = folderTitle(/第二个文件夹/);
    fireEvent.dragOver(target, { dataTransfer: transfer });
    fireEvent.drop(target, { dataTransfer: transfer });
    fireEvent.dragEnd(handle, { dataTransfer: transfer });
    expect(readLibrary(datasetId)).toEqual(before);
  });

  it("rejects papers added to an active drag payload that were not dragged", async () => {
    const before = readLibrary(datasetId);
    await renderLibrary();
    const transfer = dragTransfer();
    const handle = screen.getByRole("button", { name: "拖动 Folder paper" });
    fireEvent.dragStart(handle, { dataTransfer: transfer });
    transfer.setData("application/x-lazymind-papers", JSON.stringify({ datasetId, ids: ["a", "c"] }));
    const target = folderTitle(/第二个文件夹/);
    fireEvent.dragOver(target, { dataTransfer: transfer });
    fireEvent.drop(target, { dataTransfer: transfer });
    fireEvent.dragEnd(handle, { dataTransfer: transfer });
    expect(readLibrary(datasetId)).toEqual(before);
  });

  it("keeps a paper in its original folder and reports persistence failure", async () => {
    const before = readLibrary(datasetId);
    await renderLibrary();
    fireEvent.click(folderTitle(/第一个文件夹/));
    vi.mocked(savePersistedPaperLibrary).mockRejectedValueOnce(new Error("后端暂时无法保存"));
    await dragPaperToFolder("Folder paper", "第二个文件夹");

    expect(await screen.findByRole("alert")).toHaveTextContent("后端暂时无法保存");
    expect(readLibrary(datasetId)).toEqual(before);
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    fireEvent.click(folderTitle(/第二个文件夹/));
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
    const folder = folderTitle(new RegExp(folderName));
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
    fireEvent.click(folderTitle(/第一个文件夹/));
    await beginPointerDrag("Folder paper", "第二个文件夹");
    await releasePointer();

    await waitFor(() => expect(readLibrary(datasetId).papers.find((paper) => paper.id === "a")?.folderId).toBe("second"));
    expect(screen.queryByRole("heading", { name: "Folder paper" })).not.toBeInTheDocument();
  });

  it("moves the selection when a selected paper is dragged with the pointer", async () => {
    await renderLibrary();
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Folder paper" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "选择 Unfiled paper" }));
    await beginPointerDrag("Folder paper", "第二个文件夹");
    await releasePointer();

    await waitFor(() => {
      const saved = readLibrary(datasetId);
      expect(saved.papers.find((paper) => paper.id === "a")?.folderId).toBe("second");
      expect(saved.papers.find((paper) => paper.id === "c")?.folderId).toBe("second");
    });
    expect(screen.getByRole("checkbox", { name: "选择 Folder paper" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "选择 Unfiled paper" })).not.toBeChecked();
  });

  it("does not save when dropping into the paper's existing folder", async () => {
    const before = readLibrary(datasetId);
    await renderLibrary();
    const save = vi.spyOn(localStorage, "setItem");
    await beginPointerDrag("Folder paper", "第一个文件夹");
    await releasePointer();

    expect(save).not.toHaveBeenCalled();
    expect(readLibrary(datasetId)).toEqual(before);
  });

  it("cancels a pointer drag without saving or moving the paper", async () => {
    const before = readLibrary(datasetId);
    await renderLibrary();
    const save = vi.spyOn(localStorage, "setItem");
    await beginPointerDrag("Folder paper", "第二个文件夹");
    await act(async () => {
      fireEvent.keyDown(document, { key: "Escape", code: "Escape" });
    });
    await releasePointer();

    expect(save).not.toHaveBeenCalled();
    expect(readLibrary(datasetId)).toEqual(before);
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
  });
});

describe("paper library backend arXiv import", () => {
  const result = {
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
    lookupFixture.mockResolvedValueOnce(candidate(version));
    const dialog = await openImportDialog("arXiv / DOI 链接", "通过链接导入");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: `https://arxiv.org/pdf/2404.06654${version}` } });
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    await within(dialog).findByText(result.paper.title);
    return dialog;
  }

  it("previews arXiv metadata without saving a placeholder or using legacy import confirmation", async () => {
    const before = readLibrary(datasetId);
    const success = vi.spyOn(message, "success");
    await renderLibrary();
    const dialog = await lookupArxivCandidate();
    expect(within(dialog).getByRole("button", { name: "导入文献与 PDF" })).toBeEnabled();
    expect(within(dialog).queryByRole("button", { name: "加入论文库" })).not.toBeInTheDocument();
    expect(readLibrary(datasetId)).toEqual(before);
    expect(success).not.toHaveBeenCalled();
  });

  it('waits for queue acknowledgement and keeps the PDF pending until document synchronization', async () => {
    let finish!: (value: { batch_id: string }) => void;
    vi.mocked(importPaperLink).mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    await renderLibrary();
    const dialog = await lookupArxivCandidate();
    fireEvent.click(within(dialog).getByRole('button', { name: '导入文献与 PDF' }));
    expect(importPaperLink).toHaveBeenCalledWith(datasetId, expect.objectContaining({ fulltext_candidates: [expect.objectContaining({ version: 'v3' })] }), expect.any(String));
    expect(readLibrary(datasetId).papers).toHaveLength(3);
    await act(async () => finish({ batch_id: 'link-batch' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    const saved = readLibrary(datasetId);
    expect(saved.papers.find(paper => paper.arxiv === '2404.06654')).toMatchObject({ title: result.paper.title, parse: 'parsing' });
    expect(saved.attachments.some(attachment => attachment.contentRef === result.document_id)).toBe(false);
  });

  it('preserves the candidate and reuses the request key when queue submission fails', async () => {
    vi.mocked(importPaperLink).mockRejectedValueOnce(new Error('导入服务暂不可用')).mockResolvedValueOnce({ batch_id: 'retried' });
    await renderLibrary();
    const dialog = await lookupArxivCandidate();
    fireEvent.click(within(dialog).getByRole('button', { name: '导入文献与 PDF' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('导入服务暂不可用');
    expect(readLibrary(datasetId).papers).toHaveLength(3);
    fireEvent.click(within(dialog).getByRole('button', { name: '导入文献与 PDF' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(vi.mocked(importPaperLink).mock.calls[0][2]).toBe(vi.mocked(importPaperLink).mock.calls[1][2]);
  });

  it('does not duplicate an existing paper or discard its old PDF and notes when importing a new version', async () => {
    const before = readLibrary(datasetId);
    before.papers[0] = { ...before.papers[0], arxiv: '2404.06654', arxivVersion: 'v1' };
    writeLibrary(datasetId, before);
    await renderLibrary();
    const dialog = await lookupArxivCandidate();
    fireEvent.click(within(dialog).getByRole('button', { name: '导入文献与 PDF' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    const saved = readLibrary(datasetId);
    expect(saved.papers).toHaveLength(3);
    expect(saved.papers[0]).toMatchObject({ id: 'a', noteIds: ['note-a'], arxivVersion: 'v1' });
    expect(saved.notes).toEqual(before.notes);
    expect(saved.attachments).toEqual(before.attachments);
    expect(importPaperLink).toHaveBeenCalledOnce();
  });

  function backendSource(status = 'ready') {
    const data = libraryFixture({ papers: [{ ...makePaper('server-paper', result.paper.title, null), academicWorkId: result.work_id, arxiv: result.paper.base_id, parse: status === 'failed' ? 'failed' : 'ready', processingError: status === 'failed' ? '解析失败' : undefined }] });
    data.attachments[0].contentRef = result.document_id;
    data.attachments[0].paperId = "server-paper";
    data.papers[0].attachmentIds = [data.attachments[0].id];
    return { data, revision: 0, canWrite: true, paperImportance: {}, active: false,
      states: [{ state: { document_id: result.document_id, kind: 'paper' as const, status: 'succeeded', paper_id: result.work_id, manual: false },
        paper: { paper_id: result.work_id, title: result.paper.title, authors: result.paper.authors, arxiv_id: result.paper.base_id, abstract: result.paper.abstract },
        source_provider: 'arxiv', source_version: 'v3', content_sha256: result.content_sha256,
        processing_status: status, processing_error: status === 'failed' ? '解析失败' : undefined, is_important: false }] };
  }

  it('hydrates arXiv documents and authoritative errors without saving derived state', async () => {
    vi.mocked(loadPaperLibrarySources).mockResolvedValue(backendSource('failed'));
    await renderLibrary();
    expect(await screen.findByRole('heading', { name: result.paper.title })).toBeInTheDocument();
    const row = within(screen.getByRole('heading', { name: result.paper.title }).closest('tr')!);
    expect(row.getByRole('link', { name: /阅读/ })).toHaveAttribute('href', `/lib/knowledge/knowledge/${datasetId}/${result.document_id}`);
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
  });

  it('retains existing rows on synchronization failure and recovers through the shared loader', async () => {
    vi.mocked(loadPaperLibrarySources).mockRejectedValueOnce(new Error('附件同步失败')).mockResolvedValue(backendSource());
    await renderLibrary();
    expect(await screen.findByRole('alert')).toHaveTextContent('附件同步失败');
    expect(screen.getByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重新同步' }));
    expect(await screen.findByRole('heading', { name: result.paper.title })).toBeInTheDocument();
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
  });

  it.each([false, true])('ignores queue acknowledgements after leaving the collection (return: %s)', async returnToOriginal => {
    let finish!: (value: { batch_id: string }) => void;
    vi.mocked(importPaperLink).mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    const other = libraryFixture({ folders: [], papers: [makePaper('other', 'Other library paper', null)] });
    writeLibrary('other-dataset', other);
    const view = await renderLibrary();
    const dialog = await lookupArxivCandidate();
    fireEvent.click(within(dialog).getByRole('button', { name: '导入文献与 PDF' }));
    await act(async () => view.rerender(<PaperLibrary datasetId="other-dataset" />));
    if (returnToOriginal) await act(async () => view.rerender(<PaperLibrary datasetId={datasetId} />));
    await act(async () => finish({ batch_id: 'late' }));
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
    expect(readLibrary('other-dataset')).toEqual(other);
  });

  it('allows readers to view synchronized documents without writing shared snapshots', async () => {
    vi.mocked(loadPersistedPaperLibrary).mockResolvedValueOnce({ data: libraryFixture({ folders: [], papers: [] }), revision: 0, canWrite: false });
    vi.mocked(loadPaperLibrarySources).mockResolvedValue({ ...backendSource(), canWrite: false });
    await renderLibrary();
    expect(await screen.findByRole('heading', { name: result.paper.title })).toBeInTheDocument();
    expect(browse().getByRole('button', { name: /导入/ })).toBeDisabled();
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
  });

});


describe("backend durable paper library", () => {
  it('keeps the current rows visible while refreshing the saved library in the background', async () => {
    await renderLibrary();
    const data = readLibrary(datasetId);
    let finish!: (value: Awaited<ReturnType<typeof loadPersistedPaperLibrary>>) => void;
    vi.mocked(loadPersistedPaperLibrary).mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
    await act(async () => fireEvent.focus(window));
    expect(screen.getByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
    expect(screen.getByText('正在刷新…')).toBeVisible();
    expect(screen.queryByText('正在加载文献…')).not.toBeInTheDocument();
    expect(screen.getByLabelText('论文列表，可横向滚动')).toHaveAttribute('aria-busy', 'true');
    await act(async () => finish({ data, revision: 1, canWrite: true }));
    await waitFor(() => expect(screen.getByLabelText('论文列表，可横向滚动')).toHaveAttribute('aria-busy', 'false'));
    expect(screen.getByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
  });
  it("keeps polling a retry whose failed items are still waiting for the worker", async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      vi.mocked(listAcademicImportBatches).mockResolvedValue({ items: [], next_cursor: '', notification: { active: 1, needs_action: 0 } });
      await renderLibrary();
      expect(screen.getByRole('button', { name: '论文导入通知：1 项进行中 · 0 项待处理' })).toBeInTheDocument();
      const requests = vi.mocked(listAcademicImportBatches).mock.calls.length;
      vi.mocked(listAcademicImportBatches).mockResolvedValue({ items: [], next_cursor: '', notification: { active: 0, needs_action: 0 } });
      await act(async () => { await vi.advanceTimersByTimeAsync(12000); });
      expect(vi.mocked(listAcademicImportBatches).mock.calls.length).toBeGreaterThan(requests);
      expect(screen.getByRole('button', { name: '论文导入通知：0 项进行中 · 0 项待处理' })).toBeInTheDocument();
      const completedRequests = vi.mocked(listAcademicImportBatches).mock.calls.length;
      await act(async () => { await vi.advanceTimersByTimeAsync(24000); });
      expect(vi.mocked(listAcademicImportBatches).mock.calls.length).toBe(completedRequests);
    } finally { vi.useRealTimers(); }
  });

  it("protects an edit draft while saving and restores editing after a failed write", async () => {
    let fail!: (error: Error) => void;
    vi.mocked(updatePaperIdentity).mockImplementationOnce(() => new Promise((_, reject) => { fail = reject; }));
    await renderLibrary();
    fireEvent.mouseEnter(screen.getByRole("button", { name: "Folder paper 的更多操作" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "查看详情" }));
    const dialog = await screen.findByRole("dialog", { name: "文献详情" });
    fireEvent.click(within(dialog).getByRole("button", { name: /编\s*辑/ }));
    fireEvent.change(within(dialog).getByLabelText("题名"), { target: { value: "Keep this draft" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "保存题录" }));
    await waitFor(() => expect(updatePaperIdentity).toHaveBeenCalledOnce());
    for (const input of within(dialog).getAllByRole("textbox")) expect(input).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "关闭" })).toBeDisabled();
    fireEvent.click(dialog.parentElement!);
    expect(dialog).toBeInTheDocument();
    await act(async () => fail(new Error("服务器保存失败")));
    expect(within(dialog).getByLabelText("题名")).toHaveValue("Keep this draft");
    expect(within(dialog).getByLabelText("题名")).toBeEnabled();
    expect(within(dialog).getByRole("button", { name: "关闭" })).toBeEnabled();
    expect(within(dialog).getByRole("alert")).toHaveTextContent("服务器保存失败");
  });

  it("retains a stale edit draft without overwriting another member's changes after refresh", async () => {
    await renderLibrary();
    fireEvent.mouseEnter(screen.getByRole("button", { name: "Folder paper 的更多操作" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "查看详情" }));
    const dialog = await screen.findByRole("dialog", { name: "文献详情" });
    fireEvent.click(within(dialog).getByRole("button", { name: /编\s*辑/ }));
    fireEvent.change(within(dialog).getByLabelText("题名"), { target: { value: "My unsaved title" } });
    const shared = readLibrary(datasetId);
    shared.papers[0].tags = ["Shared tag"];
    mockReload({ data: shared, revision: 8, canWrite: true });
    await act(async () => fireEvent.focus(window));
    await act(async () => fireEvent.click(within(dialog).getByRole("button", { name: "保存题录" })));
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
    expect(within(dialog).getByRole("alert")).toHaveTextContent("重新打开编辑");
    expect(within(dialog).getByLabelText("题名")).toHaveValue("My unsaved title");
    expect(screen.getAllByRole("heading", { name: "Folder paper" })).toHaveLength(2);
    expect(screen.getAllByText("Shared tag")[0]).toBeInTheDocument();
  });

  it("honors reader access and never writes an arXiv synchronization snapshot", async () => {
    mockReload({ data: readLibrary(datasetId), revision: 3, canWrite: false });
    await renderLibrary();
    expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
    expect(browse().getByRole("button", { name: /导入/ })).toBeDisabled();
    expect(browse().getByRole("button", { name: "新建文件夹" })).toBeDisabled();
    expect(within(screen.getByRole("heading", { name: "Folder paper" }).parentElement!).getByRole("button", { name: /取消收藏|取消重点|paperTasks.unmarkImportant/ })).toBeEnabled();
    expect(screen.getByRole("alert")).toHaveTextContent("只读");
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
  });

  it("retains folders added by another member while a bibliography import is being confirmed", async () => {
    lookupFixture.mockResolvedValueOnce(makePaper("new", "New import", null));
    await renderLibrary();
    const dialog = await openImportDialog("arXiv / DOI 链接", "通过链接导入");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "10.1000/example" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    await within(dialog).findByRole("button", { name: "保存题录" });
    const shared = readLibrary(datasetId);
    shared.folders.push({ id: "remote-folder", name: "Shared folder" });
    mockReload({ data: shared, revision: 9, canWrite: true });
    await act(async () => fireEvent.focus(window));
    await act(async () => fireEvent.click(within(dialog).getByRole("button", { name: "保存题录" })));
    expect(readLibrary(datasetId).folders).toContainEqual({ id: "remote-folder", name: "Shared folder" });
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
      finish = () => { writeLibrary(id, data); resolve({ data, revision: revision + 1, canWrite: true }); };
    }));
    lookupFixture.mockResolvedValueOnce(makePaper("new", "Awaiting backend", null));
    await renderLibrary();
    const dialog = await openImportDialog("arXiv / DOI 链接", "通过链接导入");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "10.1000/example" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "查找文献" }));
    const addButton = await within(dialog).findByRole("button", { name: "保存题录" });
    await act(async () => fireEvent.click(addButton));
    expect(within(dialog).getByRole("button", { name: /正在提交/ })).toBeDisabled();
    expect(readLibrary(datasetId).papers.some(paper => paper.title === "Awaiting backend")).toBe(false);
    expect(readLibrary(datasetId).papers).toHaveLength(3);
    expect(success).not.toHaveBeenCalled();
    await act(async () => finish());
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Awaiting backend" })).toBeInTheDocument();
    expect(success).toHaveBeenCalledOnce();
    expect(readLibrary(datasetId).papers.find((item) => item.id === "link-new")?.attachmentIds).toEqual([]);
  });

  it("keeps the personal mark and shared metadata when a star update fails", async () => {
    const before = readLibrary(datasetId);
    vi.mocked(setLibraryPaperImportance).mockRejectedValueOnce(new Error('temporarily unavailable'));
    const error = vi.spyOn(message, 'error');
    await renderLibrary();
    const star = within(screen.getByRole('heading', { name: 'Folder paper' }).parentElement!).getByRole('button', { name: /取消重点|paperTasks.unmarkImportant/ });
    await act(async () => fireEvent.click(star));
    await waitFor(() => expect(error).toHaveBeenCalled());
    expect(personalMarks.get('a')).toBe(true);
    expect(star).toHaveAttribute('aria-pressed', 'true');
    expect(readLibrary(datasetId)).toEqual(before);
    expect(savePersistedPaperLibrary).not.toHaveBeenCalled();
  });
});
