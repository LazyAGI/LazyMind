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
import { loadPersistedPaperLibrary, previewLibraryPapers, patchLibraryPaper, confirmLibraryPdf, createLibraryFolder, removeLibraryPapers } from './store';
import { paperFromPdf } from './pdfMeta';
import { setPaperImportance } from '../api/paperWorkflows';
import { TaskServiceApi } from '../utils/request';
import { previewPaperLink, previewPaperPDF, importPaperLink, type ImportPreviewItem, getAcademicImportBatch, listAcademicImportBatches, type ImportOperation } from '../api/academicReferences';
vi.mock('../utils/request', async original => ({ ...await original<typeof import('../utils/request')>(), TaskServiceApi: vi.fn() }));
vi.mock('@/runtime/readiness', () => ({ waitForRuntimeCapability: vi.fn().mockResolvedValue(undefined) }));
vi.mock('../components/PaperTasks', () => ({ default: vi.fn(() => null) }));
vi.mock('./pdfMeta', () => ({ paperFromPdf: vi.fn() }));
vi.mock('../utils/fileHash', () => ({ computeFileSha256: vi.fn().mockResolvedValue('hash') }));
vi.mock('../api/paperWorkflows', async (original) => ({ ...await original<typeof import('../api/paperWorkflows')>(), setPaperImportance: vi.fn() }));
vi.mock('../api/academicReferences', async (original) => ({ ...await original<typeof import('../api/academicReferences')>(), previewPaperLink: vi.fn(), previewPaperPDF: vi.fn(), importPaperLink: vi.fn(), getAcademicImportBatch: vi.fn(), listAcademicImportBatches: vi.fn() }));
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
    it('opens paper details from the right list while the directory contains only folders', async () => {
        await renderLibrary();
        const tree = browse().getByRole('tree', { name: '论文目录' });
        expect(within(tree).queryByText('Folder paper')).not.toBeInTheDocument();
        fireEvent.click(folderTitle(/第一个文件夹/));
        expect(screen.getByRole('table', { name: '论文列表' })).toBeInTheDocument();
        fireEvent.click(within(screen.getByRole('heading', { name: 'Folder paper' }).closest('tr')!).getByRole('button', { name: /PDF ·/ }));
        expect(await screen.findByRole('dialog', { name: '文献详情' })).toBeInTheDocument();
    });
    it('refreshes import notifications immediately after removing a paper', async () => {
        let removed = false;
        const operation = (): ImportOperation => ({ id: 'upload', entry_type: 'pdf_upload', target_dataset_id: datasetId, source: { file_name: 'paper.pdf' }, status: removed ? 'removed' : 'succeeded', created_at: '2026-10-27T01:00:00Z', summary: { total: 1, ready: removed ? 0 : 1, metadata_only: 0, skipped: 0, active: 0, failed: 0, canceled: 0, deleted: 0, removed: removed ? 1 : 0, resolved_later: 0, retryable: 0, needs_action: 0 } });
        vi.mocked(listAcademicImportBatches).mockImplementation(async () => ({ items: [operation()], next_cursor: '', notification: { active: 0, needs_action: 0 } }));
        vi.mocked(getAcademicImportBatch).mockImplementation(async () => ({ operation: operation(), next_cursor: '', items: [{ item_id: 'upload-item', batch_id: 'upload', work_id: 'work-a', title: 'Imported paper', target_dataset_id: datasetId, library_paper_id: removed ? undefined : 'a', status: 'succeeded', stage: 'done', result_kind: 'imported', current_state: removed ? 'removed' : 'ready', document_deleted: false, resolved_later: false, can_retry: false, current_document: { document_id: 'stored-pdf-a', processing_status: 'ready' }, created_at: '', updated_at: '' }] }));
        vi.mocked(removeLibraryPapers).mockImplementation(async (id, papers) => {
            const ids = papers.map(paper => paper.id), data = readLibrary(id);
            data.papers = data.papers.filter(paper => !ids.includes(paper.id));
            data.attachments = data.attachments.filter(attachment => !ids.includes(attachment.paperId));
            data.notes = data.notes.filter(note => !ids.includes(note.paperId));
            writeLibrary(id, data);
            removed = true;
            return { data: emptyLibrary(), removedPaperIds: ids };
        });
        await renderLibrary();
        fireEvent.click(screen.getByRole('button', { name: /^通知：/ }));
        fireEvent.click(screen.getByRole('button', { name: /^展开导入记录/ }));
        expect(await screen.findByText('已入库')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: '关闭通知' }));
        fireEvent.mouseEnter(screen.getByRole('button', { name: 'Folder paper 的更多操作' }));
        fireEvent.click(await screen.findByRole('menuitem', { name: '从论文集移除' }));
        const confirmation = await screen.findByRole('dialog', { name: '从论文集移除这篇题录？' });
        fireEvent.click(within(confirmation).getByRole('button', { name: /移\s*除/ }));
        await waitFor(() => expect(screen.queryByRole('heading', { name: 'Folder paper' })).not.toBeInTheDocument());
        fireEvent.click(screen.getByRole('button', { name: /^通知：/ }));
        await waitFor(() => expect(screen.getAllByText('已移出论文库')).toHaveLength(2));
        expect(screen.queryByText('已入库')).not.toBeInTheDocument();
        expect(screen.getByRole('link', { name: 'Imported paper' })).toHaveAttribute('href', `/lib/knowledge/knowledge/${datasetId}/stored-pdf-a`);
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
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: /^通知：/ }));
        expect(await screen.findByText('通知暂时不可用', { selector: 'summary' })).toBeInTheDocument();
        fireEvent.click(screen.getByRole('tab', { name: '导入记录' }));
        expect(screen.queryByText('暂无论文导入记录')).not.toBeInTheDocument();
        expect(loadPersistedPaperLibrary).toHaveBeenCalledOnce();
    });
    it('keeps load and sync failures in the bell and clears only the recovered request', async () => {
        vi.mocked(loadPersistedPaperLibrary).mockRejectedValueOnce(new Error('library unavailable'));
        vi.mocked(listAcademicImportBatches).mockRejectedValue(new Error('sync unavailable'));
        await renderLibrary();
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        expect(screen.getByText(/论文库尚未加载成功/)).toBeInTheDocument();
        const bell = screen.getByRole('button', { name: /通知：.*2 条待处理/ });
        fireEvent.click(bell);
        expect(await screen.findByText('论文库加载失败')).toBeInTheDocument();
        expect(screen.getByText('导入记录同步失败')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: '关闭通知' }));
        expect(bell).toHaveAccessibleName(/2 条待处理/);
        fireEvent.click(bell);
        fireEvent.click(await screen.findByRole('button', { name: '重新加载' }));
        expect(await screen.findByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
        await waitFor(() => expect(bell).toHaveAccessibleName(/1 条待处理/));
        expect(screen.queryByText('论文库加载失败')).not.toBeInTheDocument();
        await act(async () => fireEvent.focus(window));
        expect(bell).toHaveAccessibleName(/1 条待处理/);
    });
    it('summarizes failed automatic identification and removes it after manual confirmation', async () => {
        const data = readLibrary(datasetId);
        const attachment = data.attachments[0];
        attachment.academicState = { document_id: attachment.contentRef!, status: 'failed', kind: 'unknown', paper_id: '', manual: false, error: 'recognizer unavailable' };
        writeLibrary(datasetId, data);
        await renderLibrary();
        const bell = screen.getByRole('button', { name: /通知：.*1 条待处理/ });
        fireEvent.click(bell);
        expect(await screen.findByText('《Folder paper》自动识别失败')).toBeInTheDocument();
        const saved = readLibrary(datasetId);
        saved.attachments[0].academicState = { ...attachment.academicState, manual: true, status: 'succeeded' };
        writeLibrary(datasetId, saved);
        await act(async () => fireEvent.focus(window));
        await waitFor(() => expect(bell).toHaveAccessibleName(/0 条待处理/));
    });
    it('keeps a failed reparse submission in the modal and clears its notice after retry', async () => {
        vi.spyOn(console, 'error').mockImplementation(() => {});
        const createTasks = vi.fn().mockRejectedValueOnce(new Error('parser unavailable')).mockResolvedValue({ data: { tasks: [{ task_id: 'parse-task' }] } });
        const startTasks = vi.fn().mockResolvedValue({ data: { started_count: 1 } });
        vi.mocked(TaskServiceApi).mockReturnValue({ createTasks, startTasks } as unknown as ReturnType<typeof TaskServiceApi>);
        await renderLibrary();
        fireEvent.mouseEnter(screen.getByRole('button', { name: 'Folder paper 的更多操作' }));
        fireEvent.click(await screen.findByRole('menuitem', { name: '重新解析' }));
        const dialog = await screen.findByRole('dialog', { name: '重新解析' });
        const confirm = within(dialog).getByRole('button', { name: /确\s*定|OK/ });
        fireEvent.click(confirm);
        expect(await within(dialog).findByText('解析任务尚未提交，请重试或查看通知。')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /通知：.*1 条待处理/ })).toBeInTheDocument();
        expect(startTasks).not.toHaveBeenCalled();
        await waitFor(() => expect(confirm).toBeEnabled());
        fireEvent.click(confirm);
        await waitFor(() => expect(screen.queryByRole('dialog', { name: '重新解析' })).not.toBeInTheDocument());
        expect(startTasks).toHaveBeenCalledOnce();
        expect(screen.getByRole('button', { name: /通知：.*0 条待处理/ })).toBeInTheDocument();
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

    it('shows only folders under the named library and keeps global views independent from nested directories', async () => {
        const data = readLibrary(datasetId);
        data.folders.push({ id: 'nested', name: '子目录', parentId: 'first' });
        data.papers[0].folderId = 'nested';
        writeLibrary(datasetId, data);
        await renderLibrary({ title: '我的论文知识库' });
        const tree = browse().getByRole('tree', { name: '论文目录' });
        expect(within(tree).getByText('我的论文知识库')).toBeInTheDocument();
        expect(within(tree).queryByText('Unfiled paper')).not.toBeInTheDocument();
        expect(within(tree).queryByText('待整理')).not.toBeInTheDocument();
        fireEvent.click(folderTitle(/第一个文件夹/));
        expect(screen.getByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
        expect(document.querySelector(".paper-library-view-heading")).not.toBeInTheDocument();
        expect(screen.queryByLabelText("子目录")).not.toBeInTheDocument();
        fireEvent.click(folderTitle(/子目录/));
        expect(screen.getByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
        fireEvent.click(browse().getByRole('button', { name: /重点论文/ }));
        expect(screen.getByRole('heading', { name: 'Other favorite' })).toBeInTheDocument();
        fireEvent.click(folderTitle(/我的论文知识库/));
        expect(screen.getByRole('heading', { name: 'Unfiled paper' })).toBeInTheDocument();
        expect(screen.getByRole('heading', { name: 'Folder paper' })).toBeInTheDocument();
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
        fireEvent.click(within(screen.getByRole('heading', { name: 'Folder paper' }).closest('tr')!).getByRole('button', { name: /PDF ·/ }));
        const detail = within(screen.getByRole('dialog', { name: '文献详情' }));
        fireEvent.click(detail.getByRole('tab', { name: '附件 2' }));
        expect(detail.getByRole('link', { name: '阅读 PDF' })).toHaveAttribute('href', `/lib/knowledge/knowledge/${datasetId}/${data.attachments[0].contentRef}`);
        expect(open).not.toHaveBeenCalled();
        expect(detail.getByLabelText('附件预览')).toHaveTextContent(data.attachments[0].name);
        fireEvent.change(detail.getByRole('combobox', { name: '选择预览附件与版本' }), { target: { value: 'second-pdf' } });
        expect(detail.getByLabelText('附件预览')).toHaveTextContent(data.attachments.find(item => item.id === 'second-pdf')!.name);
        expect(detail.getByRole('link', { name: '阅读此附件' })).toHaveAttribute('href', `/lib/knowledge/knowledge/${datasetId}/second-doc`);
        expect(open).not.toHaveBeenCalled();
        expect(detail.getByLabelText('附件预览')).toHaveTextContent(data.attachments.find(item => item.id === 'second-pdf')!.name);
        expect(detail.getByRole('link', { name: '阅读 PDF' })).toHaveAttribute('href', `/lib/knowledge/knowledge/${datasetId}/${data.attachments[0].contentRef}`);
        expect(open).not.toHaveBeenCalled();
        expect(detail.getByLabelText('附件预览')).toHaveTextContent(data.attachments.find(item => item.id === 'second-pdf')!.name);
        expect(screen.getByRole('link', { name: 'Folder paper' })).toHaveAttribute('href', `/lib/knowledge/knowledge/${datasetId}/${data.attachments[0].contentRef}`);
        expect(screen.getByRole('link', { name: 'Folder paper' })).not.toHaveAttribute('target', '_blank');
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
        expect(setPaperImportance).toHaveBeenCalledWith(datasetId, 'a', false, true);
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
        expect(tools.getByRole("button", { name: /通知/ })).toHaveAttribute("aria-expanded", "false");
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
        for (const [tool, panel] of [["搜索", "搜索文献"], ["筛选", "筛选文献"], ["通知：0 项进行中 · 0 条待处理", "通知"], ["搜索", "搜索文献"]] as const) {
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
    it("exposes Zotero and backup restore alongside standard imports", async () => {
        await renderLibrary();
        fireEvent.click(browse().getByRole("button", { name: /导入/ }));
        const menu = await screen.findByRole("menu");
        expect(within(menu).getAllByRole("menuitem")).toHaveLength(5);
        expect(within(menu).getByText(/Zotero/)).toBeInTheDocument();
        expect(within(menu).queryByText(/Semantic Scholar/)).not.toBeInTheDocument();
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
        expect(await within(dialog).findByRole("alert")).toHaveTextContent("导入尚未完成");
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
        const notificationButton = libraryTools().getByRole("button", { name: /^通知：/ });
        fireEvent.mouseEnter(screen.getByRole("button", { name: "Folder paper 的更多操作" }));
        fireEvent.click(await screen.findByRole("menuitem", { name: "查看详情" }));
        const dialog = await screen.findByRole("dialog", { name: "文献详情" });
        const details = within(dialog);
        fireEvent.click(details.getByRole("button", { name: "编辑题录" }));
        const titleInput = details.getByLabelText("题名");
        const saveButton = details.getByRole("button", { name: "保存题录" });
        const closeButton = details.getByRole("button", { name: "关闭" });
        fireEvent.change(titleInput, { target: { value: "Keep this draft" } });
        fireEvent.click(saveButton);
        await waitFor(() => expect(patchLibraryPaper).toHaveBeenCalledOnce());
        for (const input of details.getAllByRole("textbox"))
            expect(input).toBeDisabled();
        expect(closeButton).toBeDisabled();
        fireEvent.click(dialog.parentElement!);
        expect(dialog).toBeInTheDocument();
        await act(async () => fail(new Error("服务器保存失败")));
        expect(titleInput).toHaveValue("Keep this draft");
        expect(titleInput).toBeEnabled();
        expect(closeButton).toBeEnabled();
        expect(details.getByText(/修改尚未保存/)).toBeInTheDocument();
        await act(async () => fireEvent.focus(window));
        expect(notificationButton).toBeInTheDocument();
        expect(notificationButton).toHaveAccessibleName(/通知：.*1 条待处理/);
        fireEvent.click(saveButton);
        await waitFor(() => {
            expect(notificationButton).toBeInTheDocument();
            expect(notificationButton).toHaveAccessibleName(/通知：.*0 条待处理/);
        });
        expect(readLibrary(datasetId).papers[0].metadata.title).toBe("Keep this draft");
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
        await waitFor(() => expect(screen.getByRole('button', { name: /通知：.*1 条待处理/ })).toBeInTheDocument());
        expect(error).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', { name: /^通知：/ }));
        expect(await screen.findByText('temporarily unavailable', { selector: 'summary' })).toBeInTheDocument();
        expect(readLibrary(datasetId).papers[0].important).toBe(true);
        expect(star).toHaveAttribute('aria-pressed', 'true');
        expect(readLibrary(datasetId)).toEqual(before);
        expect(confirmLibraryPdf).not.toHaveBeenCalled();
    });
});


describe('DOI import workflow', () => {
    const metadata: ImportPreviewItem = { work_id: 'doi-work', title: 'DOI preview paper', doi: '10.1234/example', disposition: 'metadata_only', fulltext_candidates: [], preview_token: 'metadata-receipt', presence: { status: 'absent', current_dataset: [], other_datasets: [], importing: false, coverage_complete: true } };
    it('shows actual request stages and retains an explicit metadata-only save after PDF timeout', async () => {
        let resolveMetadata!: (item: ImportPreviewItem) => void;
        let rejectPDF!: (error: Error) => void;
        vi.mocked(previewPaperLink).mockReturnValue(new Promise(resolve => { resolveMetadata = resolve; }));
        vi.mocked(previewPaperPDF).mockReturnValue(new Promise((_, reject) => { rejectPDF = reject; }));
        vi.mocked(importPaperLink).mockResolvedValue({ batch_id: 'batch' });
        await renderLibrary();
        const dialog = await openImportDialog('链接', '通过链接导入');
        fireEvent.change(within(dialog).getByRole('textbox', { name: /链接或标识符/ }), { target: { value: '10.1234/example' } });
        fireEvent.click(within(dialog).getByRole('button', { name: '查找文献' }));
        expect(await within(dialog).findByText('正在查询题名、作者与出版信息…')).toBeInTheDocument();
        expect(previewPaperPDF).not.toHaveBeenCalled();
        await act(async () => { resolveMetadata(metadata); });
        expect(within(dialog).getByText(/正在检查开放来源/)).toBeInTheDocument();
        expect(previewPaperLink).toHaveBeenCalledWith('10.1234/example', datasetId, true, false);
        expect(previewPaperPDF).toHaveBeenCalledWith('doi-work', datasetId);
        await act(async () => { rejectPDF(new Error('PDF lookup timeout')); });
        expect(within(dialog).getByText('PDF lookup timeout')).toBeInTheDocument();
        expect(within(dialog).getByText(/未找到可用 PDF/)).toBeInTheDocument();
        expect(importPaperLink).not.toHaveBeenCalled();
        fireEvent.click(within(dialog).getByRole('button', { name: '保存题录' }));
        await waitFor(() => expect(importPaperLink).toHaveBeenCalledWith(datasetId, expect.objectContaining({ preview_token: 'metadata-receipt', fulltext_candidates: [] }), expect.any(String), null, true));
    });
    it('does not show success or offer saving when metadata lookup fails', async () => {
        vi.mocked(previewPaperLink).mockRejectedValue(new Error('Crossref HTTP 503'));
        await renderLibrary();
        const dialog = await openImportDialog('链接', '通过链接导入');
        fireEvent.change(within(dialog).getByRole('textbox', { name: /链接或标识符/ }), { target: { value: '10.1234/example' } });
        fireEvent.click(within(dialog).getByRole('button', { name: '查找文献' }));
        expect(await within(dialog).findByText('当前步骤失败，请查看错误详情后重试。')).toBeInTheDocument();
        expect(previewPaperPDF).not.toHaveBeenCalled();
        expect(within(dialog).queryByRole('button', { name: '保存题录' })).not.toBeInTheDocument();
        expect(importPaperLink).not.toHaveBeenCalled();
    });
});
