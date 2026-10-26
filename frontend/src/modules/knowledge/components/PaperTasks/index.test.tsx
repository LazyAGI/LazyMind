import { useState } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ConfigProvider, Modal } from 'antd';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import zhCN from '@/i18n/locales/zh-CN';
import enUS from '@/i18n/locales/en-US';
import PaperTasks from './index';
import PaperImportanceButton from './PaperImportanceButton';
import { deleteExpansion, getExpansion, launchPaperTask, listExpansions, resolvePaperSelection, setPaperImportance, type ExpansionDetail, type PaperTask } from '../../api/paperWorkflows';
import { previewPaperImport, startReferenceImport, getPaperImportStatus, type ImportPreviewItem } from '../../api/academicReferences';
vi.mock('../../api/paperWorkflows', () => ({ launchPaperTask: vi.fn(), getExpansion: vi.fn(), listExpansions: vi.fn(), resolvePaperSelection: vi.fn(), retryPaperContent: vi.fn(), deleteExpansion: vi.fn(), setPaperImportance: vi.fn() }));
vi.mock('../../api/academicReferences', () => ({ previewPaperImport: vi.fn(), startReferenceImport: vi.fn(), getPaperImportStatus: vi.fn(), isImportPreviewExpired: () => false }));
const importsChanged = vi.fn();
const i18n = createInstance();
void i18n.init({ lng: 'zh-CN', resources: { 'zh-CN': { translation: zhCN }, 'en-US': { translation: enUS } }, initImmediate: false });
const input = { user_instruction: 'agent research', candidate_directions: ['tools'], selected_directions: ['tools'], papers: [{ paper: { paper_id: 'a', abstract: null, title: 'A' }, is_seed: true, is_important: false }, { paper: { paper_id: 'b', abstract: null, title: 'B' }, is_seed: false, is_important: false }] };
const record = { id: 'r', dataset_id: 'd', conversation_id: 'expansion-chat', title: 'agents', goal: 'expansion', status: 'completed', version: 1, result: input, created_at: '', updated_at: '' };
const detail: ExpansionDetail = { record, paper_importance: {}, chat_status: 'available', rounds: [], contents: [{ id: 'ca', paper_id: 'a', status: 'ready', content_hash: 'a' }, { id: 'cb', paper_id: 'b', status: 'ready', content_hash: 'b' }], handoffs: [] };
const third = { paper: { paper_id: 'c', abstract: null, title: 'C' }, is_seed: false, is_important: false };
const threePapers: ExpansionDetail = { ...detail, record: { ...record, result: { ...input, papers: [...input.papers, third] } }, contents: [...detail.contents, { id: 'cc', paper_id: 'c', status: 'ready', content_hash: 'c' }] };
const preview = (id: string, disposition: ImportPreviewItem['disposition'] = 'downloadable'): ImportPreviewItem => ({ work_id: id, title: id, disposition, presence: { status: '', current_dataset: [], other_datasets: [], importing: false, coverage_complete: true }, fulltext_candidates: disposition === 'downloadable' ? [{ url: `https://arxiv.org/pdf/${id}v2`, source_type: 'official_pdf', source_provider: 'arxiv', version: 'v2' }] : [], paper: { paper_id: id, abstract: null, source_provider: 'arxiv', source_version: 'v2' } });
function Harness({ selected = ['a'], canImport = true, selectionError, initialMode }: { selected?: string[]; canImport?: boolean; selectionError?: string; initialMode?: PaperTask }) {
    const [mode, setMode] = useState<PaperTask | undefined>(initialMode);
    const [actionsContainer, setActionsContainer] = useState<HTMLDivElement | null>(null);
    const location = useLocation();
    return <><aside aria-label="论文工具栏"><div ref={setActionsContainer} /></aside><main><PaperTasks dataset="d" mode={mode} setMode={setMode} selected={selected} selectionError={selectionError} canImport={canImport} onImportSubmitted={importsChanged} actionsContainer={actionsContainer} /></main><div data-testid="location">{location.pathname}</div></>;
}
function mount(selected?: string[], query = '', canImport = true, options: { selectionError?: string; initialMode?: PaperTask } = {}) {
    return render(<I18nextProvider i18n={i18n}><MemoryRouter initialEntries={['/knowledge' + query]}><ConfigProvider theme={{ token: { motion: false } }}><Harness selected={selected} canImport={canImport} {...options} /></ConfigProvider></MemoryRouter></I18nextProvider>);
}
beforeEach(() => {
    vi.clearAllMocks();
    void i18n.changeLanguage('zh-CN');
    vi.mocked(launchPaperTask).mockResolvedValue({ conversation_id: 'new-chat' });
    vi.mocked(resolvePaperSelection).mockResolvedValue({ papers: input.papers, invalid: [] });
    vi.mocked(listExpansions).mockResolvedValue([record]);
    vi.mocked(getExpansion).mockResolvedValue(detail);
    vi.mocked(getPaperImportStatus).mockResolvedValue('pending');
    vi.mocked(previewPaperImport).mockResolvedValue([preview('a'), preview('b')]);
    vi.mocked(startReferenceImport).mockResolvedValue({ batch_id: 'batch' } as Awaited<ReturnType<typeof startReferenceImport>>);
});
afterEach(() => { Modal.destroyAll(); cleanup(); vi.useRealTimers(); });
const submit = async () => {
    fireEvent.click(screen.getByRole('button', { name: /^开\s*始$/ }));
    await waitFor(() => expect(launchPaperTask).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/agent/chat/home/new-chat'));
};
async function selectMultiple(...ids: string[]) {
    await screen.findByRole('checkbox', { name: '选择 A' });
    for (const id of ids) fireEvent.click(screen.getByRole('checkbox', { name: `选择 ${id.toUpperCase()}` }));
}
const reviewButton = () => screen.getByRole('button', { name: '生成综述' });
const rowImport = (title: string) => screen.getByRole('button', { name: `加入知识库：${title}` });
describe('paper task startup routes', () => {
    it('opens selected-paper expansion directly with the current selection', async () => {
        mount(['a']);
        const sidebar = within(screen.getByRole('complementary', { name: '论文工具栏' }));
        fireEvent.click(sidebar.getByRole('button', { name: /自动扩展/ }));
        const menu = within(await screen.findByRole('menu'));
        expect(menu.getAllByRole('menuitem').map(item => item.textContent)).toEqual(['指定方向扩展', '基于选中论文扩展']);
        fireEvent.click(menu.getByRole('menuitem', { name: '基于选中论文扩展' }));
        expect(sidebar.getByRole('button', { name: /自动扩展/ })).toHaveAttribute('aria-pressed', 'true');
        expect(screen.queryByRole('button', { name: /^继\s*续$/ })).not.toBeInTheDocument();
        expect(screen.getAllByRole('textbox')).toHaveLength(1);
        expect(screen.getByLabelText('补充要求（选填）')).toBeInTheDocument();
        await submit();
        expect(resolvePaperSelection).toHaveBeenCalledWith('d', ['a']);
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ kind: 'expansion', document_ids: ['a'] }));
    });
    it('starts direction expansion with one topic input and no papers', async () => {
        mount([]);
        fireEvent.click(screen.getByRole('button', { name: /自动扩展/ }));
        fireEvent.click(await screen.findByText('指定方向扩展'));
        expect(screen.getAllByRole('textbox')).toHaveLength(1);
        fireEvent.change(screen.getByLabelText('研究主题与要求'), { target: { value: 'tool agents' } });
        await submit();
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ kind: 'expansion', document_ids: [], direction: 'tool agents', user_instruction: undefined }));
    });
    it.each([['基于选中论文生成综述', false], ['指定方向生成', true]] as const)('starts review through %s', async (label, direction) => {
        mount(direction ? [] : ['a']);
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        const menu = within(await screen.findByRole('menu'));
        expect(menu.getAllByRole('menuitem').map(item => item.textContent)).toEqual(['从历史扩展生成', '指定方向生成', expect.stringContaining('基于选中论文生成综述')]);
        fireEvent.click(menu.getByRole('menuitem', { name: new RegExp(label) }));
        if (direction) fireEvent.change(screen.getByLabelText('研究主题与要求'), { target: { value: 'agents' } });
        expect(screen.queryByRole('button', { name: /^继\s*续$/ })).not.toBeInTheDocument();
        expect(screen.getByText('系统将查找相关论文，并基于结果生成综述')).toBeInTheDocument();
        expect(screen.getAllByRole('textbox')).toHaveLength(1);
        await submit();
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ kind: 'review', document_ids: direction ? [] : ['a'] }));
    });
    describe.each([['自动扩展', '基于选中论文扩展'], ['论文综述', '基于选中论文生成综述']])('%s selection requirements', (task, label) => {
        it('disables the paper entry and explains why when no paper is selected', async () => {
            mount([]);
            fireEvent.click(screen.getByRole('button', { name: new RegExp(task) }));
            const entry = await screen.findByRole('menuitem', { name: new RegExp(label) });
            expect(entry).toHaveAttribute('aria-disabled', 'true');
            await waitFor(() => expect(within(entry).getByText('请至少选择一篇已确认论文')).toBeVisible());
            fireEvent.click(within(entry).getByText(label));
            expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
            expect(launchPaperTask).not.toHaveBeenCalled();
        });
        it.each(['所选文献尚未关联入库 PDF，请先添加 PDF', '所选文献的论文身份尚未确认，请先识别或手动确认'])('displays the selection problem directly: %s', async selectionError => {
            mount(['a'], '', true, { selectionError });
            fireEvent.click(screen.getByRole('button', { name: new RegExp(task) }));
            const entry = await screen.findByRole('menuitem', { name: new RegExp(label) });
            expect(entry).toHaveAttribute('aria-disabled', 'true');
            await waitFor(() => expect(within(entry).getByText(selectionError)).toBeVisible());
        });
    });
    it.each(['expansion', 'review'] as const)('opens the %s action from the library without another selection step', async kind => {
        mount(['a'], '', true, { initialMode: kind });
        expect(await screen.findByLabelText('补充要求（选填）')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: /^继\s*续$/ })).not.toBeInTheDocument();
        await submit();
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ kind, document_ids: ['a'] }));
    });
    it('opens history and reviews papers selected in the result list', async () => {
        mount();
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        fireEvent.click(await screen.findByText('从历史扩展生成'));
        fireEvent.click(await screen.findByRole('button', { name: '查看扩展成果' }));
        await selectMultiple('a', 'b');
        fireEvent.click(reviewButton());
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ expansion_id: 'r', expansion_version: 1, paper_ids: ['a', 'b'] })));
    });
    it('returns to paper expansion through a single named action', async () => {
        mount(undefined, '?expansion_id=r');
        fireEvent.click(await screen.findByRole('button', { name: '返回论文扩展' }));
        expect(screen.getByTestId('location')).toHaveTextContent('/agent/chat/home/expansion-chat');
        expect(launchPaperTask).not.toHaveBeenCalled();
    });
});
describe('result paper title navigation', () => {
    it('opens seeds and matching imported candidates in the current collection reader', async () => {
        const presence = input.papers.map(item => ({ work_id: item.paper.paper_id, importing: false, current_dataset: [{ dataset_id: 'd', dataset_name: 'Papers', document_id: `doc-${item.paper.paper_id}`, document_name: item.paper.title, source_version: 'v2', source_provider: 'arxiv' }] }));
        vi.mocked(getExpansion).mockResolvedValue({
            ...detail, presence,
            record: { ...record, result: { ...input, papers: input.papers.map(item => ({ ...item, paper: { ...item.paper, source_version: 'v2', source_provider: 'arxiv', paper_url: `https://arxiv.org/abs/${item.paper.paper_id}` } })) } },
        });
        render(<I18nextProvider i18n={i18n}><MemoryRouter basename="/app" initialEntries={['/app/knowledge?expansion_id=r']}><ConfigProvider theme={{ token: { motion: false } }}><Harness /></ConfigProvider></MemoryRouter></I18nextProvider>);
        const seed = await screen.findByRole('link', { name: 'A' });
        const expanded = screen.getByRole('link', { name: 'B' });
        expect(seed).toHaveAttribute('href', '/app/lib/knowledge/knowledge/d/doc-a');
        expect(expanded).toHaveAttribute('href', '/app/lib/knowledge/knowledge/d/doc-b');
        for (const link of [seed, expanded]) {
            expect(link).toHaveAttribute('target', '_blank');
            expect(link).toHaveAttribute('rel', 'noopener noreferrer');
        }
    });
    it('opens the DOI landing page when an expanded paper has no original URL', async () => {
        vi.mocked(getExpansion).mockResolvedValue({
            ...detail,
            record: { ...record, result: { ...input, papers: [{ ...input.papers[1], paper: { ...input.papers[1].paper, doi: '10.1234/example' } }] } },
        });
        mount(undefined, '?expansion_id=r');
        expect(await screen.findByRole('link', { name: 'B' })).toHaveAttribute('href', 'https://doi.org/10.1234/example');
    });
    it('keeps another version as an external candidate that can still be imported', async () => {
        vi.mocked(getExpansion).mockResolvedValue({
            ...detail,
            presence: [{ work_id: 'b', importing: false, current_dataset: [{ dataset_id: 'd', dataset_name: 'Papers', document_id: 'old-b', document_name: 'B v1', source_provider: 'arxiv', source_version: 'v1' }] }],
            record: { ...record, result: { ...input, papers: [{ ...input.papers[1], paper: { ...input.papers[1].paper, source_provider: 'arxiv', source_version: 'v2', paper_url: 'https://arxiv.org/abs/2205.14135v2' } }] } },
        });
        mount(undefined, '?expansion_id=r');
        expect(await screen.findByRole('link', { name: 'B' })).toHaveAttribute('href', 'https://arxiv.org/abs/2205.14135v2');
        expect(screen.getByText('已有其他版本')).toBeInTheDocument();
        await waitFor(() => expect(rowImport('B')).toBeEnabled());
    });
    it('keeps titles as text when the seed document is missing or the expanded URL is unsafe', async () => {
        vi.mocked(getExpansion).mockResolvedValue({
            ...detail,
            record: { ...record, result: { ...input, papers: [{ ...input.papers[0], paper: { ...input.papers[0].paper, paper_url: 'https://arxiv.org/abs/a' } }, { ...input.papers[1], paper: { ...input.papers[1].paper, paper_url: 'javascript:alert(1)' } }, third] } },
        });
        mount(undefined, '?expansion_id=r');
        await screen.findByRole('checkbox', { name: '选择 A' });
        for (const title of ['A', 'B', 'C']) {
            expect(screen.getByText(title)).toBeInTheDocument();
            expect(screen.queryByRole('link', { name: title })).not.toBeInTheDocument();
        }
    });
});
describe('result list actions', () => {
    it('shows the running state and keeps both actions visible when no results have been published', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, status: 'running', version: 0, result: { ...input, papers: [] } }, contents: [] });
        mount(undefined, '?expansion_id=r');
        expect(await screen.findByRole('status')).toHaveTextContent('扩展中');
        expect(reviewButton()).toBeDisabled();
        expect(screen.getByRole('button', { name: '批量入库（0 篇）' })).toBeDisabled();
        expect(screen.queryByText(/生成综述至少需要两篇/)).not.toBeInTheDocument();
        fireEvent.focus(reviewButton().parentElement!);
        expect(await screen.findByRole('tooltip')).toHaveTextContent('扩展结果尚未发布');
        expect(launchPaperTask).not.toHaveBeenCalled();
    });
    it('explains why an unselected review is disabled on hover', async () => {
        mount(undefined, '?expansion_id=r');
        await screen.findByRole('checkbox', { name: '选择 A' });
        expect(reviewButton()).toBeDisabled();
        fireEvent.mouseEnter(reviewButton().parentElement!);
        expect(await screen.findByRole('tooltip')).toHaveTextContent('请至少选择两篇论文');
    });
    it('explains when every selected paper is already in the collection', async () => {
        vi.mocked(previewPaperImport).mockResolvedValue([preview('a', 'already_present'), preview('b', 'already_present')]);
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a', 'b');
        const button = screen.getByRole('button', { name: '批量入库（0 篇）' });
        expect(button).toBeDisabled();
        fireEvent.focus(button.parentElement!);
        expect(await screen.findByRole('tooltip')).toHaveTextContent('所选论文暂不可入库：已在当前论文集');
        expect(startReferenceImport).not.toHaveBeenCalled();
    });
    it('shows unchecked papers and disabled actions by default, then selects distinct papers and clears selection', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...threePapers, record: { ...threePapers.record, result: { ...threePapers.record.result, papers: [...threePapers.record.result.papers, third] } } });
        mount(undefined, '?expansion_id=r');
        await screen.findByRole('checkbox', { name: '选择 A' });
        expect(screen.getAllByRole('checkbox')).toHaveLength(4);
        expect(screen.getByText('已选 0 篇')).toBeInTheDocument();
        expect(reviewButton()).toBeDisabled();
        expect(screen.getByRole('button', { name: '批量入库（0 篇）' })).toBeDisabled();
        fireEvent.click(screen.getByRole('checkbox', { name: '选择 A' }));
        fireEvent.click(screen.getByRole('checkbox', { name: '选择 B' }));
        expect(reviewButton()).toBeEnabled();
        fireEvent.click(screen.getByRole('button', { name: '清空选择' }));
        expect(screen.getByText('已选 0 篇')).toBeInTheDocument();
        expect(screen.getByRole('checkbox', { name: '选择 A' })).not.toBeChecked();
        expect(reviewButton()).toBeDisabled();
    });
    it('allows unavailable papers to be selected but requires all selected full texts for review', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...threePapers, chat_status: 'trashed', contents: detail.contents });
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a');
        expect(screen.getByRole('button', { name: '返回论文扩展' })).toBeDisabled();
        expect(reviewButton()).toBeDisabled();
        fireEvent.click(screen.getByRole('checkbox', { name: '选择 B' }));
        expect(reviewButton()).toBeEnabled();
        fireEvent.click(screen.getByRole('checkbox', { name: '选择 C' }));
        expect(screen.getByRole('checkbox', { name: '选择 C' })).toBeChecked();
        expect(reviewButton()).toBeDisabled();
        fireEvent.click(screen.getByRole('checkbox', { name: '选择 C' }));
        fireEvent.click(reviewButton());
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ paper_ids: ['a', 'b'] })));
    });
    it('keeps the request key on retry and changes it when the selection changes', async () => {
        vi.mocked(getExpansion).mockResolvedValue(threePapers);
        vi.mocked(launchPaperTask).mockRejectedValue(new Error('temporary failure'));
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a', 'b');
        fireEvent.click(reviewButton());
        await screen.findByText('提交失败，请检查提示后重试，已保留本次选择。');
        fireEvent.click(reviewButton());
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(reviewButton()).toBeEnabled());
        expect(vi.mocked(launchPaperTask).mock.calls[0][0]).toEqual(vi.mocked(launchPaperTask).mock.calls[1][0]);
        fireEvent.click(screen.getByRole('checkbox', { name: '选择 C' }));
        fireEvent.click(reviewButton());
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledTimes(3));
        expect(vi.mocked(launchPaperTask).mock.calls[2][0].request_key).not.toBe(vi.mocked(launchPaperTask).mock.calls[0][0].request_key);
    });
    it('pins selected papers and result version while polling and loads the latest after clearing selection', async () => {
        vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
        vi.mocked(launchPaperTask).mockRejectedValue(new Error('offline'));
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a', 'b');
        vi.mocked(getExpansion).mockResolvedValue({ ...threePapers, record: { ...threePapers.record, version: 2 } });
        await act(async () => { window.dispatchEvent(new Event('focus')); });
        expect(screen.queryByRole('checkbox', { name: '选择 C' })).not.toBeInTheDocument();
        expect(screen.getByText('成果已有更新，本次选择仍使用第 1 版；清空选择后可查看最新结果。')).toBeInTheDocument();
        fireEvent.click(reviewButton());
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ expansion_version: 1, paper_ids: ['a', 'b'] })));
        await waitFor(() => expect(reviewButton()).toBeEnabled());
        fireEvent.click(screen.getByRole('button', { name: '清空选择' }));
        await selectMultiple();
        expect(screen.getByRole('checkbox', { name: '选择 C' })).toBeInTheDocument();
    });
    it('does not retry full text from a newer result while an older selection is pinned', async () => {
        vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, contents: [{ ...detail.contents[0], status: 'failed', content_hash: '' }] });
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a');
        expect(screen.getByRole('button', { name: '重试获取全文' })).toBeEnabled();
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, version: 2 } });
        await act(async () => { window.dispatchEvent(new Event('focus')); });
        expect(screen.getByRole('button', { name: '重试获取全文' })).toBeDisabled();
        expect(reviewButton()).toBeDisabled();
    });
    it('ignores a poll and preview completing after the drawer is closed and reopens without selection', async () => {
        vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a');
        const drawer = screen.getByText('论文扩展成果').closest('[role=dialog]') as HTMLElement;
        let finish!: (value: ExpansionDetail) => void;
        vi.mocked(getExpansion).mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
        await act(async () => { window.dispatchEvent(new Event('focus')); });
        fireEvent.click(within(drawer).getByRole('button', { name: /close/i }));
        await act(async () => { finish(detail); });
        expect(screen.queryByRole('button', { name: '生成综述' })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: '扩展记录' }));
        fireEvent.click(await screen.findByRole('button', { name: '查看扩展成果' }));
        await selectMultiple();
        expect(screen.getByText('已选 0 篇')).toBeInTheDocument();
    });
    it('imports one row directly, prevents double submission, and refreshes after completion', async () => {
        let finish!: (value: { batch_id: string }) => void;
        vi.mocked(startReferenceImport).mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
        vi.mocked(getPaperImportStatus).mockResolvedValue('succeeded');
        mount(undefined, '?expansion_id=r');
        await waitFor(() => expect(rowImport('B')).toBeEnabled());
        fireEvent.click(rowImport('B'));
        fireEvent.click(rowImport('B'));
        expect(startReferenceImport).toHaveBeenCalledTimes(1);
        expect(screen.queryByText('确认批量入库')).not.toBeInTheDocument();
        await act(async () => { finish({ batch_id: 'batch' }); });
        expect(startReferenceImport).toHaveBeenCalledWith('d', [], [preview('b')], expect.any(String));
        await waitFor(() => expect(importsChanged).toHaveBeenCalledTimes(2));
        expect(vi.mocked(getExpansion).mock.calls.length).toBeGreaterThanOrEqual(3);
    });
    it('reports a failed asynchronous import and lets the paper be imported again', async () => {
        vi.mocked(getExpansion).mockImplementation(async () => structuredClone(detail));
        vi.mocked(getPaperImportStatus).mockResolvedValue('failed');
        mount(undefined, '?expansion_id=r');
        await waitFor(() => expect(rowImport('B')).toBeEnabled());
        fireEvent.click(rowImport('B'));
        await screen.findByText('部分论文入库失败，请在导入任务中查看原因；也可在列表中重新入库。');
        await waitFor(() => expect(rowImport('B')).toBeEnabled());
        fireEvent.click(rowImport('B'));
        await waitFor(() => expect(startReferenceImport).toHaveBeenCalledTimes(2));
        expect(vi.mocked(startReferenceImport).mock.calls[1][3]).not.toEqual(vi.mocked(startReferenceImport).mock.calls[0][3]);
    });
    it('keeps single-paper import request keys when submission fails', async () => {
        vi.mocked(startReferenceImport).mockRejectedValueOnce(new Error('offline'));
        mount(undefined, '?expansion_id=r');
        await waitFor(() => expect(rowImport('B')).toBeEnabled());
        fireEvent.click(rowImport('B'));
        await screen.findByText('提交失败，请检查提示后重试，已保留本次选择。');
        fireEvent.click(rowImport('B'));
        await waitFor(() => expect(startReferenceImport).toHaveBeenCalledTimes(2));
        expect(vi.mocked(startReferenceImport).mock.calls[1]).toEqual(vi.mocked(startReferenceImport).mock.calls[0]);
        await waitFor(() => expect(rowImport('B')).toBeDisabled());
    });
    it('confirms mixed bulk selection without a second selection table and preserves retry input', async () => {
        vi.mocked(getExpansion).mockResolvedValue(threePapers);
        vi.mocked(previewPaperImport).mockResolvedValue([preview('a', 'already_present'), preview('b'), preview('c', 'metadata_only')]);
        vi.mocked(startReferenceImport).mockRejectedValueOnce(new Error('offline'));
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a', 'b', 'c');
        await waitFor(() => expect(screen.getByRole('button', { name: '批量入库（1 篇）' })).toBeEnabled());
        fireEvent.click(screen.getByRole('button', { name: '批量入库（1 篇）' }));
        const dialog = screen.getByText('确认批量入库').closest('[role=dialog]') as HTMLElement;
        expect(within(dialog).queryAllByRole('checkbox')).toHaveLength(0);
        expect(within(dialog).getByText('将入库 1 篇，跳过 2 篇；具体原因如下。')).toBeInTheDocument();
        expect(within(dialog).getByText('已在当前论文集')).toBeInTheDocument();
        expect(within(dialog).getByText('暂无可下载全文')).toBeInTheDocument();
        fireEvent.click(within(dialog).getByRole('button', { name: '确认入库（1 篇）' }));
        await within(dialog).findByText('提交失败，请检查提示后重试，已保留本次选择。');
        fireEvent.click(within(dialog).getByRole('button', { name: '确认入库（1 篇）' }));
        await waitFor(() => expect(startReferenceImport).toHaveBeenCalledTimes(2));
        expect(startReferenceImport).toHaveBeenNthCalledWith(1, 'd', [], [preview('b')], expect.any(String));
        expect(vi.mocked(startReferenceImport).mock.calls[1]).toEqual(vi.mocked(startReferenceImport).mock.calls[0]);
        expect(launchPaperTask).not.toHaveBeenCalled();
    });
    it('enables bibliography-only entries and keeps missing-fulltext and importing entries disabled', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...threePapers, presence: [{ work_id: 'a', metadata_only: true, current_dataset: [], importing: false }] });
        vi.mocked(previewPaperImport).mockResolvedValue([preview('a'), preview('b', 'importing'), preview('c', 'metadata_only')]);
        mount(undefined, '?expansion_id=r');
        await waitFor(() => expect(rowImport('A')).toBeEnabled());
        expect(screen.getByText('已收录，待补全文')).toBeInTheDocument();
        expect(rowImport('B')).toBeDisabled();
        expect(rowImport('C')).toBeDisabled();
        await selectMultiple('a', 'b', 'c');
        expect(screen.getByRole('button', { name: '批量入库（1 篇）' })).toBeEnabled();
    });
    it('does not reuse a preview for a different paper version', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, result: { ...input, papers: [{ ...input.papers[1], paper: { ...input.papers[1].paper, source_provider: 'arxiv', source_version: 'v1' } }] } } });
        mount(undefined, '?expansion_id=r');
        await waitFor(() => expect(previewPaperImport).toHaveBeenCalled());
        expect(rowImport('B')).toBeDisabled();
        expect(startReferenceImport).not.toHaveBeenCalled();
    });
    it('hides import for an already present matching PDF', async () => {
        vi.mocked(previewPaperImport).mockResolvedValue([preview('a', 'already_present'), preview('b')]);
        mount(undefined, '?expansion_id=r');
        await screen.findByText('已在当前论文集');
        expect(screen.queryByRole('button', { name: '加入知识库：A' })).not.toBeInTheDocument();
        expect(rowImport('B')).toBeEnabled();
    });
    it('allows retry after preview failure', async () => {
        vi.mocked(previewPaperImport).mockRejectedValueOnce(new Error('offline'));
        mount(undefined, '?expansion_id=r');
        fireEvent.click(await screen.findByRole('button', { name: /^重\s*试$/ }));
        await waitFor(() => expect(rowImport('A')).toBeEnabled());
    });
    it('allows readonly users to review but disables imports without requesting a preview', async () => {
        mount(undefined, '?expansion_id=r', false);
        await selectMultiple('a', 'b');
        expect(rowImport('B')).toBeDisabled();
        expect(screen.getByRole('button', { name: '批量入库（0 篇）' })).toBeDisabled();
        expect(reviewButton()).toBeEnabled();
        expect(previewPaperImport).not.toHaveBeenCalled();
        fireEvent.click(reviewButton());
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalled());
    });
    it('uses expansion-result wording and keeps deletion confirmation semantics', async () => {
        vi.mocked(deleteExpansion).mockResolvedValue(undefined);
        mount(undefined, '?expansion_id=r');
        fireEvent.click(await screen.findByRole('button', { name: '删除扩展结果' }));
        expect((await screen.findAllByText('删除扩展结果？')).length).toBeGreaterThan(0);
        expect(screen.getByText('将删除本次扩展结果及对应论文扩展，并停止相关任务。已入库论文与已生成综述保留。')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
        await waitFor(() => expect(deleteExpansion).toHaveBeenCalledWith('r'));
    });
    it('renders result actions in English', async () => {
        await i18n.changeLanguage('en-US');
        mount(undefined, '?expansion_id=r');
        await screen.findByRole('checkbox', { name: 'Select A' });
        expect(screen.getByRole('button', { name: 'Delete expansion results' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Import selected (0)' })).toBeDisabled();
        expect(screen.getByRole('checkbox', { name: 'Select A' })).toBeInTheDocument();
    });
});
describe('personal importance', () => {
    it('lets the user toggle a personal star and prevents duplicate clicks during submission', async () => {
        let finish!: () => void;
        vi.mocked(setPaperImportance).mockReturnValue(new Promise<void>(resolve => { finish = resolve; }));
        function Mark() {
            const [important, setImportant] = useState(false);
            return <PaperImportanceButton paperID="a" important={important} onChange={async () => setImportant(true)} />;
        }
        render(<I18nextProvider i18n={i18n}><Mark /></I18nextProvider>);
        fireEvent.click(screen.getByRole('button', { name: '标为重点' }));
        expect(screen.getByRole('button', { name: '标为重点' })).toBeDisabled();
        expect(setPaperImportance).toHaveBeenCalledWith('a', true);
        await act(async () => { finish(); });
        expect(screen.getByRole('button', { name: '取消重点' })).toHaveAttribute('aria-pressed', 'true');
    });
    it('keeps the original star state when saving fails', async () => {
        vi.mocked(setPaperImportance).mockRejectedValueOnce(new Error('offline'));
        const changed = vi.fn();
        render(<I18nextProvider i18n={i18n}><PaperImportanceButton paperID="a" important={false} onChange={changed} /></I18nextProvider>);
        fireEvent.click(screen.getByRole('button', { name: '标为重点' }));
        await screen.findByText('重点标记更新失败，请重试');
        expect(screen.getByRole('button', { name: '标为重点' })).toHaveAttribute('aria-pressed', 'false');
        expect(changed).not.toHaveBeenCalled();
    });

});
