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
import { confirmExpansion, deleteExpansion, getExpansion, launchPaperTask, listResearchRecords, resolvePaperSelection, retryPaperContent, setPaperImportance, type ExpansionDetail, type ExpansionReport, type PaperTask } from '../../api/paperWorkflows';
import { previewPaperImport, startReferenceImport, getPaperImportStatus, type ImportPreviewItem } from '../../api/academicReferences';
vi.mock('../../api/paperWorkflows', () => ({ launchPaperTask: vi.fn(), getExpansion: vi.fn(), listResearchRecords: vi.fn(), resolvePaperSelection: vi.fn(), retryPaperContent: vi.fn(), deleteExpansion: vi.fn(), setPaperImportance: vi.fn(), getExpansionCapabilities: vi.fn().mockResolvedValue({ openalex_key: true, sciverse: true, web_search: true }), submitPreparation: vi.fn(), confirmExpansion: vi.fn() }));
vi.mock('../../api/academicReferences', () => ({ previewPaperImport: vi.fn(), startReferenceImport: vi.fn(), getPaperImportStatus: vi.fn(), isImportPreviewExpired: () => false }));
const importsChanged = vi.fn();
const i18n = createInstance();
void i18n.init({ lng: 'zh-CN', resources: { 'zh-CN': { translation: zhCN }, 'en-US': { translation: enUS } }, initImmediate: false });
const input = { user_instruction: 'agent research', candidate_directions: ['tools'], selected_directions: ['tools'], papers: [{ paper: { paper_id: 'a', abstract: null, title: 'A' }, is_seed: true, is_important: false }, { paper: { paper_id: 'b', abstract: null, title: 'B' }, is_seed: false, is_important: false }] };
const record = { id: 'r', preparation_revision: 1, budget_mode: 'balanced' as const, dataset_id: 'd', conversation_id: 'expansion-chat', title: 'agents', goal: 'expansion', status: 'completed', version: 1, result: input, created_at: '', updated_at: '' };
const detail: ExpansionDetail = { record, paper_importance: {}, chat_status: 'available', rounds: [], contents: [{ id: 'ca', paper_id: 'a', status: 'ready', content_hash: 'a', quality: { eligible: true, downloaded: true, parsed: true, identity: 'matched', text: 'usable', material: 'main' } }, { id: 'cb', paper_id: 'b', status: 'ready', content_hash: 'b', quality: { eligible: true, downloaded: true, parsed: true, identity: 'matched', text: 'usable', material: 'main' } }], handoffs: [] };
const third = { paper: { paper_id: 'c', abstract: null, title: 'C' }, is_seed: false, is_important: false };
const threePapers: ExpansionDetail = { ...detail, record: { ...record, result: { ...input, papers: [...input.papers, third] } }, contents: [...detail.contents, { id: 'cc', paper_id: 'c', status: 'ready', content_hash: 'c', quality: { eligible: true, downloaded: true, parsed: true, identity: 'matched', text: 'usable', material: 'main' } }] };
const preview = (id: string, disposition: ImportPreviewItem['disposition'] = 'downloadable'): ImportPreviewItem => ({ work_id: id, title: id, disposition, presence: { status: '', current_dataset: [], other_datasets: [], importing: false, coverage_complete: true }, fulltext_candidates: disposition === 'downloadable' ? [{ url: `https://arxiv.org/pdf/${id}v2`, source_type: 'official_pdf', source_provider: 'arxiv', version: 'v2' }] : [], paper: { paper_id: id, abstract: null, source_provider: 'arxiv', source_version: 'v2' } });
function Harness({ selected = ['a'], canImport = true, selectionError, onNotification, onNotificationResolved }: { selected?: string[]; canImport?: boolean; selectionError?: string; onNotification?: React.ComponentProps<typeof PaperTasks>["onNotification"]; onNotificationResolved?: (id: string) => void }) {
    const [actionsContainer, setActionsContainer] = useState<HTMLDivElement | null>(null);
    const location = useLocation();
    return <><aside aria-label="论文工具栏"><div ref={setActionsContainer} /></aside><main><PaperTasks dataset="d" selected={selected} selectedTitles={selected.map(id => id.toUpperCase())} selectionError={selectionError} canImport={canImport} onImportSubmitted={importsChanged} actionsContainer={actionsContainer} onNotification={onNotification} onNotificationResolved={onNotificationResolved} /></main><div data-testid="location">{location.pathname}</div></>;
}
function mount(selected?: string[], query = '', canImport = true, options: { selectionError?: string; initialMode?: PaperTask; onNotification?: React.ComponentProps<typeof PaperTasks>["onNotification"]; onNotificationResolved?: (id: string) => void } = {}) {
    return render(<I18nextProvider i18n={i18n}><MemoryRouter initialEntries={['/knowledge' + query]}><ConfigProvider theme={{ token: { motion: false } }}><Harness selected={selected} canImport={canImport} {...options} /></ConfigProvider></MemoryRouter></I18nextProvider>);
}
beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(retryPaperContent).mockReset().mockResolvedValue(undefined);
    void i18n.changeLanguage('zh-CN');
    vi.mocked(launchPaperTask).mockResolvedValue({ conversation_id: 'new-chat' });
    vi.mocked(resolvePaperSelection).mockResolvedValue({ papers: input.papers, invalid: [], materials: input.papers.map(item => ({ paper_id: item.paper.paper_id, status: 'ready', eligible: true })) });
    vi.mocked(listResearchRecords).mockResolvedValue({ items: [record], reviews: [] });
    vi.mocked(getExpansion).mockResolvedValue(detail);
    vi.mocked(getPaperImportStatus).mockResolvedValue('pending');
    vi.mocked(previewPaperImport).mockResolvedValue([preview('a'), preview('b')]);
    vi.mocked(startReferenceImport).mockResolvedValue({ batch_id: 'batch' } as Awaited<ReturnType<typeof startReferenceImport>>);
});
afterEach(() => { Modal.destroyAll(); cleanup(); vi.useRealTimers(); });
const submit = async () => {
    fireEvent.click(screen.getByRole('button', { name: /^(开\s*始|开始初步调研)$/ }));
    await waitFor(() => expect(launchPaperTask).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/agent/chat/home/new-chat'));
};
async function selectMultiple(...ids: string[]) {
    await screen.findByRole('checkbox', { name: '选择 A' });
    for (const id of ids) fireEvent.click(screen.getByRole('checkbox', { name: `选择 ${id.toUpperCase()}` }));
}
const reviewButton = () => screen.getByRole('button', { name: '生成综述' });
const confirmReview = () => fireEvent.click(screen.getByRole('button', { name: /确认使用 \d+ 篇生成/ }));
const confirmImport = () => fireEvent.click(screen.getByRole('button', { name: /确认入库/ }));
const cancelConfirmation = () => fireEvent.click(screen.getByRole('button', { name: /取\s*消/ }));
const toggleAll = () => fireEvent.click(within(screen.getByRole('table')).getAllByRole('checkbox')[0]);
const rowImport = (title: string) => screen.getByRole('button', { name: `加入知识库：${title}` });
describe('paper task startup routes', () => {
    it('distinguishes acquired PDFs and retries only unresolved papers while retaining partial failures', async () => {
        const pending = { paper: { paper_id: 'd', title: 'D', abstract: null }, is_seed: false, is_important: false };
        vi.mocked(getExpansion).mockResolvedValue({ ...threePapers,
            record: { ...threePapers.record, result: { ...threePapers.record.result, papers: [...threePapers.record.result.papers, pending] } },
            contents: [detail.contents[0],
                { ...detail.contents[1], status: 'failed', content_hash: '', source_hash: 'b-pdf', error: 'Parser unavailable', quality: { ...detail.contents[1].quality!, eligible: false, parsed: false } },
                { id: 'cd', paper_id: 'd', status: 'processing', content_hash: '', quality: { eligible: false, downloaded: false, parsed: false, identity: 'unconfirmed', text: 'suspect', material: 'unknown', acquisition: { started_at: '2026-10-30T00:00:00Z' } } },
            ],
            presence: [{ work_id: 'b', importing: false, current_dataset: [{ dataset_id: 'd', dataset_name: 'Papers', document_id: 'local-b', document_name: 'B.pdf', content_sha256: 'b-pdf' }] }],
        });
        vi.mocked(retryPaperContent).mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('Queue unavailable'));
        const onNotification = vi.fn();
        mount(undefined, '?expansion_id=r', true, { onNotification });
        expect(await screen.findByText('PDF 已获取，正文处理失败')).toBeVisible();
        expect(screen.getByText('尚未获取 PDF')).toBeVisible();
        expect(screen.getByText('正在获取 PDF')).toBeVisible();
        expect(screen.getByRole('link', { name: 'B' })).toHaveAttribute('href', '/lib/knowledge/knowledge/d/local-b');
        expect(screen.queryByText('全文尚未获取，详情见通知。')).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: '重试未就绪全文（2）' }));
        await waitFor(() => expect(retryPaperContent).toHaveBeenCalledTimes(2));
        expect(retryPaperContent).toHaveBeenCalledWith('b', 'r', true);
        expect(retryPaperContent).toHaveBeenCalledWith('c', 'r', true);
        await waitFor(() => expect(onNotification).toHaveBeenCalledWith(expect.objectContaining({ title: '《C》全文重试失败', message: 'Queue unavailable' })));
        await waitFor(() => expect(screen.getByRole('button', { name: '重试未就绪全文（2）' })).toBeEnabled());
    });

    it('shows both relations, identifies seed papers and expands the full analysis with folded evidence', async () => {
        const seedRelation = '与种子论文共同关注工具调用，并比较训练数据的构建方式。'.repeat(8);
        const directionRelation = '对应工具规模化研究方向，关注工具选择与调用过程。'.repeat(8);
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, result: { ...input, papers: [
            { ...input.papers[0], seed_relation: '这是本次研究的起点' },
            { ...input.papers[1], seed_relation: seedRelation, direction_relation: directionRelation,
                key_contribution: '补充大规模工具调用的研究视角', paper: { ...input.papers[1].paper, abstract: '论文摘要内容' } },
        ] } } });
        mount();
        fireEvent.click(screen.getByRole('button', { name: '研究记录' }));
        fireEvent.click(await screen.findByRole('button', { name: /^查看扩展成果:/ }));
        const seedRow = (await screen.findByRole('checkbox', { name: '选择 A' })).closest('tr')!;
        const expandedRow = screen.getByRole('checkbox', { name: '选择 B' }).closest('tr')!;
        expect(within(seedRow).getByText('种子论文')).toBeVisible();
        expect(within(expandedRow).queryByText('种子论文')).not.toBeInTheDocument();
        expect(within(expandedRow).getByText('与种子论文：')).toBeVisible();
        expect(within(expandedRow).getByText('与研究方向：')).toBeVisible();
        const detailsButton = within(expandedRow).getByRole('button', { name: '查看详情：B' });
        expect(detailsButton).toHaveAttribute('aria-expanded', 'false');
        fireEvent.click(within(expandedRow).getByRole('checkbox', { name: '选择 B' }));
        expect(detailsButton).toHaveAttribute('aria-expanded', 'false');
        fireEvent.click(detailsButton);
        expect(within(expandedRow).getByRole('button', { name: '收起详情：B' })).toHaveAttribute('aria-expanded', 'true');
        expect(within(expandedRow).getByRole('checkbox', { name: '选择 B' })).toBeChecked();
        const analysis = expandedRow.nextElementSibling as HTMLElement;
        expect(within(analysis).getByRole('heading', { name: '与种子论文的关系' })).toBeVisible();
        expect(within(analysis).getByText(seedRelation)).toBeVisible();
        expect(within(analysis).getByRole('heading', { name: '与研究方向的关系' })).toBeVisible();
        expect(within(analysis).getByText(directionRelation)).toBeVisible();
        expect(within(analysis).getByText('补充大规模工具调用的研究视角')).toBeVisible();
        expect(within(analysis).getByText('论文摘要内容')).not.toBeVisible();
        fireEvent.click(within(analysis).getByText('论文摘要'));
        expect(within(analysis).getByText('论文摘要内容')).toBeVisible();
        expect(within(analysis).getByText('实际读取：仅题录与摘要')).not.toBeVisible();
        fireEvent.click(within(analysis).getByText('阅读与证据详情'));
        expect(within(analysis).getByText('实际读取：仅题录与摘要')).toBeVisible();
        expect(within(analysis).queryByText('待核验')).not.toBeInTheDocument();
        fireEvent.click(within(expandedRow).getByRole('button', { name: '收起详情：B' }));
        expect(within(analysis).getByText(seedRelation)).not.toBeVisible();
        expect(within(expandedRow).getByRole('button', { name: '查看详情：B' })).toHaveAttribute('aria-expanded', 'false');
        expect(within(expandedRow).getByRole('checkbox', { name: '选择 B' })).toBeChecked();
        fireEvent.click(within(seedRow).getByRole('button', { name: '查看详情：A' }));
        const seedAnalysis = seedRow.nextElementSibling as HTMLElement;
        expect(within(seedAnalysis).getByRole('heading', { name: '本次扩展的研究起点' })).toBeVisible();
        expect(within(seedAnalysis).queryByRole('heading', { name: '与种子论文的关系' })).not.toBeInTheDocument();
    });

    it.each([{}, { currency_cost: null, stage_timing: [], source_queries: [] }, { currency_cost: 1, compose_metrics: { model: 'test-model', provider_usage_records: [{ prompt_tokens: 100 }] } }])('hides execution details when the report only contains no data or internal metrics: %j', async report => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, report } });
        mount(undefined, '?expansion_id=r');
        await screen.findByText('原始研究要求');
        expect(screen.queryByText('执行详情')).not.toBeInTheDocument();
        expect(screen.queryByText(/货币成本|模型与上游用量记录/)).not.toBeInTheDocument();
    });

    it.each<[ExpansionReport, string]>([
        [{ execution_elapsed_ms: 0 }, '正式执行：0 秒'],
        [{ stage_timing: [{ step_id: 'prepare_plan', attempt: 1, status: 'completed', queue_and_execution_ms: 0 }] }, 'prepare_plan · 0 s · completed'],
        [{ source_queries: [{ query: 'agent research', sources: [{ source: 'arxiv', status: 'ok', count: 0, elapsed_ms: 0 }] }] }, 'agent research：arxiv ok（0）'],
    ])('omits raw execution logs even when they are available: %j', async (report, text) => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, report } });
        mount(undefined, '?expansion_id=r');
        fireEvent.click(await screen.findByRole('button', { name: '查看过程' }));
        expect(screen.queryByText('执行详情')).not.toBeInTheDocument();
        expect(screen.queryByText(text)).not.toBeInTheDocument();
        expect(screen.queryByText(/货币成本|模型与上游用量记录/)).not.toBeInTheDocument();
    });

    it('shows the original request in the research context above supplementary questions', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, status: 'awaiting_info', version: 0,
            preparation: { original_instruction: '原始研究目标', user_instruction: '原始研究目标', summary: '', selected_directions: [], directions: [],
                questions: [{ id: 'scope', question: '关注哪个场景？', options: ['企业知识库'] }] },
        } });
        mount(undefined, '?expansion_id=r');
        const drawer = within((await screen.findByText('论文拓展')).closest('[role=dialog]') as HTMLElement);
        const original = drawer.getByText('原始研究要求').parentElement!;
        expect(within(original).getByText('原始研究目标', { selector: '.ant-typography' })).toBeVisible();
        expect(within(original).queryByRole('textbox')).not.toBeInTheDocument();
        expect(drawer.queryByText('调整研究范围')).not.toBeInTheDocument();
        expect(drawer.getByRole('radio', { name: '企业知识库' })).toBeEnabled();
    });

    it('reports a failed task launch without discarding the instruction and resolves it after retry', async () => {
        const onNotification = vi.fn(); const onNotificationResolved = vi.fn();
        vi.mocked(launchPaperTask).mockRejectedValueOnce(new Error('launch unavailable'));
        mount(['a'], '', true, { onNotification, onNotificationResolved });
        fireEvent.click(screen.getByRole('button', { name: '阅读心得' }));
        fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Keep this instruction' } });
        fireEvent.click(screen.getByRole('button', { name: /^(开\s*始|开始初步调研)$/ }));
        await waitFor(() => expect(onNotification).toHaveBeenCalledWith(expect.objectContaining({ id: 'paper-task-launch', message: 'launch unavailable' })));
        expect(screen.getByRole('textbox')).toHaveValue('Keep this instruction');
        expect(launchPaperTask).toHaveBeenLastCalledWith(expect.objectContaining({ user_instruction: 'Keep this instruction' }), true);
        await submit();
        expect(onNotificationResolved).toHaveBeenCalledWith('paper-task-launch');
    });

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
        expect(resolvePaperSelection).toHaveBeenCalledWith('d', ['a'], false);
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ kind: 'expansion', document_ids: ['a'] }), false);
    });
    it('chooses research depth at launch and resets it for a new task', async () => {
        mount(['a']);
        fireEvent.click(screen.getByRole('button', { name: /自动扩展/ }));
        fireEvent.click(await screen.findByText('基于选中论文扩展'));
        fireEvent.click(screen.getByRole('radio', { name: /深入/ }));
        await submit();
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ budget_mode: 'deep' }), false);
        fireEvent.click(screen.getByRole('button', { name: /自动扩展/ }));
        fireEvent.click(await screen.findByText('基于选中论文扩展'));
        expect(screen.getByRole('radio', { name: /标准/ })).toBeChecked();
    });
    it('starts direction expansion with one topic input and no papers', async () => {
        mount([]);
        fireEvent.click(screen.getByRole('button', { name: /自动扩展/ }));
        fireEvent.click(await screen.findByText('指定方向扩展'));
        expect(screen.getAllByRole('textbox')).toHaveLength(1);
        fireEvent.change(screen.getByLabelText('研究主题与要求'), { target: { value: 'tool agents' } });
        await submit();
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ kind: 'expansion', document_ids: [], direction: 'tool agents', user_instruction: undefined }), false);
    });
    it.each([['基于选中论文生成综述', false], ['指定方向生成', true]] as const)('starts review through %s', async (label, direction) => {
        mount(direction ? [] : ['a']);
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        const menu = within(await screen.findByRole('menu'));
        expect(menu.getAllByRole('menuitem').map(item => item.textContent)).toEqual(['从历史扩展生成', '指定方向生成', expect.stringContaining('基于选中论文生成综述')]);
        fireEvent.click(menu.getByRole('menuitem', { name: new RegExp(label) }));
        if (direction) fireEvent.change(screen.getByLabelText('研究主题与要求'), { target: { value: 'agents' } });
        else fireEvent.click(screen.getByRole('switch', { name: '开启论文扩展' }));
        expect(screen.queryByRole('button', { name: /^继\s*续$/ })).not.toBeInTheDocument();
        expect(screen.getByText('为综述准备文献时，初步调研与检索阅读始终使用此深度。')).toBeInTheDocument();
        const flow = within(screen.getByRole('region', { name: '论文综述流程' }));
        await waitFor(() => {
            for (const stage of ['初步调研', '确认研究方向', '检索与全文准备', '阅读与生成综述']) expect(flow.getByText(stage)).toBeVisible();
        });
        expect(screen.getAllByRole('textbox')).toHaveLength(direction ? 1 : 2);
        await submit();
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ kind: 'review', document_ids: direction ? [] : ['a'] }), false);
    });
    it('hands off usable selected papers and labeled review instructions without expansion', async () => {
        vi.mocked(resolvePaperSelection).mockResolvedValue({ papers: [...input.papers, third], invalid: [], materials: [
            { paper_id: 'a', status: 'ready', eligible: true }, { paper_id: 'b', status: 'ready', eligible: true },
            { paper_id: 'c', status: 'failed', eligible: false, reason: '全文解析失败' },
        ] });
        mount(['a', 'b', 'c']);
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        expect(await screen.findByRole('menuitem', { name: '指定方向生成' })).toHaveAttribute('aria-disabled', 'true');
        fireEvent.click(screen.getByText('基于选中论文生成综述'));
        expect(screen.getByRole('switch', { name: '开启论文扩展' })).not.toBeChecked();
        await waitFor(() => expect(screen.getByText('全文解析失败')).toBeVisible());
        expect(screen.queryByText('扩展深度')).not.toBeInTheDocument();
        expect(screen.queryByText('文献范围')).not.toBeInTheDocument();
        fireEvent.change(screen.getByRole('textbox', { name: /综述主题/ }), { target: { value: '工具学习方法比较' } });
        fireEvent.click(screen.getByRole('button', { name: '确认并继续' }));
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ skip_expansion: true, review_scope: 'selected', paper_ids: ['a', 'b'], document_ids: ['a', 'b', 'c'], user_instruction: '综述主题：工具学习方法比较' }), false));
    });
    it('collapses long selected-paper lists without hiding the totals', async () => {
        const papers = ['a', 'b', 'c', 'd', 'e'].map(id => ({ paper: { paper_id: id, title: id.toUpperCase(), abstract: null }, is_seed: true, is_important: false }));
        vi.mocked(resolvePaperSelection).mockResolvedValue({ papers, invalid: [], materials: papers.map(item => ({ paper_id: item.paper.paper_id, status: 'ready', eligible: true })) });
        mount(['a', 'b', 'c', 'd', 'e']);
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        fireEvent.click(await screen.findByText('基于选中论文生成综述'));
        const toggle = await screen.findByRole('button', { name: '展开全部（5 篇）' });
        await screen.findByText('已选 5 篇：使用 5 篇全文可用论文，0 篇不参与本次生成。');
        expect(screen.queryByText('D', { selector: '.ant-list-item-meta-title' })).not.toBeInTheDocument();
        fireEvent.click(toggle);
        await waitFor(() => expect(screen.getByText('D', { selector: '.ant-list-item-meta-title' })).toBeVisible());
        fireEvent.click(screen.getByRole('button', { name: /收\s*起/ }));
        expect(screen.queryByText('D', { selector: '.ant-list-item-meta-title' })).not.toBeInTheDocument();
        expect(screen.getByRole('button', { name: '确认并继续' })).toBeEnabled();
    });
    it('preserves the selection on a failed material check and lets the user retry', async () => {
        vi.mocked(resolvePaperSelection).mockRejectedValueOnce(new Error('offline'));
        mount(['a', 'b']);
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        fireEvent.click(await screen.findByText('基于选中论文生成综述'));
        await screen.findByText('论文状态加载失败，请重试');
        await waitFor(() => expect(screen.getByText('已选 2 项，论文状态待确认。')).toBeVisible());
        expect(screen.getByText('A', { selector: '.ant-list-item-meta-title' })).toBeVisible();
        expect(screen.getByRole('button', { name: '确认并继续' })).toBeDisabled();
        fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
        await waitFor(() => expect(screen.getByRole('button', { name: '确认并继续' })).toBeEnabled());
        expect(screen.queryByText('论文状态加载失败，请重试')).not.toBeInTheDocument();
        fireEvent.change(screen.getByRole('textbox', { name: /综述主题/ }), { target: { value: '工具学习' } });
        fireEvent.change(screen.getByRole('textbox', { name: '补充要求（选填）' }), { target: { value: '比较训练方法' } });
        fireEvent.click(screen.getByRole('button', { name: '确认并继续' }));
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ user_instruction: '综述主题：工具学习\n\n补充要求：比较训练方法' }), false));
    });
    it('disables both direction entries while library papers are selected', async () => {
        mount(['a']);
        fireEvent.click(screen.getByRole('button', { name: /自动扩展/ }));
        expect(await screen.findByRole('menuitem', { name: '指定方向扩展' })).toHaveAttribute('aria-disabled', 'true');
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        expect(await screen.findByRole('menuitem', { name: '指定方向生成' })).toHaveAttribute('aria-disabled', 'true');
    });
    it('blocks a direct review with fewer than two usable papers', async () => {
        vi.mocked(resolvePaperSelection).mockResolvedValue({ papers: input.papers, invalid: [], materials: [
            { paper_id: 'a', status: 'ready', eligible: true }, { paper_id: 'b', status: 'failed', eligible: false, reason: '全文不可用' },
        ] });
        mount(['a', 'b']);
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        fireEvent.click(await screen.findByText('基于选中论文生成综述'));
        await waitFor(() => expect(screen.getByText('全文不可用')).toBeVisible());
        expect(screen.getByRole('button', { name: '确认并继续' })).toBeDisabled();
        expect(launchPaperTask).not.toHaveBeenCalled();
    });
    it('keeps the review goal and flow when preparing, reopening and confirming directions', async () => {
        const review = { ...record, goal: 'review', status: 'awaiting_confirmation', version: 0,
            preparation: { user_instruction: 'agents', summary: '已找到代表性论文', questions: [], selected_directions: ['tools'],
                directions: [{ name: 'tools', question: '如何选择工具？', reason: '代表性方法', difference: '', paper_ids: ['a'] }] } };
        vi.mocked(launchPaperTask).mockResolvedValue({ expansion_id: 'r' });
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: review });
        vi.mocked(listResearchRecords).mockResolvedValue({ items: [review], reviews: [] });
        vi.mocked(confirmExpansion).mockResolvedValue({ expansion_id: 'r', conversation_id: 'review-preparation-chat' });
        mount();
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        fireEvent.click(await screen.findByText('基于选中论文生成综述'));
        fireEvent.click(screen.getByRole('switch', { name: '开启论文扩展' }));
        fireEvent.click(screen.getByRole('button', { name: '开始初步调研' }));
        const drawer = (await screen.findByText('论文综述 · 资料准备')).closest('[role=dialog]') as HTMLElement;
        const flow = within(within(drawer).getByRole('region', { name: '论文综述流程' }));
        for (const stage of ['初步调研', '确认方向', '检索与阅读', '生成综述']) expect(flow.getByText(stage)).toBeVisible();
        expect(within(drawer).getByText('选择综述关注的方向')).toBeVisible();
        expect(within(drawer).queryByText('正式拓展')).not.toBeInTheDocument();
        fireEvent.click(within(drawer).getByRole('button', { name: /close/i }));
        fireEvent.click(screen.getByRole('button', { name: '研究记录' }));
        fireEvent.click(await screen.findByRole('button', { name: /^查看扩展成果:/ }));
        expect(await screen.findByText('论文综述 · 资料准备')).toBeVisible();
        fireEvent.click(screen.getByRole('button', { name: '确认方向并继续' }));
        await waitFor(() => expect(confirmExpansion).toHaveBeenCalledWith('r', expect.objectContaining({ selected_directions: ['tools'] })));
        await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('review-preparation-chat'));
    });
    describe.each([['自动扩展', '基于选中论文扩展'], ['论文综述', '基于选中论文生成综述']])('%s selection requirements', (task, label) => {
        it('shows the selection hint only while hovering the disabled paper entry', async () => {
            mount([]);
            fireEvent.click(screen.getByRole('button', { name: new RegExp(task) }));
            const entry = await screen.findByRole('menuitem', { name: new RegExp(label) });
            expect(entry).toHaveAttribute('aria-disabled', 'true');
            expect(screen.queryByText('请先选择论文')).not.toBeInTheDocument();
            fireEvent.mouseEnter(within(entry).getByText(label));
            expect(await screen.findByRole('tooltip')).toHaveTextContent('请先选择论文');
            fireEvent.mouseLeave(within(entry).getByText(label));
            await waitFor(() => expect(screen.queryByRole('tooltip')).not.toBeInTheDocument());
            fireEvent.click(within(entry).getByText(label));
            expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
            expect(launchPaperTask).not.toHaveBeenCalled();
        });
        it.each(['所选文献尚未关联入库 PDF，请先添加 PDF', '所选文献的论文身份尚未确认，请先识别或手动确认'])('shows the specific selection problem on hover: %s', async selectionError => {
            mount(['a'], '', true, { selectionError });
            fireEvent.click(screen.getByRole('button', { name: new RegExp(task) }));
            const entry = await screen.findByRole('menuitem', { name: new RegExp(label) });
            expect(entry).toHaveAttribute('aria-disabled', 'true');
            expect(screen.queryByText(selectionError)).not.toBeInTheDocument();
            fireEvent.mouseEnter(within(entry).getByText(label));
            expect(await screen.findByRole('tooltip')).toHaveTextContent(selectionError);
        });
    });
    it.each([['reflection', '阅读心得'], ['ppt', '汇报 PPT']] as const)('starts %s directly from the current library selection', async (kind, label) => {
        mount(['a', 'b']);
        fireEvent.click(screen.getByRole('button', { name: label }));
        expect(screen.queryByRole('menu')).not.toBeInTheDocument();
        expect(screen.queryByRole('button', { name: /^继\s*续$/ })).not.toBeInTheDocument();
        const dialog = within(screen.getByRole('dialog'));
        expect(dialog.getByText('已选 2 篇库中论文')).toBeInTheDocument();
        expect(dialog.getByText('A · B')).toBeInTheDocument();
        fireEvent.change(dialog.getByLabelText('补充要求（选填）'), { target: { value: 'Focus on the main contributions' } });
        if (kind === 'ppt') {
            const reflection = dialog.getByRole('checkbox', { name: '先生成阅读心得，再生成汇报 PPT' });
            expect(reflection).not.toBeChecked();
            fireEvent.click(reflection);
        }
        await submit();
        expect(resolvePaperSelection).toHaveBeenCalledWith('d', ['a', 'b'], false);
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ kind, document_ids: ['a', 'b'], direction: undefined, user_instruction: 'Focus on the main contributions', reflection_first: kind === 'ppt' }), false);
    });
    it.each(['阅读心得', '汇报 PPT'])('shows the selection hint only while hovering disabled %s', async label => {
        mount([]);
        const button = screen.getByRole('button', { name: label });
        expect(button).toBeDisabled();
        expect(screen.queryByText('请先选择论文')).not.toBeInTheDocument();
        fireEvent.mouseEnter(button.parentElement!);
        expect(await screen.findByRole('tooltip')).toHaveTextContent('请先选择论文');
        fireEvent.mouseLeave(button.parentElement!);
        await waitFor(() => expect(screen.queryByRole('tooltip')).not.toBeInTheDocument());
        fireEvent.click(button);
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
        expect(launchPaperTask).not.toHaveBeenCalled();
    });
    it.each(['阅读心得', '汇报 PPT'])('explains an invalid library selection when hovering disabled %s', async label => {
        const selectionError = '所选文献的论文身份尚未确认，请先识别或手动确认';
        mount(['a'], '', true, { selectionError });
        expect(screen.getByRole('button', { name: '阅读心得' })).toBeDisabled();
        expect(screen.getByRole('button', { name: '汇报 PPT' })).toBeDisabled();
        expect(screen.queryByText(selectionError)).not.toBeInTheDocument();
        fireEvent.mouseEnter(screen.getByRole('button', { name: label }).parentElement!);
        expect(await screen.findByRole('tooltip')).toHaveTextContent(selectionError);
    });
    it('opens history and reviews papers selected in the result list', async () => {
        mount();
        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        fireEvent.click(await screen.findByText('从历史扩展生成'));
        fireEvent.click(await screen.findByRole('button', { name: /^查看扩展成果:/ }));
        await selectMultiple('a', 'b');
        fireEvent.click(reviewButton());
        confirmReview();
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ expansion_id: 'r', expansion_version: 1, paper_ids: ['a', 'b'] }), false));
    });
    it('stacks a reopened historical result and its confirmation above their parents', async () => {
        mount(undefined, '?expansion_id=r');
        const initialResult = (await screen.findByText('论文扩展成果')).closest('[role=dialog]') as HTMLElement;
        fireEvent.click(within(initialResult).getByRole('button', { name: /close/i }));
        await waitFor(() => expect(screen.queryByRole('button', { name: '生成综述' })).not.toBeInTheDocument());

        fireEvent.click(screen.getByRole('button', { name: /论文综述/ }));
        fireEvent.click(await screen.findByText('从历史扩展生成'));
        const history = await screen.findByRole('dialog', { name: /研究记录/ });
        fireEvent.click(await within(history).findByRole('button', { name: /^查看扩展成果:/ }));
        await screen.findByRole('checkbox', { name: '选择 A' });
        const result = screen.getByText('论文扩展成果').closest('[role=dialog]') as HTMLElement;
        const resultZIndex = Number(getComputedStyle(result.closest('.ant-drawer')!).zIndex);
        expect(resultZIndex).toBeGreaterThan(Number(getComputedStyle(history.closest('.ant-drawer')!).zIndex));

        await selectMultiple('a', 'b');
        const importButton = within(result).getByRole('button', { name: '批量入库（2 篇）' });
        await waitFor(() => expect(importButton).toBeEnabled());
        fireEvent.click(importButton);
        const confirmation = (await screen.findByText('确认批量入库')).closest('[role=dialog]') as HTMLElement;
        expect(Number(getComputedStyle(confirmation.closest('.ant-modal-wrap')!).zIndex)).toBeGreaterThan(resultZIndex);
        fireEvent.click(within(confirmation).getByRole('button', { name: /取\s*消/ }));
        await waitFor(() => expect(screen.queryByRole('button', { name: '确认入库（2 篇）' })).not.toBeInTheDocument());
        expect(within(result).getByRole('checkbox', { name: '选择 A' })).toBeEnabled();

        fireEvent.click(within(result).getByRole('button', { name: /close/i }));
        await waitFor(() => expect(screen.queryByRole('button', { name: '生成综述' })).not.toBeInTheDocument());
        const refresh = within(history).getByRole('button', { name: /刷\s*新/ });
        expect(refresh).toBeEnabled();
        fireEvent.click(refresh);
        await waitFor(() => expect(listResearchRecords).toHaveBeenCalledTimes(2));
        expect(startReferenceImport).not.toHaveBeenCalled();
    });
    it('returns to paper expansion through a single named action', async () => {
        mount(undefined, '?expansion_id=r');
        fireEvent.click(await screen.findByRole('button', { name: '打开拓展会话' }));
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
            expect(within(screen.getByRole('table')).getByText(title)).toBeInTheDocument();
            expect(screen.queryByRole('link', { name: title })).not.toBeInTheDocument();
        }
    });
});
describe('result list actions', () => {
    it('keeps research details collapsed above the papers after completion', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, goal: 'review', report: { narrative: '研究解读正文' } } });
        mount(undefined, '?expansion_id=r');
        await selectMultiple();
        const reading = screen.getByText('研究解读').closest('details')!;
        const process = screen.getByRole('button', { name: '查看过程' });
        expect(reading).not.toHaveAttribute('open');
        expect(process).toHaveAttribute('aria-expanded', 'false');
        expect(process.compareDocumentPosition(screen.getByRole('table')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        expect(screen.getByRole('region', { name: '论文综述流程', hidden: true })).not.toBeVisible();
        fireEvent.click(process);
        expect(screen.getByRole('region', { name: '论文综述流程' })).toBeVisible();
        fireEvent.click(screen.getByText('研究解读'));
        expect(screen.getByText('研究解读正文')).toBeVisible();
    });
    it('freezes review confirmation inputs and uses a new key for another generation', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...threePapers, contents: detail.contents });
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a', 'b', 'c');
        fireEvent.click(reviewButton());
        vi.mocked(getExpansion).mockResolvedValue(threePapers);
        await act(async () => { window.dispatchEvent(new Event('focus')); });
        expect(screen.getByRole('button', { name: '确认使用 2 篇生成' })).toBeEnabled();
        confirmReview();
        await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/agent/chat/home/new-chat'));
        expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ paper_ids: ['a', 'b'] }), false);
        fireEvent.click(reviewButton());
        expect(screen.getByRole('button', { name: '确认使用 3 篇生成' })).toBeEnabled();
        confirmReview();
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledTimes(2));
        expect(vi.mocked(launchPaperTask).mock.calls[1][0].request_key).not.toBe(vi.mocked(launchPaperTask).mock.calls[0][0].request_key);
    });
    it.each(['pending', 'running', 'active', 'failed', 'cancelled'])('hides intermediate papers and actions while expansion is %s', async status => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, status, report: { narrative: '尚未完成的研究解读' },
            progress: { expansion_plan: { input, plan: ['检索工具调用训练信号'] }, candidate_set: { input }, relation_analysis: { input }, expansion_result: { input } },
        } });
        mount(undefined, '?expansion_id=r');
        await screen.findByRole('region', { name: '拓展进展' });
        expect(screen.queryByRole('table')).not.toBeInTheDocument();
        expect(screen.queryByRole('button', { name: '生成综述' })).not.toBeInTheDocument();
        expect(screen.queryByRole('button', { name: /批量入库|加入知识库|重试获取全文/ })).not.toBeInTheDocument();
        expect(screen.queryByText('研究解读')).not.toBeInTheDocument();
        expect(screen.getByText('完成后统一展示扩展论文和研究解读，届时会通知你。')).toBeVisible();
        expect(screen.getByRole('button', { name: '打开拓展会话' })).toBeEnabled();
        fireEvent.click(screen.getByRole('button', { name: '查看过程' }));
        expect(screen.getAllByText('准备参考论文与检索计划')).toHaveLength(1);
        fireEvent.click(screen.getByText('查看检索计划'));
        expect(screen.getByText('检索工具调用训练信号')).toBeVisible();
        expect(previewPaperImport).not.toHaveBeenCalled();
    });
    it('keeps long research context compact while preserving every confirmed direction and its evidence', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, status: 'running', preparation: {
            original_instruction: '原始研究要求内容', user_instruction: '原始研究要求内容', summary: '初步调研完整正文', questions: [],
            selected_directions: ['训练数据构造', '跨工具迁移'], directions: [
                { name: '训练数据构造', question: '如何构造训练数据？', reason: '数据来源依据', difference: '', paper_ids: [] },
                { name: '跨工具迁移', question: '如何迁移到新工具？', reason: '迁移方向依据', difference: '', paper_ids: [] },
            ],
        } } });
        mount(undefined, '?expansion_id=r');
        const context = await screen.findByRole('region', { name: '本次研究背景' });
        expect(within(context).getByText('原始研究要求内容')).toBeVisible();
        expect(within(context).getByText('训练数据构造')).toBeVisible();
        expect(within(context).queryByText('跨工具迁移')).not.toBeInTheDocument();
        fireEvent.click(within(context).getByRole('button', { name: '查看全部 2 个研究方向' }));
        expect(within(context).getByText('跨工具迁移')).toBeVisible();
        fireEvent.click(within(context).getAllByText('查看依据')[1]);
        expect(within(context).getByText('迁移方向依据')).toBeVisible();
        fireEvent.click(within(context).getByText('查看初步调研'));
        expect(within(context).getByText('初步调研完整正文')).toBeVisible();
        fireEvent.click(within(context).getByRole('button', { name: '收起其余方向' }));
        expect(within(context).queryByText('跨工具迁移')).not.toBeInTheDocument();
    });
    it('reveals papers, insights and actions only after polling observes completion', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, status: 'running', report: { narrative: '研究解读正文' } } });
        mount(undefined, '?expansion_id=r');
        await screen.findByRole('region', { name: '拓展进展' });
        expect(screen.queryByRole('table')).not.toBeInTheDocument();
        expect(previewPaperImport).not.toHaveBeenCalled();
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, report: { narrative: '研究解读正文' } } });
        await act(async () => { window.dispatchEvent(new Event('focus')); });
        await selectMultiple('a', 'b');
        expect(reviewButton()).toBeEnabled();
        await waitFor(() => expect(screen.getByRole('button', { name: '批量入库（2 篇）' })).toBeEnabled());
        expect(previewPaperImport).toHaveBeenCalled();
        fireEvent.click(screen.getByText('研究解读'));
        expect(screen.getByText('研究解读正文')).toBeVisible();
        fireEvent.click(reviewButton());
        expect(screen.getByRole('button', { name: '确认使用 2 篇生成' })).toBeEnabled();
        vi.mocked(getExpansion).mockResolvedValue({ ...detail, record: { ...record, status: 'running' } });
        await act(async () => { window.dispatchEvent(new Event('focus')); });
        await waitFor(() => expect(screen.queryByRole('table')).not.toBeInTheDocument());
        expect(screen.queryByRole('button', { name: '确认使用 2 篇生成' })).not.toBeInTheDocument();
    });
    it('opens an empty review confirmation and explains the required selection', async () => {
        mount(undefined, '?expansion_id=r');
        await selectMultiple();
        expect(reviewButton()).toBeEnabled();
        fireEvent.click(reviewButton());
        expect(screen.getByRole('button', { name: '确认使用 0 篇生成' })).toBeDisabled();
        await waitFor(() => expect(screen.getByText('生成综述至少需要 2 篇全文可用论文，请返回列表调整选择。')).toBeVisible());
        cancelConfirmation();
        expect(launchPaperTask).not.toHaveBeenCalled();
    });
    it('explains when every selected paper is already in the collection', async () => {
        vi.mocked(previewPaperImport).mockResolvedValue([preview('a', 'already_present'), preview('b', 'already_present')]);
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a', 'b');
        fireEvent.click(screen.getByRole('button', { name: '批量入库（0 篇）' }));
        const dialog = screen.getByText('确认批量入库').closest('[role=dialog]') as HTMLElement;
        expect(within(dialog).getByRole('button', { name: '确认入库（0 篇）' })).toBeDisabled();
        expect(within(dialog).getAllByText('已在当前论文集')).toHaveLength(2);
        cancelConfirmation();
        expect(screen.getByRole('checkbox', { name: '选择 A' })).toBeChecked();
        expect(startReferenceImport).not.toHaveBeenCalled();
    });
    it('selects distinct papers and unselects them through the table header', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...threePapers, record: { ...threePapers.record, result: { ...threePapers.record.result, papers: [...threePapers.record.result.papers, input.papers[0]] } } });
        mount(undefined, '?expansion_id=r');
        await selectMultiple();
        expect(screen.getByRole('checkbox', { name: '选择 A' })).not.toBeChecked();
        expect(screen.queryByRole('button', { name: '清空选择' })).not.toBeInTheDocument();
        expect(screen.getByText('已选 0 篇')).toBeInTheDocument();
        toggleAll();
        expect(screen.getByText('已选 3 篇')).toBeInTheDocument();
        toggleAll();
        expect(screen.getByText('已选 0 篇')).toBeInTheDocument();
        expect(screen.getByRole('checkbox', { name: '选择 A' })).not.toBeChecked();
    });
    it('confirms the eligible subset while keeping unavailable papers selected', async () => {
        vi.mocked(getExpansion).mockResolvedValue({ ...threePapers, chat_status: 'trashed', contents: detail.contents });
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a', 'b', 'c');
        expect(screen.getByRole('button', { name: '打开拓展会话' })).toBeDisabled();
        fireEvent.click(reviewButton());
        await waitFor(() => expect(screen.getByText('已选 3 篇：使用 2 篇全文可用论文，1 篇不参与本次生成。')).toBeVisible());
        expect(launchPaperTask).not.toHaveBeenCalled();
        cancelConfirmation();
        expect(screen.getByRole('checkbox', { name: '选择 C' })).toBeChecked();
        expect(launchPaperTask).not.toHaveBeenCalled();
        fireEvent.click(reviewButton());
        confirmReview();
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ paper_ids: ['a', 'b'] }), false));
    });
    it('keeps the request key on retry and changes it when the selection changes', async () => {
        vi.mocked(getExpansion).mockResolvedValue(threePapers);
        vi.mocked(launchPaperTask).mockRejectedValue(new Error('temporary failure'));
        mount(undefined, '?expansion_id=r');
        await selectMultiple('a', 'b');
        fireEvent.click(reviewButton());
        confirmReview();
        await screen.findByText('提交失败，请检查提示后重试，已保留本次选择。');
        confirmReview();
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(screen.getByRole('button', { name: '确认使用 2 篇生成' })).toBeEnabled());
        expect(vi.mocked(launchPaperTask).mock.calls[0][0]).toEqual(vi.mocked(launchPaperTask).mock.calls[1][0]);
        cancelConfirmation();
        fireEvent.click(screen.getByRole('checkbox', { name: '选择 C' }));
        fireEvent.click(reviewButton());
        confirmReview();
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
        expect(previewPaperImport).toHaveBeenLastCalledWith(['a', 'b'], 'd', 'r', 1, false);
        expect(screen.getByText('成果已有更新，本次选择仍使用第 1 版；取消全部勾选后可查看最新结果。')).toBeInTheDocument();
        fireEvent.click(reviewButton());
        confirmReview();
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalledWith(expect.objectContaining({ expansion_version: 1, paper_ids: ['a', 'b'] }), false));
        await screen.findByText('提交失败，请检查提示后重试，已保留本次选择。');
        cancelConfirmation();
        toggleAll();
        await selectMultiple();
        expect(screen.getByRole('checkbox', { name: '选择 C' })).toBeInTheDocument();
        await waitFor(() => expect(previewPaperImport).toHaveBeenLastCalledWith(['a', 'b', 'c'], 'd', 'r', 2, false));
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
        fireEvent.click(reviewButton());
        expect(screen.getByRole('button', { name: '确认使用 0 篇生成' })).toBeDisabled();
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
        fireEvent.click(screen.getByRole('button', { name: '研究记录' }));
        fireEvent.click(await screen.findByRole('button', { name: /^查看扩展成果:/ }));
        await selectMultiple();
        expect(screen.getByText('已选 0 篇')).toBeInTheDocument();
    });
    it('confirms one row, prevents double submission, and refreshes after completion', async () => {
        let finish!: (value: { batch_id: string }) => void;
        vi.mocked(startReferenceImport).mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
        vi.mocked(getPaperImportStatus).mockResolvedValue('succeeded');
        mount(undefined, '?expansion_id=r');
        await waitFor(() => expect(rowImport('B')).toBeEnabled());
        fireEvent.click(rowImport('B'));
        expect(startReferenceImport).not.toHaveBeenCalled();
        confirmImport();
        confirmImport();
        expect(startReferenceImport).toHaveBeenCalledTimes(1);
        await act(async () => { finish({ batch_id: 'batch' }); });
        expect(startReferenceImport).toHaveBeenCalledWith('d', [], [preview('b')], expect.any(String), false);
        await waitFor(() => expect(importsChanged).toHaveBeenCalledTimes(2));
        expect(vi.mocked(getExpansion).mock.calls.length).toBeGreaterThanOrEqual(3);
    });
    it('reports a failed asynchronous import and lets the paper be imported again', async () => {
        vi.mocked(getExpansion).mockImplementation(async () => structuredClone(detail));
        vi.mocked(getPaperImportStatus).mockResolvedValue('failed');
        mount(undefined, '?expansion_id=r');
        await waitFor(() => expect(rowImport('B')).toBeEnabled());
        fireEvent.click(rowImport('B'));
        confirmImport();
        await screen.findByText('部分论文入库失败，请在导入任务中查看原因；也可在列表中重新入库。');
        await waitFor(() => expect(rowImport('B')).toBeEnabled());
        fireEvent.click(rowImport('B'));
        confirmImport();
        await waitFor(() => expect(startReferenceImport).toHaveBeenCalledTimes(2));
        expect(vi.mocked(startReferenceImport).mock.calls[1][3]).not.toEqual(vi.mocked(startReferenceImport).mock.calls[0][3]);
    });
    it('keeps single-paper import request keys when submission fails', async () => {
        vi.mocked(startReferenceImport).mockRejectedValueOnce(new Error('offline'));
        mount(undefined, '?expansion_id=r');
        await waitFor(() => expect(rowImport('B')).toBeEnabled());
        fireEvent.click(rowImport('B'));
        confirmImport();
        await screen.findByText('提交失败，请检查提示后重试，已保留本次选择。');
        confirmImport();
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
        expect(startReferenceImport).toHaveBeenNthCalledWith(1, 'd', [], [preview('b')], expect.any(String), false);
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
        fireEvent.click(screen.getByRole('checkbox', { name: '选择 B' }));
        fireEvent.click(screen.getByRole('button', { name: '批量入库（0 篇）' }));
        expect(screen.getByText('下载来源与所选论文版本不一致，暂不能导入')).toBeInTheDocument();
        expect(screen.queryByText('论文版本已更新，请取消全部勾选后重新选择')).not.toBeInTheDocument();
    });
    it('explains when the selected expansion snapshot is unavailable', async () => {
        vi.mocked(previewPaperImport).mockRejectedValueOnce({ isAxiosError: true, response: { data: { code: 'EXPANSION_VERSION_UNAVAILABLE' } } });
        mount(undefined, '?expansion_id=r');
        await screen.findByText('所选扩展版本不可用，请刷新扩展结果');
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
        confirmReview();
        await waitFor(() => expect(launchPaperTask).toHaveBeenCalled());
    });
    it('uses expansion-result wording and keeps deletion confirmation semantics', async () => {
        vi.mocked(deleteExpansion).mockResolvedValue(undefined);
        mount(undefined, '?expansion_id=r');
        fireEvent.click(await screen.findByRole('button', { name: '删除扩展结果' }));
        expect((await screen.findAllByText('删除扩展结果？')).length).toBeGreaterThan(0);
        expect(screen.getByText('将删除本次扩展结果及对应论文扩展，并停止相关任务。已入库论文与已生成综述保留。')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
        await waitFor(() => expect(deleteExpansion).toHaveBeenCalledWith('r', false));
    });
    it('renders result actions in English', async () => {
        await i18n.changeLanguage('en-US');
        mount(undefined, '?expansion_id=r');
        await screen.findByRole('checkbox', { name: 'Select A' });
        expect(screen.getByRole('button', { name: 'Delete expansion results' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Import selected (0)' })).toBeEnabled();
        expect(screen.getByRole('checkbox', { name: 'Select A' })).toBeInTheDocument();
    });
});
describe('personal importance', () => {
    it('lets the user toggle a personal star and prevents duplicate clicks during submission', async () => {
        let finish!: () => void;
        vi.mocked(setPaperImportance).mockReturnValue(new Promise<void>(resolve => { finish = resolve; }));
        function Mark() {
            const [important, setImportant] = useState(false);
            return <PaperImportanceButton important={important} onToggle={async value => {await setPaperImportance("papers","a",value);setImportant(value)}} />;
        }
        render(<I18nextProvider i18n={i18n}><Mark /></I18nextProvider>);
        fireEvent.click(screen.getByRole('button', { name: '标为重点' }));
        expect(screen.getByRole('button', { name: '标为重点' })).toBeDisabled();
        expect(setPaperImportance).toHaveBeenCalledWith('papers', 'a', true);
        await act(async () => { finish(); });
        expect(screen.getByRole('button', { name: '取消重点' })).toHaveAttribute('aria-pressed', 'true');
    });
    it('keeps the original star state when saving fails', async () => {
        vi.mocked(setPaperImportance).mockRejectedValueOnce(new Error('offline'));
        const changed = vi.fn();
        render(<I18nextProvider i18n={i18n}><PaperImportanceButton important={false} onToggle={async value=>{await setPaperImportance("papers","a",value);changed();}} /></I18nextProvider>);
        fireEvent.click(screen.getByRole('button', { name: '标为重点' }));
        await screen.findByText('重点标记更新失败，请重试');
        expect(screen.getByRole('button', { name: '标为重点' })).toHaveAttribute('aria-pressed', 'false');
        expect(changed).not.toHaveBeenCalled();
    });

});
