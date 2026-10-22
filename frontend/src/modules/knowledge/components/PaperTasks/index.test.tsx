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
import { getExpansion, launchPaperTask, listExpansions, resolvePaperSelection, setPaperImportance, type ExpansionDetail, type PaperTask } from '../../api/paperWorkflows';
import { previewPaperImport, startReferenceImport, getPaperImportStatus, type ImportPreviewItem } from '../../api/academicReferences';
vi.mock('../../api/paperWorkflows', () => ({ launchPaperTask: vi.fn(), getExpansion: vi.fn(), listExpansions: vi.fn(), resolvePaperSelection: vi.fn(), retryPaperContent: vi.fn(), deleteExpansion: vi.fn(), setPaperImportance: vi.fn() }));
vi.mock('../../api/academicReferences', () => ({ previewPaperImport: vi.fn(), startReferenceImport: vi.fn(), getPaperImportStatus: vi.fn() }));
const importsChanged = vi.fn();
const i18n = createInstance();
void i18n.init({ lng: 'zh-CN', resources: { 'zh-CN': { translation: zhCN }, 'en-US': { translation: enUS } }, initImmediate: false });
const input = { user_instruction: 'agent research', candidate_directions: ['tools'], selected_directions: ['tools'], papers: [{ paper: { paper_id: 'a', abstract: null, title: 'A' }, is_seed: true, is_important: false }, { paper: { paper_id: 'b', abstract: null, title: 'B' }, is_seed: false, is_important: false }] };
const record = { id: 'r', dataset_id: 'd', conversation_id: 'expansion-chat', title: 'agents', goal: 'expansion', status: 'completed', version: 1, result: input, created_at: '', updated_at: '' };
const detail: ExpansionDetail = { record, paper_importance: {}, chat_status: 'available', rounds: [], contents: [{ id: 'ca', paper_id: 'a', status: 'ready', content_hash: 'a' }, { id: 'cb', paper_id: 'b', status: 'ready', content_hash: 'b' }], handoffs: [] };
const third = { paper: { paper_id: 'c', abstract: null, title: 'C' }, is_seed: false, is_important: false };
const threePapers: ExpansionDetail = { ...detail, record: { ...record, result: { ...input, papers: [...input.papers, third] } }, contents: [...detail.contents, { id: 'cc', paper_id: 'c', status: 'ready', content_hash: 'c' }] };
const preview = (id: string, disposition: ImportPreviewItem['disposition'] = 'downloadable'): ImportPreviewItem => ({ work_id: id, title: id, disposition, presence: { status: '', current_dataset: [], other_datasets: [], importing: false, coverage_complete: true }, fulltext_candidates: [] });
function Harness({ selected = ['a'], canImport = true }: { selected?: string[]; canImport?: boolean }) {
    const [mode, setMode] = useState<PaperTask>();
    const location = useLocation();
    return <><PaperTasks dataset="d" mode={mode} setMode={setMode} selected={selected} canImport={canImport} onImportSubmitted={importsChanged} /><div data-testid="location">{location.pathname}</div></>;
}
function mount(selected?: string[], query = '', canImport = true) {
    return render(<I18nextProvider i18n={i18n}><MemoryRouter initialEntries={['/knowledge' + query]}><ConfigProvider theme={{ token: { motion: false } }}><Harness selected={selected} canImport={canImport} /></ConfigProvider></MemoryRouter></I18nextProvider>);
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
async function openReview() {
    fireEvent.click(await screen.findByRole('button', { name: '生成综述' }));
    return (await screen.findByText('生成论文综述')).closest('[role=dialog]') as HTMLElement;
}
describe('paper task startup routes', () => {
    it('opens a dropdown before entering selected-paper expansion', async () => {
        mount();
        fireEvent.click(screen.getByRole('button', { name: /自动扩展/ }));
        fireEvent.click(await screen.findByText('选择论文扩展'));
        fireEvent.click(screen.getByRole('button', { name: /^继\s*续$/ }));
        expect(screen.getAllByRole('textbox')).toHaveLength(1);
        expect(screen.getByLabelText('补充要求（选填）')).toBeInTheDocument();
        await submit();
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
    it.each([['选择论文生成', false], ['指定方向生成', true]] as const)('starts review through %s', async (label, direction) => {
        mount(direction ? [] : ['a']);
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        fireEvent.click(await screen.findByText(label));
        if (direction) fireEvent.change(screen.getByLabelText('研究主题与要求'), { target: { value: 'agents' } });
        else fireEvent.click(screen.getByRole('button', { name: /^继\s*续$/ }));
        expect(screen.getByText('系统将查找相关论文，并基于结果生成综述')).toBeInTheDocument();
        expect(screen.getAllByRole('textbox')).toHaveLength(1);
        await submit();
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ kind: 'review', document_ids: direction ? [] : ['a'] }));
    });
    it('opens history then confirms the default review selection', async () => {
        mount();
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        fireEvent.click(await screen.findByText('从历史扩展生成'));
        fireEvent.click(await screen.findByRole('button', { name: '查看扩展成果' }));
        const dialog = await openReview();
        expect(launchPaperTask).not.toHaveBeenCalled();
        expect(within(dialog).getByText('已选 2 篇：种子 1 篇，扩展 1 篇')).toBeInTheDocument();
        fireEvent.click(within(dialog).getByRole('button', { name: '确认生成综述（2 篇）' }));
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
        expect(screen.getByRole('button', { name: '加入知识库' })).toBeEnabled();
    });
    it('keeps titles as text when the seed document is missing or the expanded URL is unsafe', async () => {
        vi.mocked(getExpansion).mockResolvedValue({
            ...detail,
            record: { ...record, result: { ...input, papers: [{ ...input.papers[0], paper: { ...input.papers[0].paper, paper_url: 'https://arxiv.org/abs/a' } }, { ...input.papers[1], paper: { ...input.papers[1].paper, paper_url: 'javascript:alert(1)' } }, third] } },
        });
        mount(undefined, '?expansion_id=r');
        await screen.findByRole('button', { name: '生成综述' });
        for (const title of ['A', 'B', 'C']) {
            expect(screen.getByText(title)).toBeInTheDocument();
            expect(screen.queryByRole('link', { name: title })).not.toBeInTheDocument();
        }
    });
});
describe('result action selections', () => {
    it('shows results without checkboxes and submits only the chosen distinct papers', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...threePapers, record: { ...threePapers.record, result: { ...threePapers.record.result, papers: [...threePapers.record.result.papers, third] } } });
        mount(undefined, '?expansion_id=r');
        await screen.findByRole('button', { name: '生成综述' });
        expect(screen.queryAllByRole('checkbox')).toHaveLength(0);
        const dialog = await openReview();
        expect(within(dialog).getByText('已选 3 篇：种子 1 篇，扩展 2 篇')).toBeInTheDocument();
        fireEvent.click(within(dialog).getByRole('checkbox', { name: '选择 C' }));
        fireEvent.click(within(dialog).getByRole('button', { name: '确认生成综述（2 篇）' }));
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ paper_ids: ['a', 'b'] })));
    });
    it('opens with one available paper, disables unavailable ones, and supports cancel', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, chat_status: 'trashed', contents: detail.contents.slice(0, 1) });
        mount(undefined, '?expansion_id=r');
        const dialog = await openReview();
        expect(screen.getByRole('button', { name: '返回论文扩展' })).toBeDisabled();
        expect(within(dialog).getByRole('checkbox', { name: '选择 A' })).toBeChecked();
        expect(within(dialog).getByRole('checkbox', { name: '选择 B' })).toBeDisabled();
        expect(within(dialog).getByRole('button', { name: '确认生成综述（1 篇）' })).toBeDisabled();
        fireEvent.click(within(dialog).getByRole('button', { name: /^取\s*消$/ }));
        expect(launchPaperTask).not.toHaveBeenCalled();
    });
    it('keeps the request on retry and changes it when the paper selection changes', async () => {
        vi.mocked(getExpansion).mockResolvedValue(threePapers);
        vi.mocked(launchPaperTask).mockRejectedValue(new Error('temporary failure'));
        mount(undefined, '?expansion_id=r');
        const dialog = await openReview();
        const confirm = within(dialog).getByRole('button', { name: '确认生成综述（3 篇）' });
        fireEvent.click(confirm);
        await within(dialog).findByText('提交失败，请检查提示后重试，已保留本次选择。');
        fireEvent.click(confirm);
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(confirm).not.toHaveClass('ant-btn-loading'));
        expect(vi.mocked(launchPaperTask).mock.calls[0][0]).toEqual(vi.mocked(launchPaperTask).mock.calls[1][0]);
        fireEvent.click(within(dialog).getByRole('checkbox', { name: '选择 C' }));
        fireEvent.click(within(dialog).getByRole('button', { name: '确认生成综述（2 篇）' }));
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledTimes(3));
        expect(vi.mocked(launchPaperTask).mock.calls[2][0].request_key).not.toBe(vi.mocked(launchPaperTask).mock.calls[0][0].request_key);
    });
    it('pins the selected version while live results are refreshed', async () => {
        vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
        mount(undefined, '?expansion_id=r');
        const dialog = await openReview();
        vi.mocked(getExpansion).mockResolvedValue({ ...threePapers, record: { ...threePapers.record, version: 2 } });
        await act(async () => { vi.advanceTimersByTime(5000); });
        expect(within(dialog).queryByRole('checkbox', { name: '选择 C' })).not.toBeInTheDocument();
        fireEvent.click(within(dialog).getByRole('button', { name: '确认生成综述（2 篇）' }));
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ expansion_version: 1, paper_ids: ['a', 'b'] })));
    });
    it('keeps results closed when an earlier poll finishes after closing', async () => {
        vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
        mount(undefined, '?expansion_id=r');
        const title = await screen.findByText('论文扩展成果');
        const drawer = title.closest('[role=dialog]') as HTMLElement;
        let finish!: (value: ExpansionDetail) => void;
        vi.mocked(getExpansion).mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
        await act(async () => { vi.advanceTimersByTime(5000); });
        fireEvent.click(within(drawer).getByRole('button', { name: /close/i }));
        await act(async () => { finish(detail); });
        await waitFor(() => expect(screen.queryByRole('button', { name: '生成综述' })).not.toBeInTheDocument());
    });
    it('starts import empty, disables unavailable papers, and imports only the chosen paper', async () => {
        vi.mocked(getExpansion).mockResolvedValue(threePapers);
        vi.mocked(getPaperImportStatus).mockResolvedValue('succeeded');
        vi.mocked(startReferenceImport).mockRejectedValueOnce(new Error('offline'));
        vi.mocked(previewPaperImport).mockResolvedValue([preview('a', 'already_present'), preview('b'), preview('c', 'metadata_only')]);
        mount(undefined, '?expansion_id=r');
        const review = await openReview();
        fireEvent.click(within(review).getByRole('button', { name: /^取\s*消$/ }));
        fireEvent.click(screen.getByRole('button', { name: '加入知识库' }));
        const dialog = (await screen.findByText('请选择要加入知识库的论文。已入库、入库中或暂无可下载全文的论文不可选。')).closest('[role=dialog]') as HTMLElement;
        await waitFor(() => expect(within(dialog).getByRole('checkbox', { name: '选择 B' })).toBeEnabled());
        expect(within(dialog).getByText('已选 0 篇：种子 0 篇，扩展 0 篇')).toBeInTheDocument();
        expect(within(dialog).getByRole('checkbox', { name: '选择 A' })).toBeDisabled();
        expect(within(dialog).getByRole('checkbox', { name: '选择 C' })).toBeDisabled();
        fireEvent.click(within(dialog).getByRole('checkbox', { name: '选择 B' }));
        fireEvent.click(within(dialog).getByRole('button', { name: '确认入库（1 篇）' }));
        await within(dialog).findByText('提交失败，请检查提示后重试，已保留本次选择。');
        expect(within(dialog).getByRole('checkbox', { name: '选择 B' })).toBeChecked();
        fireEvent.click(within(dialog).getByRole('button', { name: '确认入库（1 篇）' }));
        await waitFor(() => expect(startReferenceImport).toHaveBeenCalledTimes(2));
        expect(startReferenceImport).toHaveBeenNthCalledWith(1, 'd', [], [preview('b')], expect.any(String));
        expect(startReferenceImport).toHaveBeenNthCalledWith(2, ...vi.mocked(startReferenceImport).mock.calls[0]);
        await waitFor(() => expect(importsChanged).toHaveBeenCalledTimes(2));
        expect(launchPaperTask).not.toHaveBeenCalled();
    });
    it('allows retry after an import preview failure', async () => {
        vi.mocked(previewPaperImport).mockRejectedValueOnce(new Error('offline'));
        mount(undefined, '?expansion_id=r');
        fireEvent.click(await screen.findByRole('button', { name: '加入知识库' }));
        const dialog = (await screen.findByText('请选择要加入知识库的论文。已入库、入库中或暂无可下载全文的论文不可选。')).closest('[role=dialog]') as HTMLElement;
        fireEvent.click(await within(dialog).findByRole('button', { name: /^重\s*试$/ }));
        await waitFor(() => expect(within(dialog).getByRole('checkbox', { name: '选择 A' })).toBeEnabled());
        expect(within(dialog).getByRole('button', { name: '确认入库（0 篇）' })).toBeDisabled();
    });
    it('keeps import permission and removes importance labels from results', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, paper_importance: { a: true } });
        mount(undefined, '?expansion_id=r', false);
        expect(await screen.findByRole('button', { name: '加入知识库' })).toBeDisabled();
        expect(screen.queryByText('重点')).not.toBeInTheDocument();
    });
    it('renders the same task controls in English', async () => {
        await i18n.changeLanguage('en-US');
        mount([]);
        fireEvent.click(screen.getByRole('button', { name: /Auto expansion/ }));
        fireEvent.click(await screen.findByText('Expand a research topic'));
        expect(screen.getByLabelText('Research topic and requirements')).toBeInTheDocument();
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
