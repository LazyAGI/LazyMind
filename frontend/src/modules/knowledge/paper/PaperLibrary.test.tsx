import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConfigProvider, Modal, message } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PaperLibrary from './PaperLibrary';
import PaperTasks from '../components/PaperTasks';
import { MemoryRouter } from 'react-router-dom';
import { libraryFixture, paperFixture } from './__tests__/fixtures';
import { emptyLibrary, type PaperLibraryData } from './libraryModel';
import type { PaperRecord } from './model';
import * as paperModel from './model';
import { loadPersistedPaperLibrary, previewLibraryPapers, patchLibraryPaper, confirmLibraryPdf, createLibraryFolder } from './store';
import { paperFromPdf } from './pdfMeta';
import { setPaperImportance } from '../api/paperWorkflows';
import { listAcademicImportBatches } from '../api/academicReferences';
vi.mock('@/runtime/readiness', () => ({ waitForRuntimeCapability: vi.fn().mockResolvedValue(undefined) }));
vi.mock('../components/PaperTasks', () => ({ default: vi.fn(() => null) }));
vi.mock('./pdfMeta', () => ({ paperFromPdf: vi.fn() }));
vi.mock('../utils/fileHash', () => ({ computeFileSha256: vi.fn().mockResolvedValue('hash') }));
vi.mock('../api/paperWorkflows', async (original) => ({ ...await original<typeof import('../api/paperWorkflows')>(), setPaperImportance: vi.fn() }));
vi.mock('../api/academicReferences', async (original) => ({ ...await original<typeof import('../api/academicReferences')>(), listAcademicImportBatches: vi.fn() }));
vi.mock('./store', async (original) => ({ ...await original<typeof import('./store')>(), loadPersistedPaperLibrary: vi.fn(), previewLibraryPapers: vi.fn(), patchLibraryPaper: vi.fn(), confirmLibraryPdf: vi.fn(), createLibraryFolder: vi.fn(), moveLibraryPapers: vi.fn(), removeLibraryPapers: vi.fn(), deleteLibraryFolder: vi.fn(), importLibraryPapers: vi.fn() }));
const datasetId = 'library';
vi.mock('./PaperAttachmentPreview', () => ({ default: ({ attachment }: { attachment: { name: string } }) => <div aria-label="附件预览">{attachment.name}</div> }));
const libraries = new Map<string, PaperLibraryData>();
function clone<T>(value: T): T { return JSON.parse(JSON.stringify(value)); }
const readLibrary = (id: string) => clone(libraries.get(id) || emptyLibrary());
const writeLibrary = (id: string, data: PaperLibraryData) => libraries.set(id, clone(data));
const makePaper = (id: string, title: string, folderId: string | null, important = false) => paperFixture({ id, folderId, important, metadata: { title } });
function seedLibrary() { const data = libraryFixture({ folders: [{ id: 'first', name: '第一个文件夹', revision: 1 }, { id: 'second', name: '第二个文件夹', revision: 1 }], papers: [makePaper('a', 'Folder paper', 'first', true), makePaper('b', 'Other favorite', 'second', true), makePaper('c', 'Unfiled paper', null)] }); data.attachments[0].contentRef = 'stored-pdf-a'; data.notes.push({ id: 'note-a', paperId: 'a', title: 'Reading notes', content: 'Keep these notes', kind: 'note', createdAt: '', updatedAt: '' }); data.papers[0].noteIds = ['note-a']; writeLibrary(datasetId, data); return data; }
const snapshot = (data: PaperLibraryData, canWrite = true) => ({ data: { ...data, attachments: data.attachments.map(attachment => attachment.contentRef ? { ...attachment, academicState: attachment.academicState || { document_id: attachment.contentRef, kind: 'paper' as const, status: 'succeeded', paper_id: data.papers.find(paper => paper.id === attachment.paperId)!.academicWorkId!, manual: true } } : attachment) }, canWrite, paperCount: data.papers.length, pdfAttachmentCount: data.attachments.filter(a => a.contentRef).length, active: false });
function mockReload(input: {
    data: PaperLibraryData;
    canWrite: boolean;
}) { vi.mocked(loadPersistedPaperLibrary).mockResolvedValueOnce(snapshot(input.data, input.canWrite)); }
function changes(dataset: string, data: PaperLibraryData, ids: string[]) { writeLibrary(dataset, data); return { data: { ...emptyLibrary(), papers: data.papers.filter(p => ids.includes(p.id)), attachments: data.attachments.filter(a => ids.includes(a.paperId)), notes: data.notes.filter(n => ids.includes(n.paperId)) } }; }
function browse() { return within(screen.getByRole('navigation', { name: '文献浏览' })); }
function libraryTools() { return within(screen.getByRole('toolbar', { name: '文献工具' })); }
async function renderLibrary(props: Partial<React.ComponentProps<typeof PaperLibrary>> = {}) { let result!: ReturnType<typeof render>; await act(async () => { result = render(<ConfigProvider theme={{ token: { motion: false } }}><PaperLibrary datasetId={datasetId} {...props}/></ConfigProvider>); }); return result; }
async function openImportDialog(method: string, title: string) { fireEvent.click(browse().getByRole('button', { name: /导入/ })); fireEvent.click(await screen.findByRole('menuitem', { name: new RegExp(method) })); return screen.findByRole('dialog', { name: title }); }
async function confirmPdfOwnership(dialog: HTMLElement) {
    const checkbox = within(dialog).queryByRole('checkbox', { name: /我已核对/ });
    if (checkbox && !(checkbox as HTMLInputElement).checked)
        fireEvent.click(checkbox);
}
beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(PaperTasks).mockImplementation(() => <></>);
    libraries.clear();
    seedLibrary();
    vi.mocked(previewLibraryPapers).mockImplementation(async (_id, papers) => papers);
    ConfigProvider.config({ holderRender: (children: React.ReactNode) => <ConfigProvider theme={{ token: { motion: false } }}>{children}</ConfigProvider> });
    vi.mocked(listAcademicImportBatches).mockResolvedValue({ items: [], next_cursor: '', notification: { active: 0, needs_action: 0 } });
    vi.mocked(loadPersistedPaperLibrary).mockImplementation(async (id) => snapshot(readLibrary(id)));
    vi.mocked(setPaperImportance).mockResolvedValue(undefined);
    vi.mocked(patchLibraryPaper).mockImplementation(async (id, paper, patch) => { const data = readLibrary(id); const index = data.papers.findIndex(p => p.id === paper.id); data.papers[index] = { ...data.papers[index], ...patch, revision: paper.revision + 1 }; return changes(id, data, [paper.id]); });
    vi.mocked(createLibraryFolder).mockImplementation(async (id, key, name, parentId) => { const data = readLibrary(id); const folder = { id: key, name, parentId, revision: 1 }; data.folders.push(folder); writeLibrary(id, data); return { data: { ...emptyLibrary(), folders: [folder] } }; });
    vi.mocked(confirmLibraryPdf).mockImplementation(async (id, paper, doc) => {
        const data = readLibrary(id);
        if (!data.papers.some(p => p.id === paper.id))
            data.papers.push({ ...paper, revision: 1 });
        const target = data.papers.find(p => p.id === paper.id)!;
        const attachment = { id: 'uploaded-' + doc, paperId: target.id, name: paper.metadata.title + '.pdf', kind: 'pdf' as const, contentRef: doc, source: paper.source, addedAt: '', isPrimary: !target.attachmentIds.length };
        target.attachmentIds.push(attachment.id);
        target.revision++;
        data.attachments.push(attachment);
        return changes(id, data, [target.id]);
    });
});
afterEach(async () => { cleanup(); await act(async () => { Modal.destroyAll(); message.destroy(); }); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
function captureDownload() {
    const browserUrl = URL;
    const createObjectURL = vi.fn<(object: Blob | MediaSource) => string>(() => "blob:paper-export");
    vi.stubGlobal("URL", class extends browserUrl {
        static createObjectURL = createObjectURL;
        static revokeObjectURL = vi.fn();
    });
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => { });
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
function folderTitle(name: RegExp): HTMLElement {
    return browse().getByText(name, { selector: '.paper-directory-node-name' }).closest('.paper-directory-folder-title') as HTMLElement;
}
async function closePopover(name: string) {
    fireEvent.mouseDown(document.body);
    fireEvent.click(document.body);
    await waitFor(() => expect(screen.queryByRole("dialog", { name })).not.toBeInTheDocument());
}
describe('paper library behavior', () => {
    it('opens directory papers as full pages and list papers as drawers', async () => {
        await renderLibrary();
        const tree = browse().getByRole('tree', { name: '论文目录' });
        fireEvent.click(within(tree).getByText('Folder paper', { selector: '.paper-directory-node-name' }));
        const detail = await screen.findByRole('region', { name: '文献详情' });
        expect(within(detail).getByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
        expect(screen.queryByRole('dialog', { name: '文献详情' })).not.toBeInTheDocument();
        expect(screen.queryByRole('table', { name: '论文列表' })).not.toBeInTheDocument();
        fireEvent.click(within(detail).getByRole('button', { name: /返回目录/ }));
        expect(screen.getByRole('table', { name: '论文列表' })).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Folder paper' }));
        expect(await screen.findByRole('dialog', { name: '文献详情' })).toBeInTheDocument();
    });
    it('keeps real task controls available after saving a new DOI identity without another read', async () => {
        const { default: ActualTasks } = await vi.importActual<typeof import('../components/PaperTasks')>('../components/PaperTasks');
        vi.mocked(PaperTasks).mockImplementation(props => <MemoryRouter><ActualTasks {...props}/></MemoryRouter>);
        vi.mocked(patchLibraryPaper).mockImplementationOnce(async (id, paper, patch) => {
            const data = snapshot(readLibrary(id)).data;
            const index = data.papers.findIndex(item => item.id === paper.id);
            data.papers[index] = { ...data.papers[index], ...patch, academicWorkId: 'doi-work', revision: paper.revision + 1 };
            for (const attachment of data.attachments.filter(item => item.paperId === paper.id && item.academicState))
                attachment.academicState = { ...attachment.academicState!, paper_id: 'doi-work', manual: true, revision: 2 };
            return changes(id, data, [paper.id]);
        });
        await renderLibrary();
        fireEvent.click(screen.getByRole('checkbox', { name: '选择 Folder paper' }));
        expect(screen.getByRole('button', { name: '阅读心得' })).toBeEnabled();
        fireEvent.mouseEnter(screen.getByRole('button', { name: 'Folder paper 的更多操作' }));
        fireEvent.click(await screen.findByRole('menuitem', { name: '查看详情' }));
        const drawer = await screen.findByRole('dialog', { name: '文献详情' });
        fireEvent.click(within(drawer).getByRole('button', { name: '编辑题录' }));
        fireEvent.change(within(drawer).getByLabelText('DOI'), { target: { value: '10.1234/corrected' } });
        fireEvent.click(within(drawer).getByRole('button', { name: '保存题录' }));
        await waitFor(() => expect(within(drawer).queryByRole('button', { name: '保存题录' })).not.toBeInTheDocument());
        fireEvent.click(within(drawer).getByRole('button', { name: '关闭' }));
        expect(screen.getByRole('button', { name: '阅读心得' })).toBeEnabled();
        expect(vi.mocked(PaperTasks).mock.lastCall?.[0].selected).toEqual(['stored-pdf-a']);
        expect(loadPersistedPaperLibrary).toHaveBeenCalledOnce();
    });
    it('reads a complete library once per initial load, active poll and focus refresh', async () => {
        vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
        try {
            const data = readLibrary(datasetId);
            const first = data.attachments[0];
            data.attachments = Array.from({ length: 201 }, (_, index) => ({ ...first, id: 'attachment-' + index, contentRef: 'pdf-' + index }));
            data.papers[0].attachmentIds = data.attachments.map(item => item.id);
            writeLibrary(datasetId, data);
            vi.mocked(loadPersistedPaperLibrary).mockImplementation(async id => ({ ...snapshot(readLibrary(id)), active: true }));
            await renderLibrary();
            expect(loadPersistedPaperLibrary).toHaveBeenCalledOnce();
            expect(listAcademicImportBatches).toHaveBeenCalledOnce();
            await act(async () => { await vi.advanceTimersByTimeAsync(12000); });
            expect(loadPersistedPaperLibrary).toHaveBeenCalledTimes(2);
            expect(listAcademicImportBatches).toHaveBeenCalledTimes(2);
            await act(async () => { fireEvent.focus(window); });
            expect(loadPersistedPaperLibrary).toHaveBeenCalledTimes(3);
        } finally {
            vi.useRealTimers();
        }
    });
    it('shows the library when the independent import notification request fails', async () => {
        vi.mocked(listAcademicImportBatches).mockRejectedValueOnce(new Error('通知暂时不可用'));
        await renderLibrary();
        expect(screen.getByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
        expect(screen.getByRole('alert')).toHaveTextContent('通知暂时不可用');
        expect(loadPersistedPaperLibrary).toHaveBeenCalledOnce();
    });
    it('defers a focus refresh until the active write finishes', async () => {
        let finish!: (value: Awaited<ReturnType<typeof patchLibraryPaper>>) => void;
        vi.mocked(patchLibraryPaper).mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
        await renderLibrary();
        const row = screen.getByRole('combobox', { name: 'Folder paper 的阅读状态' });
        fireEvent.mouseDown(row);
        fireEvent.click(await screen.findByText('已读', { selector: '.ant-select-item-option-content' }));
        await waitFor(() => expect(patchLibraryPaper).toHaveBeenCalledOnce());
        await act(async () => { fireEvent.focus(window); });
        expect(loadPersistedPaperLibrary).toHaveBeenCalledOnce();
        const data = readLibrary(datasetId);
        data.papers[0].status = 'read';
        data.papers[0].revision++;
        await act(async () => { finish(changes(datasetId, data, ['a'])); });
        await waitFor(() => expect(loadPersistedPaperLibrary).toHaveBeenCalledTimes(2));
        expect(screen.getByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
    });

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
        data.papers[2].metadata.identifiers.doi = '10.1000/target';
        writeLibrary(datasetId, data);
        vi.mocked(paperFromPdf).mockResolvedValueOnce({ ...makePaper('wrong', 'Another paper', null), identityVerified: true, metadata: {
                ...makePaper('wrong', 'Another paper', null).metadata,
                identifiers: { doi: '10.1000/other' }
            } });
        const upload = vi.fn();
        await renderLibrary({ onUploadPdf: upload });
        const row = within(screen.getByRole('heading', { name: 'Unfiled paper' }).closest('tr')!);
        expect(row.queryByRole('button', { name: /阅读|添加 PDF/ })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Unfiled paper' }));
        const detail = within(screen.getByRole('dialog', { name: '文献详情' }));
        expect(detail.getByRole('button', { name: /阅读/ })).toBeDisabled();
        expect(detail.queryByRole('button', { name: '添加 PDF' })).not.toBeInTheDocument();
        fireEvent.click(detail.getByRole('button', { name: '附件' }));
        expect(detail.getByRole('tab', { name: '附件 1' })).toHaveAttribute('aria-selected', 'true');
        fireEvent.click(detail.getByRole('button', { name: '附件操作' }));
        fireEvent.click(await screen.findByRole('menuitem', { name: '添加 PDF' }));
        const dialog = await screen.findByRole('dialog', { name: '上传文献' });
        fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(['pdf'], 'wrong.pdf')] } });
        expect(await within(dialog).findByText('PDF 与当前论文身份不一致')).toBeInTheDocument();
        expect(within(dialog).getByRole('button', { name: '添加 PDF' })).toBeDisabled();
        expect(within(dialog).getByRole('combobox', { name: 'PDF 关联论文' })).toHaveAttribute('disabled');
        expect(upload).not.toHaveBeenCalled();
        expect(confirmLibraryPdf).not.toHaveBeenCalled();
    });
    it("opens paper details consistently and lets readers choose among PDF attachments", async () => {
        const open = vi.spyOn(window, 'open').mockReturnValue(null);
        const data = readLibrary(datasetId);
        data.attachments.push({ ...data.attachments[0], id: "second-pdf", contentRef: "second-doc", isPrimary: false, sourceVersion: 'v1' });
        data.papers[0].attachmentIds.push('second-pdf');
        writeLibrary(datasetId, data);
        await renderLibrary();
        const row = within(screen.getByRole('heading', { name: 'Folder paper' }).closest('tr')!);
        expect(row.queryByRole('button', { name: /阅读|添加 PDF/ })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Folder paper' }));
        const detail = within(screen.getByRole('dialog', { name: '文献详情' }));
        fireEvent.click(detail.getByRole('tab', { name: '附件 2' }));
        fireEvent.click(detail.getByRole('button', { name: '阅读 PDF' }));
        expect(open).toHaveBeenLastCalledWith(`/lib/knowledge/knowledge/${datasetId}/stored-pdf-a`, '_blank', 'noopener');
        fireEvent.change(detail.getByRole('combobox', { name: '选择预览附件与版本' }), { target: { value: 'second-pdf' } });
        expect(detail.getByLabelText('附件预览')).toHaveTextContent(data.attachments.find(item => item.id === 'second-pdf')!.name);
        fireEvent.click(detail.getByRole('button', { name: '阅读 ↗' }));
        expect(open).toHaveBeenLastCalledWith(`/lib/knowledge/knowledge/${datasetId}/second-doc`, '_blank', 'noopener');
        fireEvent.click(detail.getByRole('button', { name: '阅读 PDF' }));
        expect(open).toHaveBeenLastCalledWith(`/lib/knowledge/knowledge/${datasetId}/stored-pdf-a`, '_blank', 'noopener');
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
    it("places the star beside the title and updates favorites without opening the reader", async () => {
        const open = vi.spyOn(window, "open").mockReturnValue(null);
        await renderLibrary();
        const title = screen.getByRole("heading", { name: "Folder paper" });
        await act(async () => fireEvent.click(within(title.parentElement!).getByRole("button", { name: /取消收藏|取消重点|paperTasks.unmarkImportant/ })));
        expect(setPaperImportance).toHaveBeenCalledWith(datasetId, 'a', false);
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
        expect(browse().queryByRole("button", { name: "删除文件夹" })).not.toBeInTheDocument();
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
        if (format === "csv")
            expect(content).toContain("第一个文件夹");
    });
    it("groups search, filter, export and import notifications in the document toolbar", async () => {
        await renderLibrary();
        const tools = libraryTools();
        expect(tools.getAllByRole("button")).toHaveLength(4);
        for (const name of ["搜索", "筛选", "导出题录"])
            expect(tools.getByRole("button", { name })).toBeInTheDocument();
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
    it("creates a child in the folder whose row action was used", async () => {
        await renderLibrary();
        expect(browse().queryByRole("button", { name: "删除文件夹" })).not.toBeInTheDocument();
        fireEvent.click(folderTitle(/第一个文件夹/));
        fireEvent.click(browse().getByRole("button", { name: "在「第二个文件夹」中新建文件夹" }));
        const dialog = await screen.findByRole("dialog", { name: "新建文件夹" });
        fireEvent.change(within(dialog).getByRole("textbox", { name: "文件夹名称" }), { target: { value: "新主题" } });
        fireEvent.click(within(dialog).getByRole("button", { name: /创\s*建/ }));
        await waitFor(() => expect(folderTitle(/新主题/).closest(".ant-tree-treenode")).toHaveClass("ant-tree-treenode-selected"));
        expect(readLibrary(datasetId).folders.find((folder) => folder.name === "新主题")?.parentId).toBe('second');
        expect(browse().getByRole("button", { name: "删除文件夹「新主题」" })).toBeEnabled();
    });
    it("keeps the folder and its papers when deletion is cancelled", async () => {
        const before = readLibrary(datasetId);
        await renderLibrary();
        fireEvent.click(folderTitle(/第一个文件夹/));
        fireEvent.click(browse().getByRole("button", { name: "删除文件夹「第一个文件夹」" }));
        const dialog = await screen.findByRole("dialog");
        fireEvent.click(within(dialog).getByRole("button", { name: /取\s*消|Cancel/ }));
        await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
        expect(readLibrary(datasetId)).toEqual(before);
        expect(folderTitle(/第一个文件夹/).closest(".ant-tree-treenode")).toHaveClass("ant-tree-treenode-selected");
    });
    it.each(['before selection', 'during recognition'])("keeps the first selected PDF when focus refresh starts %s and matches the refreshed library", async (refreshTiming) => {
        let finishRecognition!: (paper: PaperRecord) => void;
        vi.mocked(paperFromPdf).mockImplementationOnce(() => new Promise(resolve => { finishRecognition = resolve; }));
        const upload = vi.fn().mockResolvedValue('first-choice-document');
        await renderLibrary({ onUploadPdf: upload });
        const dialog = await openImportDialog('直接上传', '上传文献');
        const input = dialog.querySelector<HTMLInputElement>('input[type="file"]')!;
        const file = new File(['pdf'], 'first-choice.pdf', { type: 'application/pdf' });
        const shared = readLibrary(datasetId);
        shared.papers[0].metadata.title = 'Updated shared paper';
        writeLibrary(datasetId, shared);
        const refreshed = snapshot(shared);
        let finishRefresh!: (snapshot: typeof refreshed) => void;
        vi.mocked(loadPersistedPaperLibrary).mockImplementationOnce(() => new Promise(resolve => { finishRefresh = resolve; }));
        if (refreshTiming === 'before selection') {
            await act(async () => fireEvent.focus(window));
            expect(input).toBeEnabled();
            fireEvent.change(input, { target: { files: [file] } });
        }
        else {
            fireEvent.change(input, { target: { files: [file] } });
            await act(async () => fireEvent.focus(window));
        }
        await waitFor(() => expect(paperFromPdf).toHaveBeenCalledOnce());
        await act(async () => finishRecognition({...makePaper('incoming', 'Updated shared paper', null),arxivVersion:'v2'}));
        expect(within(dialog).getByText('正在识别文献…')).toBeInTheDocument();
        expect(within(dialog).queryByRole('region', { name: '导入确认' })).not.toBeInTheDocument();
        expect(upload).not.toHaveBeenCalled();
        await act(async () => finishRefresh(refreshed));
        const confirm = await within(dialog).findByRole('button', { name: '添加 PDF' });
        await confirmPdfOwnership(dialog);
        fireEvent.click(confirm);
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        expect(upload).toHaveBeenCalledOnce();
        expect(confirmLibraryPdf).toHaveBeenLastCalledWith(datasetId, expect.objectContaining({ id: 'a' }), 'first-choice-document', expect.any(String), 'v2');
        expect(readLibrary(datasetId).papers).toHaveLength(3);
        expect(readLibrary(datasetId).papers[0].metadata.title).toBe('Updated shared paper');
        expect(readLibrary(datasetId).attachments.find(item => item.contentRef === 'first-choice-document')?.paperId).toBe('a');
    });
    it('retains the selected PDF when focus refresh fails and retries it without another file selection', async () => {
        vi.mocked(paperFromPdf).mockResolvedValue(makePaper('incoming', 'New paper', null));
        const upload = vi.fn();
        await renderLibrary({ onUploadPdf: upload });
        const dialog = await openImportDialog('直接上传', '上传文献');
        let failRefresh!: (error: Error) => void;
        vi.mocked(loadPersistedPaperLibrary).mockImplementationOnce(() => new Promise((_, reject) => { failRefresh = reject; }));
        await act(async () => fireEvent.focus(window));
        fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(['pdf'], 'retained.pdf')] } });
        await waitFor(() => expect(paperFromPdf).toHaveBeenCalledOnce());
        await act(async () => failRefresh(new Error('论文库刷新失败')));
        expect(within(dialog).getByRole('alert')).toHaveTextContent('论文库尚未就绪');
        expect(upload).not.toHaveBeenCalled();
        expect(confirmLibraryPdf).not.toHaveBeenCalled();
        fireEvent.click(within(dialog).getByRole('button', { name: '重试识别' }));
        expect(await within(dialog).findByRole('button', { name: '加入论文库' })).toBeEnabled();
        expect(paperFromPdf).toHaveBeenCalledTimes(2);
        expect(vi.mocked(paperFromPdf).mock.calls[1][0].name).toBe('retained.pdf');
        expect(upload).not.toHaveBeenCalled();
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
        expect(confirmLibraryPdf).not.toHaveBeenCalled();
        expect(readLibrary(datasetId)).toEqual(before);
    });
    it("reuses a confirmed upload if saving its paper snapshot fails and is retried", async () => {
        const upload = vi.fn().mockResolvedValue("one-uploaded-document");
        vi.mocked(paperFromPdf).mockResolvedValueOnce(makePaper("new-pdf", "Retry PDF", null));
        await renderLibrary({ onUploadPdf: upload });
        const dialog = await openImportDialog("直接上传", "上传文献");
        fireEvent.change(dialog.querySelector<HTMLInputElement>('input[type="file"]')!, { target: { files: [new File(["pdf"], "retry.pdf")] } });
        const confirm = await within(dialog).findByRole("button", { name: "加入论文库" });
        vi.mocked(confirmLibraryPdf).mockRejectedValueOnce(new Error("数据库保存失败"));
        fireEvent.click(confirm);
        expect(await within(dialog).findByRole("alert")).toHaveTextContent("数据库保存失败");
        expect(upload).toHaveBeenCalledOnce();
        await waitFor(() => expect(confirm).toBeEnabled());
        fireEvent.click(confirm);
        await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
        expect(upload).toHaveBeenCalledOnce();
        expect(readLibrary(datasetId).attachments.find((item) => item.paperId === "new-pdf")?.contentRef).toBe("one-uploaded-document");
    });
    it("protects an edit draft while saving and restores editing after a failed write", async () => {
        let fail!: (error: Error) => void;
        vi.mocked(patchLibraryPaper).mockImplementationOnce(() => new Promise((_, reject) => { fail = reject; }));
        await renderLibrary();
        fireEvent.mouseEnter(screen.getByRole("button", { name: "Folder paper 的更多操作" }));
        fireEvent.click(await screen.findByRole("menuitem", { name: "查看详情" }));
        const dialog = await screen.findByRole("dialog", { name: "文献详情" });
        fireEvent.click(within(dialog).getByRole("button", { name: '编辑题录' }));
        fireEvent.change(within(dialog).getByLabelText("题名"), { target: { value: "Keep this draft" } });
        fireEvent.click(within(dialog).getByRole("button", { name: "保存题录" }));
        await waitFor(() => expect(patchLibraryPaper).toHaveBeenCalledOnce());
        for (const input of within(dialog).getAllByRole("textbox"))
            expect(input).toBeDisabled();
        expect(within(dialog).getByRole("button", { name: "关闭" })).toBeDisabled();
        fireEvent.click(dialog.parentElement!);
        expect(dialog).toBeInTheDocument();
        await act(async () => fail(new Error("服务器保存失败")));
        expect(within(dialog).getByLabelText("题名")).toHaveValue("Keep this draft");
        expect(within(dialog).getByLabelText("题名")).toBeEnabled();
        expect(within(dialog).getByRole("button", { name: "关闭" })).toBeEnabled();
        expect(within(dialog).getByRole("alert")).toHaveTextContent("服务器保存失败");
    });
    it("honors reader access and never writes an arXiv synchronization snapshot", async () => {
        mockReload({ data: readLibrary(datasetId), canWrite: false });
        await renderLibrary();
        expect(screen.getByRole("heading", { name: "Folder paper" })).toBeInTheDocument();
        expect(browse().getByRole("button", { name: /导入/ })).toBeDisabled();
        expect(browse().getByRole("button", { name: "在「论文库」中新建文件夹" })).toBeDisabled();
        expect(within(screen.getByRole("heading", { name: "Folder paper" }).parentElement!).getByRole("button", { name: /取消收藏|取消重点|paperTasks.unmarkImportant/ })).toBeEnabled();
        expect(screen.getByRole("alert")).toHaveTextContent("只读");
        expect(confirmLibraryPdf).not.toHaveBeenCalled();
    });
    it("keeps the personal mark and shared metadata when a star update fails", async () => {
        const before = readLibrary(datasetId);
        vi.mocked(setPaperImportance).mockRejectedValueOnce(new Error('temporarily unavailable'));
        const error = vi.spyOn(message, 'error');
        await renderLibrary();
        const star = within(screen.getByRole('heading', { name: 'Folder paper' }).parentElement!).getByRole('button', { name: /取消重点|paperTasks.unmarkImportant/ });
        await act(async () => fireEvent.click(star));
        await waitFor(() => expect(error).toHaveBeenCalled());
        expect(readLibrary(datasetId).papers[0].important).toBe(true);
        expect(star).toHaveAttribute('aria-pressed', 'true');
        expect(readLibrary(datasetId)).toEqual(before);
        expect(confirmLibraryPdf).not.toHaveBeenCalled();
    });
});
